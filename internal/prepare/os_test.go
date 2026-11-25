package prepare

import (
	"bytes"
	"encoding/binary"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DemiCloud/doszer/internal/builtin"
	"github.com/DemiCloud/doszer/internal/fat"
)

// fileDisk is a fat.Disk over a byte slice.
type fileDisk []byte

func (d fileDisk) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(d)) {
		return 0, io.EOF
	}
	return copy(p, d[off:]), nil
}

func (d fileDisk) WriteAt(p []byte, off int64) (int, error) { return copy(d[off:], p), nil }

// win9xImage builds an unpartitioned FAT16 image holding files (a path
// ending in "/" is a directory), written with fat.Write.
func win9xImage(t *testing.T, files map[string]string) []byte {
	t.Helper()
	const total, fatSectors = 20000, 20
	img := make(fileDisk, total*512)
	bs := img[0:512]
	le := binary.LittleEndian
	bs[0], bs[1], bs[2] = 0xEB, 0x3C, 0x90
	copy(bs[3:], "MSWIN4.1")
	le.PutUint16(bs[11:], 512)
	bs[13] = 4 // sectors per cluster
	le.PutUint16(bs[14:], 1)
	bs[16] = 2
	le.PutUint16(bs[17:], 512)
	le.PutUint16(bs[19:], total)
	bs[21] = 0xF8
	le.PutUint16(bs[22:], fatSectors)
	bs[510], bs[511] = 0x55, 0xAA

	vol, err := fat.Open(bytes.NewReader(img))
	if err != nil {
		t.Fatal(err)
	}
	root, _ := vol.Tree()
	when := time.Date(1998, 6, 25, 12, 0, 0, 0, time.UTC)
	dir := func(p string) *fat.Node {
		n := root
		for _, part := range strings.Split(strings.Trim(p, "/"), "/") {
			if part == "" || part == "." {
				continue
			}
			c := n.Child(part)
			if c == nil {
				c, err = fat.NewDir(n, part, when)
				if err != nil {
					t.Fatal(err)
				}
				n.Children = append(n.Children, c)
			}
			n = c
		}
		return n
	}
	for p, data := range files {
		if strings.HasSuffix(p, "/") {
			dir(p)
			continue
		}
		parent := dir(filepath.ToSlash(filepath.Dir(p)))
		b := []byte(data)
		n, err := fat.NewFile(parent, filepath.Base(p), int64(len(b)), when, func() (io.Reader, error) { return bytes.NewReader(b), nil })
		if err != nil {
			t.Fatal(err)
		}
		parent.Children = append(parent.Children, n)
	}
	if err := fat.Write(img, vol.Layout(), root); err != nil {
		t.Fatal(err)
	}
	return img
}

func win9xFiles() map[string]string {
	return map[string]string{
		"IO.SYS":                      "io",
		"MSDOS.SYS":                   "[Paths]\r\nWinDir=C:\\WINDOWS\r\n",
		"AUTOEXEC.BAT":                "",
		"BOOTLOG.PRV":                 "old log",
		"WINDOWS/WIN.COM":             "win",
		"WINDOWS/WSCRIPT.EXE":         "wsh",
		"WINDOWS/SYSTEM.INI":          "[boot]\r\nshell=Explorer.exe\r\n",
		"WINDOWS/WIN.INI":             "[windows]\r\n",
		"WINDOWS/WIN386.SWP":          "swap",
		"WINDOWS/TEMP/msnmgsr1.log":   "temp",
		"WINDOWS/Recent/Doc.lnk":      "lnk",
		"RECYCLED/DC0.TXT":            "deleted",
		"RECYCLED/desktop.ini":        "[.ShellClassInfo]\r\n",
		"Program Files/Long Name.txt": "kept",
		"GAMES/~DF1234.TMP":           "tmp",
	}
}

