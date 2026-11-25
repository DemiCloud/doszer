// Package prepare makes a game-ready copy of a Windows environment for
// DOSBox Pure (README "Preparing a shell"): `doszer prepare shell` turns a
// Windows 3.x system shell into one that runs a game and closes. The result
// is a new file built for running games, not a faithful copy: files Windows
// can do without are left out, and settings are changed.
package prepare

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/DemiCloud/doszer/internal/builtin"
	"github.com/DemiCloud/doszer/internal/interrupt"
	"github.com/DemiCloud/doszer/internal/status"
	"github.com/DemiCloud/doszer/internal/zipw"
)

// ShellOptions configures Shell.
type ShellOptions struct {
	Input  string   // a shell: .dosz/.zip, or a directory of its files
	Output string   // the .dosz to write
	Add    []string // directories whose files are laid over the result, in order
	DryRun bool
	Force  bool // overwrite Output if it exists
}

// Change is one difference between the input and the output.
type Change struct {
	Action string // "added", "replaced", "edited", "removed"
	Path   string
	Why    string
}

// Report is what Shell did, or would do.
type Report struct {
	Output  string
	Changes []Change
	Entries int // entries written, directories included
}

// RunExit's place in a prepared shell: `WIN C:\RUNEXIT\RUNEXIT.EXE GAME.EXE`
// runs the game and exits Windows when it quits, which ends the shell's
// startup batch file and so the DOSBox Pure session (README).
const (
	runExitDir = "RUNEXIT"
	// runExitDateStr dates RunExit's files in the output: its release's date.
	runExitDateStr = "2025-12-23"
)

// entry is one file or directory of the shell, by its own spelling.
type entry struct {
	name     string // slash-separated path as the input spells it
	dir      bool
	modified time.Time
	open     func() (io.ReadCloser, error) // the input's content
	data     []byte                        // replaced content, if non-nil
}

