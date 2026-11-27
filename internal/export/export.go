// Package export copies archives into a frontend's folders, with what
// they need: for RetroDECK, a game into roms/<system>/ (ES-DE's system
// folders for DOSBox Pure: dos, windows3x, windows9x), its parents
// beside it (DOSBox Pure looks for a parent in the archive's own
// directory: src/dos/drive_zip.cpp, zipDriveImpl), and the shell or OS
// image it boots into the BIOS folder, DOSBox Pure's system directory, at
// the path AUTOBOOT.DBP names (dosbox_pure_run.h, ReadAutoBoot). Or the
// same layout into a ZIP, to unpack into a RetroDECK folder.
package export

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DemiCloud/doszer/internal/chain"
	"github.com/DemiCloud/doszer/internal/config"
	"github.com/DemiCloud/doszer/internal/install"
	"github.com/DemiCloud/doszer/internal/interrupt"
	"github.com/DemiCloud/doszer/internal/platform"
	"github.com/DemiCloud/doszer/internal/zipw"
)

// Target is where archives go: a ROM folder holding a folder per system,
// and the BIOS folder (DOSBox Pure's system directory).
type Target struct {
	ROMs, BIOS string
	From       string // where they came from: "--dir", "RetroDECK", "system directory"
	Flat       bool   // every game in ROMs itself, no folder per system
	zip        bool   // into a new ZIP: ROMs and BIOS are its roms and bios
}

// Systems are the ES-DE system folders archives go in, by System.
var Systems = []string{"dos", "windows3x", "windows9x"}

// Filter keeps the archives whose System is one of systems.
func Filter(archives, systems []string) []string {
	var out []string
	for _, a := range archives {
		for _, s := range systems {
			if System(chain.Resolve(a)) == s {
				out = append(out, a)
				break
			}
		}
	}
	return out
}

// FindRetroDECK finds RetroDECK's folders: dir's roms/ and bios/ if dir
// is given; else RetroDECK's settings (retrodeck.json); else the parent
// of systemDir when it is a bios/ beside a roms/ (a synced copy of
// RetroDECK's folder).
func FindRetroDECK(dir, systemDir string) (Target, error) {
	isDir := func(p string) bool { fi, err := os.Stat(p); return err == nil && fi.IsDir() }
	if dir != "" {
		t := Target{ROMs: filepath.Join(dir, "roms"), BIOS: filepath.Join(dir, "bios"), From: "--dir"}
		if !isDir(t.ROMs) || !isDir(t.BIOS) {
			return Target{}, fmt.Errorf("%s: not a RetroDECK folder (no roms and bios folders)", dir)
		}
		return t, nil
	}
	if r := config.RetroDECK(); r.ROMs != "" && r.BIOS != "" {
		return Target{ROMs: r.ROMs, BIOS: r.BIOS, From: config.FromRetroDECK}, nil
	}
	if systemDir != "" && strings.EqualFold(filepath.Base(systemDir), "bios") {
		if roms := filepath.Join(filepath.Dir(systemDir), "roms"); isDir(roms) {
			return Target{ROMs: roms, BIOS: systemDir, From: "system directory"}, nil
		}
	}
	return Target{}, errors.New("no RetroDECK folder found: give one with --dir")
}

// System is the ES-DE system folder an archive goes in, from how it
// boots: DOS, a Windows 3.x shell, or an OS image (Windows 3.x or 9x,
// by its name; 9x when the name doesn't tell).
func System(c *chain.Chain) string {
	switch c.Kind {
	case chain.KindShell:
		if install.GuessOS(install.SystemFile{Name: c.Boot, Shell: true}).Platform == platform.Win32 {
			return "windows9x"
		}
		return "windows3x"
	case chain.KindOS:
		if install.GuessOS(install.SystemFile{Name: c.Boot}).Platform == platform.Win16 {
			return "windows3x"
		}
		return "windows9x"
	}
	return "dos"
}

// Kinds of step.
const (
	Copy     = "copy"     // not there yet
	Replace  = "replace"  // there, different: copied with overwrite
	Same     = "same"     // there, the same size and time
	Conflict = "conflict" // there, different, and kept
	Missing  = "missing"  // the source isn't here
)

// Step is one file of an export.
type Step struct {
	What   string `json:"what" yaml:"what" toml:"what"` // game, parent, system
	From   string `json:"from" yaml:"from" toml:"from"`
	To     string `json:"to" yaml:"to" toml:"to"`
	Action string `json:"action" yaml:"action" toml:"action"`
}