func readFAT(t *testing.T, p string) (fs.FS, *fat.Node) {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	f, err := fat.Open(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	root, err := f.Tree()
	if err != nil {
		t.Fatal(err)
	}
	return f, root
}

func TestOS(t *testing.T) {
	if _, _, err := builtin.Presskey(); err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	in := filepath.Join(dir, "win98.img")
	if err := os.WriteFile(in, win9xImage(t, win9xFiles()), 0o644); err != nil {
		t.Fatal(err)
	}
	add := filepath.Join(dir, "add")
	os.MkdirAll(filepath.Join(add, "My Tools"), 0o755)
	os.WriteFile(filepath.Join(add, "My Tools", "Helper Program.exe"), []byte("helper"), 0o644)

	out := filepath.Join(dir, "win98-games.img")
	rep, err := OS(OSOptions{Input: in, Output: out, Add: []string{add}})
	if err != nil {
		t.Fatal(err)
	}
	f, root := readFAT(t, out)
	read := func(p string) string {
		b, err := fs.ReadFile(f, p)
		if err != nil {
			t.Errorf("%s: %v", p, err)
		}
		return string(b)
	}
	exists := func(p string) bool { _, err := fs.Stat(f, p); return err == nil }

	for _, gone := range []string{"BOOTLOG.PRV", "WINDOWS/WIN386.SWP", "WINDOWS/TEMP/msnmgsr1.log", "WINDOWS/Recent/Doc.lnk", "RECYCLED/DC0.TXT", "GAMES/~DF1234.TMP"} {
		if exists(gone) {
			t.Errorf("%s not removed", gone)
		}
	}
	for _, kept := range []string{"WINDOWS/TEMP", "WINDOWS/Recent", "RECYCLED/desktop.ini"} {
		if !exists(kept) {
			t.Errorf("%s removed", kept)
		}
	}
	if s := read("WINDOWS/SYSTEM.INI"); s != "[boot]\r\nshell=wscript.exe C:\\WINDOWS\\STARTRUN.VBS\r\n" {
		t.Errorf("SYSTEM.INI = %q", s)
	}
	if s := read("WINDOWS/STARTRUN.VBS"); !strings.Contains(s, "D:\\WINDOWS.BAT\", 0, True\r\n") {
		t.Errorf("STARTRUN.VBS = %q", s)
	}
	if s := read("WINDOWS/PROGMAN.INI"); s != "[Groups]\r\nGroup1=C:\\WINDOWS\\TOOLS.GRP\r\n\r\n[Settings]\r\nOrder=1\r\n" && s != "[Groups]\nGroup1=C:\\WINDOWS\\TOOLS.GRP\n\n[Settings]\nOrder=1\n" {
		t.Errorf("PROGMAN.INI = %q", s)
	}
	if !strings.HasPrefix(read("WINDOWS/TOOLS.GRP"), "PMCC") {
		t.Error("TOOLS.GRP missing")
	}
	pk, _, _ := builtin.Presskey()
	if read("WINDOWS/PRESSKEY.EXE") != string(pk) {
		t.Error("PRESSKEY.EXE missing")
	}
	if read("Program Files/Long Name.txt") != "kept" || read("My Tools/Helper Program.exe") != "helper" {
		t.Error("kept or --add file wrong")
	}
	if s, err := fs.Stat(f, "MYTOOL~1/HELPER~1.EXE"); err != nil || s.Name() != "Helper Program.exe" {
		t.Errorf("--add file's short name: %v, %v", s, err)
	}
	if !root.Children[0].Is("IO.SYS") || !root.Children[1].Is("MSDOS.SYS") {
		t.Errorf("root starts %s, %s; want IO.SYS, MSDOS.SYS", root.Children[0].Name(), root.Children[1].Name())
	}
	if len(rep.Changes) == 0 || rep.Entries == 0 {
		t.Errorf("report %+v", rep)
	}

	// Deterministic.
	out2 := filepath.Join(dir, "again.img")
	if _, err := OS(OSOptions{Input: in, Output: out2, Add: []string{add}}); err != nil {
		t.Fatal(err)
	}
	a, _ := os.ReadFile(out)
	b, _ := os.ReadFile(out2)
	if !bytes.Equal(a, b) {
		t.Error("two runs differ")
	}
}

func TestOSRefuses(t *testing.T) {
	if _, _, err := builtin.Presskey(); err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	tests := []struct {
		name  string
		edit  func(m map[string]string)
		input bool // write over the input
		want  string
	}{
		{"not windows", func(m map[string]string) { delete(m, "IO.SYS") }, false, "not a Windows 9x image"},
		{"no wsh", func(m map[string]string) { delete(m, "WINDOWS/WSCRIPT.EXE") }, false, "Windows Script Host"},
		{"pending install", func(m map[string]string) { m["WINDOWS/WININIT.INI"] = "[rename]\r\n" }, false, "WININIT.INI"},
		{"output is input", func(map[string]string) {}, true, "the output is the input"},
	}
	for _, tt := range tests {
		files := win9xFiles()
		tt.edit(files)
		in := filepath.Join(dir, tt.name+".img")
		os.WriteFile(in, win9xImage(t, files), 0o644)
		out := filepath.Join(dir, "out.img")
		if tt.input {
			out = in
		}
		_, err := OS(OSOptions{Input: in, Output: out, Force: true})
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: err = %v, want %q", tt.name, err, tt.want)
		}
	}
}