func (e *entry) read() ([]byte, error) {
	if e.data != nil {
		return e.data, nil
	}
	rc, err := e.open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// tree is the shell's entries, keyed case-insensitively as DOS sees them.
type tree map[string]*entry

func key(name string) string {
	return strings.ToUpper(strings.Trim(strings.ReplaceAll(name, `\`, "/"), "/"))
}

func (t tree) get(name string) *entry { return t[key(name)] }

// put sets name's content, keeping an existing entry's spelling, and adds
// any missing parent directories. It returns whether name already existed.
func (t tree) put(name string, data []byte, modified time.Time) bool {
	k := key(name)
	if e := t[k]; e != nil && !e.dir {
		e.data, e.modified = data, modified
		return true
	}
	t[k] = &entry{name: t.spell(name), modified: modified, data: data}
	for d := path.Dir(k); d != "."; d = path.Dir(d) {
		if t[d] == nil {
			t[d] = &entry{name: t.spell(d), dir: true, modified: modified}
		}
	}
	return false
}

// spell returns name with each existing parent directory spelled as t
// already spells it, so a new file joins its directory rather than
// starting a second one that differs only in case.
func (t tree) spell(name string) string {
	name = strings.Trim(strings.ReplaceAll(name, `\`, "/"), "/")
	dir, base := path.Split(name)
	if dir == "" {
		return name
	}
	dir = strings.TrimSuffix(dir, "/")
	if e := t[key(dir)]; e != nil {
		return e.name + "/" + base
	}
	return t.spell(dir) + "/" + base
}

// Shell writes a prepared copy of a Windows 3.x shell (README "Preparing a
// shell"). Nothing is written when opts.DryRun is set.
func Shell(opts ShellOptions) (*Report, error) {
	exe, license, err := builtin.RunExit()
	if err != nil {
		return nil, err
	}
	if !opts.DryRun && !opts.Force {
		if _, err := os.Stat(opts.Output); err == nil {
			return nil, fmt.Errorf("%s already exists (--force to overwrite)", opts.Output)
		}
	}

	status.Step("Reading %s", filepath.Base(opts.Input))
	t, closeInput, err := load(opts.Input)
	if err != nil {
		return nil, err
	}
	defer closeInput()

	for _, need := range []string{"WINDOWS/WIN.COM", "WINDOWS/WIN.INI", "WINDOWS/SYSTEM.INI"} {
		if e := t.get(need); e == nil || e.dir {
			return nil, fmt.Errorf("%s: not a Windows 3.x shell (no %s)", opts.Input, strings.ReplaceAll(need, "/", `\`))
		}
	}

	rep := &Report{Output: opts.Output}
	changed := func(action, name, why string) {
		rep.Changes = append(rep.Changes, Change{action, name, why})
	}

	// Swap and temporary files: a permanent swap file in a shell is
	// dead weight (DOSBox has no 32-bit disk access to use it with), and
	// the rest are leftovers of past sessions.
	for k, e := range t {
		if e.dir {
			continue
		}
		base := path.Base(k)
		why := ""
		switch {
		case base == "386SPART.PAR":
			why = "permanent swap file"
		case base == "WIN386.SWP":
			why = "temporary swap file"
		case strings.HasPrefix(base, "~") && strings.HasSuffix(base, ".TMP"):
			why = "temporary file"
		default:
			continue
		}
		delete(t, k)
		changed("removed", e.name, why)
	}

	type edit struct {
		file, section, key, value string // value "" removes the key
		why                       string
	}
	edits := []edit{
		// Without the permanent swap file Windows would report it corrupt
		// at startup, behind which an unattended start waits forever; with
		// no setting it uses a temporary swap file.
		{"WINDOWS/SYSTEM.INI", "386Enh", "PermSwapDOSDrive", "", "no permanent swap file"},
		{"WINDOWS/SYSTEM.INI", "386Enh", "PermSwapSizeK", "", "no permanent swap file"},
		{"WINDOWS/WIN.INI", "Desktop", "Wallpaper", "(None)", "black desktop"},
		{"WINDOWS/WIN.INI", "Desktop", "Pattern", "(None)", "black desktop"},
		{"WINDOWS/WIN.INI", "colors", "Background", "0 0 0", "black desktop"},
	}
	inis := map[string]*iniFile{}
	orig := map[string][]byte{}
	var iniOrder []string
	for _, ed := range edits {
		f := inis[ed.file]
		if f == nil {
			b, err := t.get(ed.file).read()
			if err != nil {
				return nil, fmt.Errorf("%s: %w", ed.file, err)
			}
			f = parseINI(b)
			inis[ed.file] = f
			orig[ed.file] = b
			iniOrder = append(iniOrder, ed.file)
		}
		var did bool
		if ed.value == "" {
			did = f.remove(ed.section, ed.key)
		} else {
			did = f.set(ed.section, ed.key, ed.value)
		}
		if did {
			changed("edited", fmt.Sprintf("%s [%s] %s", t.get(ed.file).name, ed.section, ed.key), ed.why)
		}
	}
	for _, name := range iniOrder {
		e := t.get(name)
		if b := inis[name].bytes(); !bytes.Equal(b, orig[name]) {
			e.data = b
		}
	}

	runExitDate, _ := time.Parse(time.DateOnly, runExitDateStr)
	for _, f := range []struct {
		name string
		data []byte
	}{
		{runExitDir + "/RUNEXIT.EXE", exe},
		{runExitDir + "/LICENSE.TXT", crlf(license)},
	} {
		action := "added"
		if t.put(f.name, f.data, runExitDate) {
			action = "replaced"
		}
		changed(action, t.get(f.name).name, "RunExit "+builtin.RunExitVersion)
	}

	for _, dir := range opts.Add {
		if err := overlay(t, dir, changed); err != nil {
			return nil, err
		}
	}

	names := t.output()
	rep.Entries = len(names)
	if opts.DryRun {
		return rep, nil
	}

	status.Step("Writing %s", filepath.Base(opts.Output))
	if err := t.write(opts.Output, names); err != nil {
		return rep, err
	}
	return rep, nil
}

// crlf converts LF line endings to CRLF, for a text file read under DOS.
func crlf(b []byte) []byte {
	return bytes.ReplaceAll(bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")), []byte("\n"), []byte("\r\n"))
}

// load reads a shell's entries from a ZIP or a directory.
func load(p string) (tree, func(), error) {
	fi, err := os.Stat(p)
	if err != nil {
		return nil, nil, err
	}
	t := tree{}
	if fi.IsDir() {
		err := filepath.WalkDir(p, func(abs string, d fs.DirEntry, err error) error {
			if err != nil || abs == p {
				return err
			}
			rel, _ := filepath.Rel(p, abs)
			info, err := d.Info()
			if err != nil {
				return err
			}
			e := &entry{name: filepath.ToSlash(rel), dir: d.IsDir(), modified: info.ModTime()}
			if !e.dir {
				e.open = func() (io.ReadCloser, error) { return os.Open(abs) }
			}
			t[key(e.name)] = e
			return nil
		})
		return t, func() {}, err
	}

	zr, err := zip.OpenReader(p)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", p, err)
	}
	for _, f := range zr.File {
		name := strings.Trim(strings.ReplaceAll(f.Name, `\`, "/"), "/")
		if name == "" || strings.HasPrefix(name, "/") || strings.Contains("/"+name+"/", "/../") {
			continue
		}
		e := &entry{name: name, dir: f.FileInfo().IsDir(), modified: dosModified(&f.FileHeader)}
		if !e.dir {
			e.open = f.Open
		}
		k := key(name)
		if old := t[k]; old != nil && !old.dir && !e.dir {
			zr.Close()
			return nil, nil, fmt.Errorf("%s: %s appears twice (DOS names are case-insensitive)", p, name)
		}
		t[k] = e
		for d := path.Dir(name); d != "."; d = path.Dir(d) {
			if t[key(d)] == nil {
				t[key(d)] = &entry{name: d, dir: true, modified: e.modified}
			}
		}
	}
	return t, func() { zr.Close() }, nil
}

// dosModified returns the entry's MS-DOS date and time fields, the
// timestamp DOSBox Pure shows (src/dos/drive_zip.cpp reads only those), as
// a UTC time zipw writes back unchanged. f.Modified prefers an extra field
// (e.g. NTFS times, in UTC) when one is present, which differs from the DOS
// fields by the time zone of whoever made the ZIP.
func dosModified(f *zip.FileHeader) time.Time {
	d, tm := f.ModifiedDate, f.ModifiedTime //nolint:staticcheck // the DOS fields are the point
	return time.Date(int(d>>9)+1980, time.Month(d>>5&0xF), int(d&0x1F), int(tm>>11), int(tm>>5&0x3F), int(tm&0x1F)*2, 0, time.UTC)
}

// overlay lays dir's files over t.
func overlay(t tree, dir string, changed func(action, name, why string)) error {
	return filepath.WalkDir(dir, func(abs string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, abs)
		data, err := os.ReadFile(abs)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		action := "added"
		if t.put(filepath.ToSlash(rel), data, info.ModTime()) {
			action = "replaced"
		}
		changed(action, t.get(rel).name, "from "+dir)
		return nil
	})
}

// output returns the keys of the entries to write, in name order: every
// file, and every directory with no file under it (an empty directory such
// as WINDOWS\TEMP still has to exist).
func (t tree) output() []string {
	full := map[string]bool{}
	for k, e := range t {
		if !e.dir {
			for d := path.Dir(k); d != "."; d = path.Dir(d) {
				full[d] = true
			}
		}
	}
	var keys []string
	for k, e := range t {
		if !e.dir || !full[k] {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return t[keys[i]].name < t[keys[j]].name })
	return keys
}

// write writes the entries to output as a deterministic ZIP, beside it
// first and renamed into place once complete.
func (t tree) write(output string, keys []string) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(output), ".doszer-prepare-*.dosz")
	if err != nil {
		return err
	}
	done := interrupt.Remove(tmp.Name())
	defer func() {
		if err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
		}
		done()
	}()

	zw := zipw.New(tmp)
	zw.TempDir = filepath.Dir(output)
	for _, k := range keys {
		e := t[k]
		if e.dir {
			if err := zw.Add(e.name+"/", bytes.NewReader(nil), true, e.modified); err != nil {
				return err
			}
			continue
		}
		var rc io.ReadCloser = io.NopCloser(bytes.NewReader(e.data))
		if e.data == nil {
			if rc, err = e.open(); err != nil {
				return fmt.Errorf("%s: %w", e.name, err)
			}
		}
		err = zw.Add(e.name, rc, false, e.modified)
		rc.Close()
		if err != nil {
			return fmt.Errorf("%s: %w", e.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), output)
}
