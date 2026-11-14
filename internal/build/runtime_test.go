package build

import (
	"testing"
	"time"

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

	data, err := effectiveManifest(m, "Override Name")
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
	fs.set(manifest.FileName, dataEntry([]byte("stale"), time.Date(1994, 1, 1, 0, 0, 0, 0, time.UTC)))

	m := &manifest.Manifest{
		Format: manifest.FormatVersion,
		Name:   "Example",
		Boot:   manifest.Boot{Exec: `C:\GAME.EXE`, Parent: "Shared.shell"},
	}

	if err := addRuntimeFiles(fs, m, "Example"); err != nil {
		t.Fatalf("addRuntimeFiles: %v", err)
	}

	autoboot, ok := fs.get("AUTOBOOT.DBP")
	if !ok {
		t.Fatal("missing AUTOBOOT.DBP")
	}
	if !autoboot.modified.Equal(Epoch) {
		t.Errorf("AUTOBOOT.DBP modified = %v, want the fixed epoch %v (§8.2)", autoboot.modified, Epoch)
	}

	parent, ok := fs.get("Shared.shell.parent")
	if !ok {
		t.Fatal("missing Shared.shell.parent")
	}
	if data, _ := parent.bytes(); len(data) != 0 {
		t.Errorf("Shared.shell.parent should be zero bytes, got %d", len(data))
	}
	if !parent.modified.Equal(Epoch) {
		t.Errorf("Shared.shell.parent modified = %v, want the fixed epoch %v (§8.2)", parent.modified, Epoch)
	}

	manifestEntry, ok := fs.get(manifest.FileName)
	if !ok {
		t.Fatal("missing DOSZ.TOML")
	}
	if !manifestEntry.modified.Equal(Epoch) {
		t.Errorf("DOSZ.TOML modified = %v, want the fixed epoch %v (§8.2), not the stale source time", manifestEntry.modified, Epoch)
	}
	if data, _ := manifestEntry.bytes(); string(data) == "stale" {
		t.Error("DOSZ.TOML was not replaced by the generated runtime file")
	}
}

func TestAddRuntimeFilesInputPad(t *testing.T) {
	src := `format = 1
[[input.pad]]
b = "space Fire"
l3 = "menu"
`
	m, err := manifest.Parse([]byte(src), "Example")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		existing string // the build's DOS.YML, "" for none
		wantYML  string
	}{
		{"generated", "", "input_pad_b: space Fire\r\n"},
		{
			"merged into the build's own DOS.YML",
			"cpu_cycles: 20000\ninput_pad_a: enter Old\n",
			"cpu_cycles: 20000\r\ninput_pad_b: space Fire\r\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := newFileSet()
			if tt.existing != "" {
				fs.set("dos.yml", dataEntry([]byte(tt.existing), time.Date(1994, 1, 1, 0, 0, 0, 0, time.UTC)))
			}
			if err := addRuntimeFiles(fs, m, "Example"); err != nil {
				t.Fatal(err)
			}

			pad, ok := fs.get("PADMAP.DBP")
			if !ok {
				t.Fatal("missing PADMAP.DBP")
			}
			// b (ID 0) = space (53), l3 (ID 14) = menu (225).
			if data, _ := pad.bytes(); string(data) != string([]byte{0, 2, 0, 53, 14, 225}) {
				t.Errorf("PADMAP.DBP = %v", data)
			}

			yml, ok := fs.get("DOS.YML")
			if !ok {
				t.Fatal("missing DOS.YML")
			}
			if data, _ := yml.bytes(); string(data) != tt.wantYML {
				t.Errorf("DOS.YML = %q, want %q", data, tt.wantYML)
			}
			if !yml.modified.Equal(Epoch) || !pad.modified.Equal(Epoch) {
				t.Error("generated files must use the fixed epoch (§8.2)")
			}
		})
	}
}

func TestAddRuntimeFilesNoDOSYMLWithoutExpressibleBinding(t *testing.T) {
	m, err := manifest.Parse([]byte("format = 1\n[[input.pad]]\nl3 = \"menu\"\n"), "Example")
	if err != nil {
		t.Fatal(err)
	}
	fs := newFileSet()
	if err := addRuntimeFiles(fs, m, "Example"); err != nil {
		t.Fatal(err)
	}
	if _, ok := fs.get("DOS.YML"); ok {
		t.Error("DOS.YML generated for a mapping it can't express")
	}
	if _, ok := fs.get("PADMAP.DBP"); !ok {
		t.Error("missing PADMAP.DBP")
	}
}