func TestGRP(t *testing.T) {
	b := buildGRP("Tools", toolsGroup)
	le := binary.LittleEndian
	if string(b[:4]) != "PMCC" || len(b)%2 != 0 {
		t.Fatalf("header %q, length %d", b[:4], len(b))
	}
	var sum uint16
	for i := 0; i < len(b); i += 2 {
		sum += le.Uint16(b[i:])
	}
	if sum != 0 {
		t.Errorf("words sum to %#x, want 0", sum)
	}
	cstr := func(off uint16) string { return string(b[off : int(off)+bytes.IndexByte(b[off:], 0)]) }
	if cstr(le.Uint16(b[22:])) != "Tools" {
		t.Error("group name")
	}
	if n := int(le.Uint16(b[32:])); n != len(toolsGroup) {
		t.Fatalf("%d items", n)
	}
	for i, it := range toolsGroup {
		item := b[le.Uint16(b[34+2*i:]):]
		if cstr(le.Uint16(item[18:])) != it.name || cstr(le.Uint16(item[20:])) != it.command || cstr(le.Uint16(item[22:])) != it.iconPath {
			t.Errorf("item %d strings", i)
		}
		if le.Uint16(item[6:]) != 12 || le.Uint16(item[8:]) != grpPlane || le.Uint16(item[10:]) != grpPlane {
			t.Errorf("item %d icon sizes", i)
		}
	}
	cb := int(le.Uint16(b[6:]))
	if string(b[cb:cb+6]) != "\x00\x80\xff\xff\x0a\x00" || string(b[cb+6:cb+10]) != "PMCC" || string(b[cb+10:]) != "\xff\xff\xff\xff\x00\x00" {
		t.Errorf("tags % x", b[cb:])
	}
}

func TestAddGroup(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", "[Groups]\nGroup1=C:\\WINDOWS\\TOOLS.GRP\n\n[Settings]\nOrder=1\n"},
		{"[Settings]\nOrder= 1 2\n[Groups]\nGroup1=A.GRP\nGroup2=B.GRP\n", "[Settings]\nOrder=1 2 3\n[Groups]\nGroup1=A.GRP\nGroup2=B.GRP\nGroup3=C:\\WINDOWS\\TOOLS.GRP\n"},
		{"[Groups]\nGroup2=B.GRP\n", "[Groups]\nGroup2=B.GRP\nGroup1=C:\\WINDOWS\\TOOLS.GRP\n\n[Settings]\nOrder=1\n"},
	}
	for _, tt := range tests {
		f := parseINI([]byte(tt.in))
		addGroup(f, `C:\WINDOWS\TOOLS.GRP`)
		if got := string(f.bytes()); got != tt.want {
			t.Errorf("%q:\n got %q\nwant %q", tt.in, got, tt.want)
		}
		if addGroup(f, `c:\windows\tools.grp`) {
			t.Errorf("%q: added twice", tt.in)
		}
	}
}
