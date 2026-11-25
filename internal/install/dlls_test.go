package install

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestCopyImportedDLLs(t *testing.T) {
	old := importsOf
	importsOf = func(_ fs.FS, p string) []string {
		return map[string][]string{
			"Games/R/Recoil.exe":         {"KERNEL32.dll", "MSVCP50.dll"},
			"WINDOWS/SYSTEM/MSVCP50.DLL": {"msvcrtd.dll", "KERNEL32.dll"},
		}[p]
	}
	t.Cleanup(func() { importsOf = old })

	progs := fstest.MapFS{"Games/R/Recoil.exe": {Data: []byte("MZ")}}
	cfs := fstest.MapFS{
		"WINDOWS/SYSTEM/MSVCP50.DLL":  {Data: []byte("msvcp50"), ModTime: t0},
		"WINDOWS/SYSTEM/msvcrtd.dll":  {Data: []byte("msvcrtd")},
		"WINDOWS/SYSTEM/UNUSED.DLL":   {Data: []byte("unused")},
		"WINDOWS/SYSTEM/KERNEL32.DLL": {Data: []byte("kernel")},
		"Games/R/Recoil.exe":          {Data: []byte("MZ")},
	}
	changes := []Change{
		{Path: "WINDOWS/SYSTEM/MSVCP50.DLL", Kind: Added, Modified: t0},
		{Path: "WINDOWS/SYSTEM/msvcrtd.dll", Kind: Added},
		{Path: "WINDOWS/SYSTEM/UNUSED.DLL", Kind: Added}, // nothing imports it
		{Path: "Games/R/Recoil.exe", Kind: Added},
	}
	newRep := func() *Report {
		return &Report{Dir: "Games", Extracted: &Extracted{Written: []string{"Games/R/Recoil.exe"}}}
	}

	src := t.TempDir()
	rep := newRep()
	if err := copyImportedDLLs(rep, progs, cfs, changes, Options{Source: src}); err != nil {
		t.Fatal(err)
	}
	if len(rep.DLLs) != 2 || rep.DLLs[0].To != "Games/R/MSVCP50.DLL" || rep.DLLs[1].To != "Games/R/msvcrtd.dll" {
		t.Fatalf("copies %+v", rep.DLLs)
	}
	if b, _ := os.ReadFile(filepath.Join(src, "Games/R/MSVCP50.DLL")); string(b) != "msvcp50" {
		t.Fatalf("MSVCP50.DLL = %q", b)
	}
	if fi, _ := os.Stat(filepath.Join(src, "Games/R/MSVCP50.DLL")); !fi.ModTime().Equal(t0) {
		t.Error("modification time not kept")
	}
	if _, err := os.Stat(filepath.Join(src, "Games/R/KERNEL32.DLL")); err == nil {
		t.Error("an unchanged Windows DLL was copied")
	}

	// Already there and identical: fine. Differing: needs --force.
	if err := copyImportedDLLs(newRep(), progs, cfs, changes, Options{Source: src}); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(src, "Games/R/msvcrtd.dll"), []byte("other"), 0o644)
	if err := copyImportedDLLs(newRep(), progs, cfs, changes, Options{Source: src}); err == nil {
		t.Fatal("differing copy replaced without --force")
	}

	// A DLL the install itself put in the folder isn't copied again.
	rep = newRep()
	rep.Extracted.Written = append(rep.Extracted.Written, "Games/R/MSVCP50.DLL")
	dry := t.TempDir()
	if err := copyImportedDLLs(rep, progs, cfs, changes, Options{Source: dry, DryRun: true}); err != nil {
		t.Fatal(err)
	}
	if len(rep.DLLs) != 0 {
		t.Fatalf("copied a DLL already in the folder: %+v", rep.DLLs)
	}
	if entries, _ := os.ReadDir(dry); len(entries) != 0 {
		t.Fatal("dry run wrote files")
	}
}

// importsOf reads a real Windows program's import table: the embedded
// redumper.exe, when this checkout has staged it.
func TestImportsOf(t *testing.T) {
	exe := "../builtin/data/windows_amd64/redumper.exe"
	if _, err := os.Stat(exe); err != nil {
		t.Skip("no staged redumper.exe (make redumper-all)")
	}
	libs := importsOf(os.DirFS(filepath.Dir(exe)), filepath.Base(exe))
	found := false
	for _, l := range libs {
		found = found || l == "KERNEL32.dll"
	}
	if !found {
		t.Fatalf("imports %v", libs)
	}
	if libs := importsOf(fstest.MapFS{"x.exe": {Data: []byte("not a program")}}, "x.exe"); libs != nil {
		t.Fatalf("non-program imports %v", libs)
	}
}
