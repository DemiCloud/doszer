package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/DemiCloud/doszer/internal/w9xreg"
)

func sz(name, s string) w9xreg.Value {
	return w9xreg.Value{Name: name, Type: w9xreg.TypeSZ, Data: []byte(s)}
}

// regFS is a C: holding SYSTEM.DAT and USER.DAT built from the given keys.
func regFS(system, user *w9xreg.Key) fstest.MapFS {
	return fstest.MapFS{
		"WINDOWS/SYSTEM.DAT": {Data: w9xreg.Encode(system)},
		"WINDOWS/USER.DAT":   {Data: w9xreg.Encode(user)},
	}
}

func TestExportRegistry(t *testing.T) {
	sysBefore := &w9xreg.Key{Subkeys: []*w9xreg.Key{
		{Name: "Software", Subkeys: []*w9xreg.Key{{Name: "Microsoft", Values: []w9xreg.Value{sz("A", "1")}}}},
		{Name: "Enum"},
	}}
	sysAfter := &w9xreg.Key{Subkeys: []*w9xreg.Key{
		{Name: "Software", Subkeys: []*w9xreg.Key{
			{Name: "Example Co", Subkeys: []*w9xreg.Key{{Name: "Game", Values: []w9xreg.Value{sz("Path", `D:\GAME`), sz("CD", "E:")}}}},
			{Name: "Microsoft", Values: []w9xreg.Value{sz("A", "2")}}, // Windows' own
			{Name: "Classes", Subkeys: []*w9xreg.Key{{Name: ".sav", Values: []w9xreg.Value{sz("", "x")}}}},
			{Name: "Description", Subkeys: []*w9xreg.Key{{Name: "Microsoft", Values: []w9xreg.Value{sz("Rpc", "t")}}}},
		}},
		{Name: "Enum", Values: []w9xreg.Value{sz("New", "device")}}, // not a program's
	}}
	userBefore := &w9xreg.Key{Subkeys: []*w9xreg.Key{{Name: ".DEFAULT", Subkeys: []*w9xreg.Key{{Name: "Software"}}}}}
	userAfter := &w9xreg.Key{Subkeys: []*w9xreg.Key{{Name: ".DEFAULT", Subkeys: []*w9xreg.Key{
		{Name: "Software", Subkeys: []*w9xreg.Key{{Name: "Example Co", Values: []w9xreg.Value{sz("Volume", "8")}}}},
		{Name: "Control Panel", Values: []w9xreg.Value{sz("x", "y")}},
	}}}}
	before, after := regFS(sysBefore, userBefore), regFS(sysAfter, userAfter)

	want := "REGEDIT4\r\n" +
		"\r\n[HKEY_LOCAL_MACHINE\\Software\\Example Co]\r\n" +
		"\r\n[HKEY_LOCAL_MACHINE\\Software\\Example Co\\Game]\r\n" +
		"\"Path\"=\"D:\\\\GAME\"\r\n" +
		"\"CD\"=\"E:\"\r\n" +
		"\r\n[HKEY_CURRENT_USER\\Software\\Example Co]\r\n" +
		"\"Volume\"=\"8\"\r\n"

	tests := []struct {
		name     string
		existing string // INSTALL.REG already in the source
		force    bool
		dryRun   bool
		wantErr  bool
		wantFile string
	}{
		{name: "writes the program's entries", wantFile: want},
		{name: "dry run writes nothing", dryRun: true},
		{name: "identical file is fine", existing: want, wantFile: want},
		{name: "differing file needs force", existing: "x", wantErr: true, wantFile: "x"},
		{name: "force replaces it", existing: "x", force: true, wantFile: want},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := t.TempDir()
			os.MkdirAll(filepath.Join(src, "GAME"), 0o755)
			target := filepath.Join(src, "GAME", RegistryFile)
			if tt.existing != "" {
				os.WriteFile(target, []byte(tt.existing), 0o644)
			}
			rep := &Report{Drive: "D:", Dir: "GAME", Extracted: &Extracted{}}
			err := exportRegistry(rep, before, after, Options{Source: src, Force: tt.force, DryRun: tt.dryRun})
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v", err)
			}
			data, _ := os.ReadFile(target)
			if string(data) != tt.wantFile {
				t.Errorf("%s:\n%q\nwant\n%q", RegistryFile, data, tt.wantFile)
			}
			if !tt.wantErr && (rep.Registry != `D:\GAME\INSTALL.REG` || rep.RegistryValues != 3) {
				t.Errorf("report %q, %d values", rep.Registry, rep.RegistryValues)
			}
		})
	}

	t.Run("nothing added, nothing written", func(t *testing.T) {
		src := t.TempDir()
		rep := &Report{Drive: "D:", Dir: "GAME", Extracted: &Extracted{}}
		if err := exportRegistry(rep, before, before, Options{Source: src}); err != nil {
			t.Fatal(err)
		}
		if rep.Registry != "" {
			t.Errorf("registry %q", rep.Registry)
		}
	})

	t.Run("no registry files: not Windows 9x", func(t *testing.T) {
		groups, _, err := registryAdded(fstest.MapFS{}, fstest.MapFS{"AUTOEXEC.BAT": {}})
		if err != nil || len(groups) > 0 {
			t.Errorf("groups %v, err %v", groups, err)
		}
	})

	t.Run("corrupt registry is an error", func(t *testing.T) {
		bad := fstest.MapFS{"WINDOWS/SYSTEM.DAT": {Data: []byte(strings.Repeat("x", 100))}}
		if _, _, err := registryAdded(before, bad); err == nil {
			t.Error("accepted")
		}
	})
}
