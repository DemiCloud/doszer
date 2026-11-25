package prepare

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/DemiCloud/doszer/internal/builtin"
	"github.com/DemiCloud/doszer/internal/fat"
	"github.com/DemiCloud/doszer/internal/interrupt"
	"github.com/DemiCloud/doszer/internal/status"
	"github.com/DemiCloud/doszer/internal/vhd"
)

// OSOptions configures OS.
type OSOptions struct {
	Input  string   // a Windows 9x OS image: VHD (fixed or dynamic) or raw .img
	Output string   // the image to write: a fixed VHD, or raw for raw input
	Add    []string // directories whose files are laid over C:, in order
	DryRun bool
	Force  bool // overwrite Output if it exists
}

// startRun is C:\WINDOWS\STARTRUN.VBS, the shell a prepared image starts
// instead of Explorer (README "Windows 9x OS image"): the game's
// D:\WINDOWS.BAT, hidden, then shut down; without one, Program Manager.
// The batch file's output goes to NUL: a hidden DOS window that has shown
// any output stays open, finished, when the batch file ends, so the
// script waiting on it would never shut Windows down (headless probe,
// 2026-11-25: one ECHO was enough).
const startRun = `Set fso = CreateObject("Scripting.FileSystemObject")
Set sh  = CreateObject("WScript.Shell")
If fso.FileExists("D:\WINDOWS.BAT") Then
    sh.Run "COMMAND.COM /C D:\WINDOWS.BAT >NUL", 0, True
    sh.Run "RUNDLL32.EXE SHELL32.DLL,SHExitWindowsEx 1", 0, False
Else
    sh.Run "C:\WINDOWS\PROGMAN.EXE", 1, False
End If
`

// toolsGroup is Program Manager's Tools group: what's left to work with
// when the image starts without a game, Explorer not being the shell.
var toolsGroup = []grpItem{
	{"Shutdown", `C:\WINDOWS\RUNDLL32.EXE SHELL32.DLL,SHExitWindowsEx 1`, `C:\WINDOWS\RUNDLL32.EXE`},
	{"Windows Explorer", `C:\WINDOWS\EXPLORER.EXE`, `C:\WINDOWS\EXPLORER.EXE`},
	{"Registry Editor", `C:\WINDOWS\REGEDIT.EXE`, `C:\WINDOWS\REGEDIT.EXE`},
	{"Control Panel", `C:\WINDOWS\CONTROL.EXE`, `C:\WINDOWS\CONTROL.EXE`},
	{"Notepad", `C:\WINDOWS\NOTEPAD.EXE`, `C:\WINDOWS\NOTEPAD.EXE`},
	{"Edit STARTRUN.VBS", `C:\WINDOWS\NOTEPAD.EXE C:\WINDOWS\STARTRUN.VBS`, `C:\WINDOWS\NOTEPAD.EXE`},
	{"Edit SYSTEM.INI", `C:\WINDOWS\NOTEPAD.EXE C:\WINDOWS\SYSTEM.INI`, `C:\WINDOWS\NOTEPAD.EXE`},
}

var (
	// generatedDate dates the files doszer writes itself (spec §8.2's
	// epoch for generated files).
	generatedDate = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)
	presskeyDate  = time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC) // its release
)

// image is an open OS image.
type image struct {
	disk   io.ReaderAt
	size   int64
	footer []byte // a fixed VHD footer; nil for a raw image
	close  func() error
}