// Plan works out the steps exporting archives takes: each archive, the
// parents its chain names, and its shell or OS image from systemDir.
// overwrite replaces archives that differ; a shell or OS image is never
// replaced, only copied when missing (the copy there may have been
// changed by playing, and is the frontend's).
func Plan(archives []string, systemDir string, t Target, overwrite bool) ([]Step, error) {
	var steps []Step
	var clash error
	seen := map[string]string{} // destination, lowercased: its source
	add := func(what, from, to string, replaceable bool) {
		key := strings.ToLower(to)
		if prev, ok := seen[key]; ok {
			if prev != from && clash == nil {
				clash = fmt.Errorf("%s and %s both go to %s", prev, from, filepath.ToSlash(to))
			}
			return
		}
		seen[key] = from
		s := Step{What: what, From: from, To: to, Action: Copy}
		src, err := os.Stat(from)
		dst, derr := os.Stat(to)
		if t.zip {
			s.To, derr = filepath.ToSlash(to), os.ErrNotExist // a new file: nothing there
		}
		switch {
		case err != nil:
			s.Action = Missing
		case derr != nil:
		case dst.Size() == src.Size() && sameTime(dst.ModTime(), src.ModTime()):
			s.Action = Same
		case overwrite && replaceable:
			s.Action = Replace
		default:
			s.Action = Conflict
		}
		steps = append(steps, s)
	}
	for _, a := range archives {
		c := chain.Resolve(a)
		dir := t.ROMs
		if !t.Flat {
			dir = filepath.Join(dir, System(c))
		}
		for i, l := range c.Links {
			what := "game"
			if i > 0 || strings.EqualFold(filepath.Ext(l.Path), ".dosp") {
				what = "parent"
			}
			add(what, l.Path, filepath.Join(dir, filepath.Base(l.Path)), true)
		}
		if c.Kind == chain.KindShell || c.Kind == chain.KindOS {
			c.Locate(systemDir)
			if c.System == "" {
				if there := *c; !t.zip { // not here, but there already?
					if there.Locate(t.BIOS); there.System != "" {
						if _, ok := seen[strings.ToLower(there.System)]; !ok {
							seen[strings.ToLower(there.System)] = there.System
							steps = append(steps, Step{What: "system", From: there.System, To: there.System, Action: Same})
						}
						continue
					}
				}
				add("system", filepath.Join(systemDir, filepath.FromSlash(c.Boot)), filepath.Join(t.BIOS, filepath.FromSlash(c.Boot)), false)
				continue
			}
			rel, err := filepath.Rel(systemDir, c.System)
			if err != nil {
				return nil, err
			}
			add("system", c.System, filepath.Join(t.BIOS, rel), false)
		}
	}
	return steps, clash
}

// Apply copies the steps that copy or replace, keeping each file's
// modification time (so a later Plan sees it the same). Each goes to a
// temporary file beside its destination first, renamed into place.
func Apply(steps []Step, progress func(Step)) error {
	for _, s := range steps {
		if s.Action != Copy && s.Action != Replace {
			continue
		}
		if progress != nil {
			progress(s)
		}
		if err := copyFile(s.From, s.To); err != nil {
			return err
		}
	}
	return nil
}

// PlanZip is Plan into a new ZIP: each step's To is its path in the
// archive, roms/<system>/... and bios/..., RetroDECK's layout; flat,
// every game in roms/ itself.
func PlanZip(archives []string, systemDir string, flat bool) ([]Step, error) {
	return Plan(archives, systemDir, Target{ROMs: "roms", BIOS: "bios", Flat: flat, zip: true}, false)
}

// ZipName is the ZIP an export into dir writes: doszer <date>.zip, or
// doszer <date> (2).zip and so on past the ones there.
func ZipName(dir string, now time.Time) string {
	base := "doszer " + now.Format("2006-01-02")
	p := filepath.Join(dir, base+".zip")
	for n := 2; ; n++ {
		if _, err := os.Lstat(p); errors.Is(err, os.ErrNotExist) {
			return p
		}
		p = filepath.Join(dir, fmt.Sprintf("%s (%d).zip", base, n))
	}
}

// WriteZip writes the steps that copy into a new ZIP at path, each with
// its file's modification time. Archives and shells are ZIPs already
// and are stored; OS images are deflated. It goes to a temporary file
// beside path first, renamed into place, and never replaces a file.
func WriteZip(path string, steps []Step, progress func(Step)) (err error) {
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("%s: already there", path)
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".doszer-export-*.zip")
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
	w := zipw.New(tmp)
	w.TempDir = dir
	for _, s := range steps {
		if s.Action != Copy {
			continue
		}
		if progress != nil {
			progress(s)
		}
		if err := addFile(w, s); err != nil {
			return err
		}
	}
	if err := w.Close(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil { // CreateTemp's is 0600
		return err
	}
	if _, err := os.Lstat(path); err == nil { // appeared meanwhile
		return fmt.Errorf("%s: already there", path)
	}
	return os.Rename(tmp.Name(), path)
}

func addFile(w *zipw.Writer, s Step) error {
	f, err := os.Open(s.From)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	ext := strings.ToLower(filepath.Ext(s.From))
	store := ext == ".dosz" || ext == ".dosp" || ext == ".zip"
	// zipw writes a time's UTC fields (a build mustn't depend on the
	// time zone); unpacking reads them as local time, so give it the
	// local wall clock: an unpacked file keeps its time, and a later
	// export into that folder finds it the same (Plan's sameTime).
	m := fi.ModTime()
	m = time.Date(m.Year(), m.Month(), m.Day(), m.Hour(), m.Minute(), m.Second(), 0, time.UTC)
	if err := w.Add(s.To, f, store, m); err != nil {
		return fmt.Errorf("%s: %w", s.From, err)
	}
	return nil
}

// sameTime compares modification times to FAT's two seconds: an SD card
// or a synced copy may round them.
func sameTime(a, b time.Time) bool {
	d := a.Sub(b)
	return d < 2*time.Second && d > -2*time.Second
}

func copyFile(from, to string) error {
	src, err := os.Open(from)
	if err != nil {
		return err
	}
	defer src.Close()
	fi, err := src.Stat()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(to), ".doszer-export-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, src); err != nil {
		tmp.Close()
		return fmt.Errorf("%s: %w", to, err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chtimes(tmp.Name(), fi.ModTime(), fi.ModTime()); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), to)
}
