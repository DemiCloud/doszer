package build

import (
	"testing"

	"github.com/DemiCloud/doszer/internal/manifest"
)

func intPtr(i int) *int { return &i }

func TestBuildAutoboot(t *testing.T) {
	tests := []struct {
		name string
		boot manifest.Boot
		want string
	}{
		{"exec only", manifest.Boot{Exec: `C:\GAME.EXE`}, `C:\GAME.EXE`},
		{"shell", manifest.Boot{Shell: "dosbox_pure/Win31"}, "S*dosbox_pure/Win31"},
		{"os", manifest.Boot{OS: "dosbox_pure/Win98"}, "O*dosbox_pure/Win98"},
		{
			"exec, skip and disc",
			manifest.Boot{Exec: `C:\GAME.EXE`, Skip: intPtr(120), Disc: "Disc 1"},
			"C:\\GAME.EXE\r\n120\r\nDisc 1",
		},
		{
			"disc alone, no startup mode",
			manifest.Boot{Disc: "Disc 2"},
			"\r\n\r\nDisc 2",
		},
		{
			"exec and disc, no skip: middle empty line kept",
			manifest.Boot{Exec: `C:\GAME.EXE`, Disc: "Disc 1"},
			"C:\\GAME.EXE\r\n\r\nDisc 1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(BuildAutoboot(&tt.boot)); got != tt.want {
				t.Errorf("BuildAutoboot(%+v) = %q, want %q", tt.boot, got, tt.want)
			}
		})
	}
}

func TestNeedsAutoboot(t *testing.T) {
	if NeedsAutoboot(&manifest.Boot{}) {
		t.Error("empty boot should not need AUTOBOOT.DBP")
	}
	if !NeedsAutoboot(&manifest.Boot{Disc: "Disc 1"}) {
		t.Error("disc alone should need AUTOBOOT.DBP")
	}
}

func TestEffectiveManifestSetsName(t *testing.T) {
	m, err := manifest.Parse([]byte("format = 1\n"), "Original Name")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	data, err := EffectiveManifest(m, "Override Name")
	if err != nil {
		t.Fatalf("effectiveManifest: %v", err)
	}

	reparsed, err := manifest.Parse(data, "fallback")
	if err != nil {
		t.Fatalf("effective manifest does not re-parse: %v\n%s", err, data)
	}
	if reparsed.Name != "Override Name" {
		t.Errorf("Name = %q, want %q", reparsed.Name, "Override Name")
	}
}

func TestAddRuntimeFiles(t *testing.T) {
	fs := newFileSet()
	fs.set(manifest.FileName, dataEntry([]byte("stale")))

	m := &manifest.Manifest{
		Format: manifest.FormatVersion,
		Name:   "Example",
		Boot:   manifest.Boot{Exec: `C:\GAME.EXE`, Parent: "Shared.shell"},
	}

	if err := addRuntimeFiles(fs, m, "Example"); err != nil {
		t.Fatalf("addRuntimeFiles: %v", err)
	}

	if _, ok := fs.get("AUTOBOOT.DBP"); !ok {
		t.Error("missing AUTOBOOT.DBP")
	}
	parent, ok := fs.get("Shared.shell.parent")
	if !ok {
		t.Fatal("missing Shared.shell.parent")
	}
	if data, _ := parent.bytes(); len(data) != 0 {
		t.Errorf("Shared.shell.parent should be zero bytes, got %d", len(data))
	}

	manifestEntry, ok := fs.get(manifest.FileName)
	if !ok {
		t.Fatal("missing DOSZ.TOML")
	}
	if data, _ := manifestEntry.bytes(); string(data) == "stale" {
		t.Error("DOSZ.TOML was not replaced by the generated runtime file")
	}
}