func openImage(p string) (*image, error) {
	d, err := vhd.Open(p)
	if err == nil {
		return &image{disk: d, size: d.Size(), footer: d.FixedFooter(), close: d.Close}, nil
	}
	if !errors.Is(err, vhd.ErrNotVHD) {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	return &image{disk: f, size: fi.Size(), close: f.Close}, nil
}

// OS writes a prepared copy of a Windows 9x OS image (README "Preparing
// an OS image"). Nothing is written when opts.DryRun is set.
func OS(opts OSOptions) (*Report, error) {
	pk, pkLicense, err := builtin.Presskey()
	if err != nil {
		return nil, err
	}
	if err := listedName(opts.Output, ".vhd", ".img", ".ima"); err != nil {
		return nil, err
	}
	if !opts.DryRun {
		if err := checkOutput(opts.Input, opts.Output, opts.Force); err != nil {
			return nil, err
		}
	}

	status.Step("Reading %s", filepath.Base(opts.Input))
	img, err := openImage(opts.Input)
	if err != nil {
		return nil, err
	}
	defer img.close()
	vol, err := fat.Open(img.disk)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", opts.Input, err)
	}
	root, err := vol.Tree()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", opts.Input, err)
	}

	win := root.Child("WINDOWS")
	switch {
	case root.Child("IO.SYS") == nil || win == nil || !win.IsDir() || win.Child("WIN.COM") == nil || win.Child("SYSTEM.INI") == nil:
		return nil, fmt.Errorf("%s: not a Windows 9x image (no IO.SYS, WINDOWS\\WIN.COM or WINDOWS\\SYSTEM.INI)", opts.Input)
	case win.Child("WSCRIPT.EXE") == nil:
		return nil, fmt.Errorf("%s: no WINDOWS\\WSCRIPT.EXE: STARTRUN.VBS needs Windows Script Host", opts.Input)
	case win.Child("WININIT.INI") != nil:
		return nil, fmt.Errorf("%s: an installation is waiting to finish (WINDOWS\\WININIT.INI): boot the image once first", opts.Input)
	}

	rep := &Report{Output: opts.Output}
	changed := func(action, name, why string) {
		rep.Changes = append(rep.Changes, Change{action, name, why})
	}

	// Leftovers of past sessions, each rule reported once.
	type rule struct {
		where string // directory, "" for the root
		match func(n *fat.Node) bool
		label string
		why   string
	}
	all := func(*fat.Node) bool { return true }
	rules := []rule{
		{"RECYCLED", func(n *fat.Node) bool { return !n.Is("desktop.ini") }, `RECYCLED\*`, "Recycle Bin"},
		{"WINDOWS/TEMP", all, `WINDOWS\TEMP\*`, "temporary files"},
		{"WINDOWS/Recent", all, `WINDOWS\Recent\*`, "recent documents list"},
		{"WINDOWS", func(n *fat.Node) bool { return n.Is("WIN386.SWP") }, `WINDOWS\WIN386.SWP`, "swap file"},
		{"", func(n *fat.Node) bool {
			s := strings.ToUpper(n.ShortName())
			return s == "BOOTLOG.PRV" || s == "SCANDISK.LOG" || (n.IsDir() && len(s) == 9 && strings.HasPrefix(s, "FOUND.") && strings.Trim(s[6:], "0123456789") == "")
		}, `BOOTLOG.PRV, SCANDISK.LOG, FOUND.*`, "boot log and ScanDisk leftovers"},
	}
	for _, r := range rules {
		dir := lookup(root, r.where)
		if dir == nil || !dir.IsDir() {
			continue
		}
		files, bytes := 0, int64(0)
		dir.Children = filter(dir.Children, func(n *fat.Node) bool {
			if n.IsLabel() || !r.match(n) {
				return true
			}
			f, b := count(n)
			files, bytes = files+f, bytes+b
			return false
		})
		if files > 0 {
			changed("removed", r.label, fmt.Sprintf("%s: %d files, %s", r.why, files, sizeString(bytes)))
		}
	}
	tmpFiles, tmpBytes := 0, int64(0)
	walk(root, func(dir *fat.Node) {
		dir.Children = filter(dir.Children, func(n *fat.Node) bool {
			s := strings.ToUpper(n.ShortName())
			if n.IsDir() || n.IsLabel() || !strings.HasPrefix(s, "~") || !strings.HasSuffix(s, ".TMP") {
				return true
			}
			tmpFiles, tmpBytes = tmpFiles+1, tmpBytes+n.Size
			return false
		})
	})
	if tmpFiles > 0 {
		changed("removed", `~*.TMP`, fmt.Sprintf("temporary files: %d files, %s", tmpFiles, sizeString(tmpBytes)))
	}

	// Settings.
	type edit struct {
		file, section, key, value, why string
	}
	edits := []edit{
		{"SYSTEM.INI", "boot", "shell", `wscript.exe C:\WINDOWS\STARTRUN.VBS`, "start STARTRUN.VBS instead of Explorer"},
	}
	for _, ed := range edits {
		n := win.Child(ed.file)
		if n == nil {
			return nil, fmt.Errorf("no WINDOWS\\%s", ed.file)
		}
		b, err := readNode(n)
		if err != nil {
			return nil, fmt.Errorf("WINDOWS\\%s: %w", ed.file, err)
		}
		ini := parseINI(b)
		if ini.set(ed.section, ed.key, ed.value) {
			setContent(n, ini.bytes())
			changed("edited", fmt.Sprintf(`WINDOWS\%s [%s] %s`, n.Name(), ed.section, ed.key), ed.why)
		}
	}

	// Program Manager's Tools group, listed in PROGMAN.INI.
	pm := win.Child("PROGMAN.INI")
	if pm == nil {
		if pm, err = addFile(win, "PROGMAN.INI", nil, generatedDate); err != nil {
			return nil, err
		}
	}
	b, err := readNode(pm)
	if err != nil {
		return nil, fmt.Errorf("WINDOWS\\PROGMAN.INI: %w", err)
	}
	ini := parseINI(b)
	if addGroup(ini, `C:\WINDOWS\TOOLS.GRP`) {
		setContent(pm, ini.bytes())
		changed("edited", `WINDOWS\PROGMAN.INI [Groups]`, "lists TOOLS.GRP")
	}

	for _, f := range []struct {
		name string
		data []byte
		date time.Time
		why  string
	}{
		{"STARTRUN.VBS", crlf([]byte(startRun)), generatedDate, "runs D:\\WINDOWS.BAT, then shuts down"},
		{"TOOLS.GRP", buildGRP("Tools", toolsGroup), generatedDate, "Program Manager group"},
		{"PRESSKEY.EXE", pk, presskeyDate, "PRESSKEY " + builtin.PresskeyVersion},
		{"PRESSKEY.TXT", crlf(pkLicense), presskeyDate, "PRESSKEY's licence"},
	} {
		action := "added"
		if win.Child(f.name) != nil {
			action = "replaced"
		}
		if _, err := addFile(win, f.name, f.data, f.date); err != nil {
			return nil, err
		}
		changed(action, `WINDOWS\`+f.name, f.why)
	}

	for _, dir := range opts.Add {
		if err := overlayFAT(root, dir, changed); err != nil {
			return nil, err
		}
	}

	// IO.SYS and MSDOS.SYS first, in the root and on the disk: what DOS's
	// boot sector expects, though Windows 9x's finds IO.SYS anywhere.
	rank := func(c *fat.Node) int {
		switch {
		case c.IsLabel():
			return 0
		case c.Is("IO.SYS"):
			return 1
		case c.Is("MSDOS.SYS"):
			return 2
		}
		return 3
	}
	slices.SortStableFunc(root.Children, func(a, b *fat.Node) int { return rank(a) - rank(b) })

	walk(root, func(dir *fat.Node) {
		for _, c := range dir.Children {
			if !c.IsLabel() {
				rep.Entries++
			}
		}
	})
	if opts.DryRun {
		return rep, nil
	}

	status.Step("Writing %s", filepath.Base(opts.Output))
	return rep, writeImage(opts.Output, img, vol.Layout(), root)
}

// listedName refuses an output name DOSBox Pure wouldn't list in its
// system directory: it lists OS images by extension (.img, .ima, .vhd)
// and shells by .dosz (dosbox_pure_libretro.cpp, DBP_ScanSystem). Which
// of the image extensions doesn't matter to it: it tells a VHD by its
// footer (src/ints/bios_disk.cpp, imageDisk::imageDisk, sparseVhd::Init).
func listedName(output string, exts ...string) error {
	ext := filepath.Ext(output)
	for _, e := range exts {
		if strings.EqualFold(ext, e) {
			return nil
		}
	}
	return fmt.Errorf("%s: DOSBox Pure lists only %s files there; name it %s", filepath.Base(output), strings.Join(exts, ", "), strings.TrimSuffix(filepath.Base(output), ext)+exts[0])
}

// checkOutput refuses an existing output without force, and the input
// itself in any case: an OS image in use (e.g. a frontend's system
// directory) must never be written to.
func checkOutput(input, output string, force bool) error {
	fo, err := os.Stat(output)
	if err != nil {
		return nil
	}
	if fi, err := os.Stat(input); err == nil && os.SameFile(fi, fo) {
		return errors.New("the output is the input: write a new image")
	}
	if !force {
		return fmt.Errorf("%s already exists (--force to overwrite)", output)
	}
	return nil
}

// writeImage writes the rebuilt volume as a new image beside output and
// renames it into place once complete: the disk's sectors before the
// volume's data (MBR, boot sector, FSInfo, backup boot sector) copied, the
// file system rebuilt in the same geometry, then a fixed VHD footer.
func writeImage(output string, img *image, l fat.Layout, root *fat.Node) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(output), ".doszer-prepare-*"+filepath.Ext(output))
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

	total := img.size + int64(len(img.footer))
	if err := tmp.Truncate(total); err != nil {
		return err
	}
	prefix := l.Base + int64(l.ReservedSectors)*int64(l.BytesPerSector)
	if _, err := io.Copy(io.NewOffsetWriter(tmp, 0), io.NewSectionReader(img.disk, 0, prefix)); err != nil {
		return fmt.Errorf("copying the boot sectors: %w", err)
	}
	if err := fat.Write(tmp, l, root); err != nil {
		return err
	}
	if img.footer != nil {
		if _, err := tmp.WriteAt(img.footer, img.size); err != nil {
			return err
		}
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), output)
}

// addGroup lists group in PROGMAN.INI's [Groups] and [Settings] Order,
// unless it already is, and reports whether ini changed.
func addGroup(ini *iniFile, group string) bool {
	free := 0
	for i := 1; i < 100; i++ {
		v, ok := ini.get("Groups", fmt.Sprintf("Group%d", i))
		switch {
		case ok && strings.EqualFold(v, group):
			return false
		case !ok && free == 0:
			free = i
		}
	}
	ini.set("Groups", fmt.Sprintf("Group%d", free), group)
	order, _ := ini.get("Settings", "Order")
	ini.set("Settings", "Order", strings.TrimSpace(order+" "+fmt.Sprint(free)))
	return true
}

// lookup finds a slash-separated path under root, "" being root itself.
func lookup(root *fat.Node, p string) *fat.Node {
	n := root
	if p == "" {
		return n
	}
	for _, part := range strings.Split(p, "/") {
		if n = n.Child(part); n == nil {
			return nil
		}
	}
	return n
}

func filter(ns []*fat.Node, keep func(*fat.Node) bool) []*fat.Node {
	out := ns[:0]
	for _, n := range ns {
		if keep(n) {
			out = append(out, n)
		}
	}
	return out
}

// walk calls fn for every directory under (and including) dir.
func walk(dir *fat.Node, fn func(*fat.Node)) {
	fn(dir)
	for _, c := range dir.Children {
		if c.IsDir() {
			walk(c, fn)
		}
	}
}

// count returns the files and bytes in n (a file, or a directory's tree).
func count(n *fat.Node) (int, int64) {
	if !n.IsDir() {
		return 1, n.Size
	}
	files, bytes := 0, int64(0)
	for _, c := range n.Children {
		if !c.IsLabel() {
			f, b := count(c)
			files, bytes = files+f, bytes+b
		}
	}
	return files, bytes
}

func readNode(n *fat.Node) ([]byte, error) {
	if n.Open == nil || n.Size == 0 {
		return nil, nil
	}
	r, err := n.Open()
	if err != nil {
		return nil, err
	}
	if c, ok := r.(io.Closer); ok {
		defer c.Close()
	}
	return io.ReadAll(r)
}

func setContent(n *fat.Node, data []byte) {
	n.Size = int64(len(data))
	n.Open = func() (io.Reader, error) { return bytes.NewReader(data), nil }
}

// addFile puts data at name in dir, replacing an existing file's content
// and date (its entry otherwise kept), and returns its node.
func addFile(dir *fat.Node, name string, data []byte, date time.Time) (*fat.Node, error) {
	open := func() (io.Reader, error) { return bytes.NewReader(data), nil }
	return putFile(dir, name, int64(len(data)), date, open)
}

func putFile(dir *fat.Node, name string, size int64, date time.Time, open func() (io.Reader, error)) (*fat.Node, error) {
	if n := dir.Child(name); n != nil {
		if n.IsDir() {
			return nil, fmt.Errorf("%s is a directory", name)
		}
		n.Size, n.Open = size, open
		n.SetModified(date)
		return n, nil
	}
	n, err := fat.NewFile(dir, name, size, date, open)
	if err != nil {
		return nil, err
	}
	dir.Children = append(dir.Children, n)
	return n, nil
}

// overlayFAT lays dir's files over root, creating directories as needed.
func overlayFAT(root *fat.Node, dir string, changed func(action, name, why string)) error {
	return filepath.WalkDir(dir, func(abs string, d fs.DirEntry, err error) error {
		if err != nil || abs == dir {
			return err
		}
		rel, _ := filepath.Rel(dir, abs)
		parts := strings.Split(filepath.ToSlash(rel), "/")
		info, err := d.Info()
		if err != nil {
			return err
		}
		parent := root
		for _, p := range parts[:len(parts)-1] {
			parent = parent.Child(p)
			if parent == nil || !parent.IsDir() {
				return fmt.Errorf("%s: %s is not a directory in the image", abs, p)
			}
		}
		name := parts[len(parts)-1]
		if d.IsDir() {
			if c := parent.Child(name); c != nil {
				if !c.IsDir() {
					return fmt.Errorf("%s: a file in the image", abs)
				}
				return nil
			}
			n, err := fat.NewDir(parent, name, info.ModTime())
			if err != nil {
				return fmt.Errorf("%s: %w", abs, err)
			}
			parent.Children = append(parent.Children, n)
			return nil
		}
		action := "added"
		if parent.Child(name) != nil {
			action = "replaced"
		}
		open := func() (io.Reader, error) { return os.Open(abs) }
		if _, err := putFile(parent, name, info.Size(), info.ModTime(), open); err != nil {
			return fmt.Errorf("%s: %w", abs, err)
		}
		changed(action, strings.ReplaceAll(filepath.ToSlash(rel), "/", `\`), "from "+dir)
		return nil
	})
}

func sizeString(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
}
