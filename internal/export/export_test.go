package export

import (
	"archive/zip"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for name, content := range files {
		fw, _ := w.Create(name)
		fw.Write([]byte(content))
	}
	w.Close()
	f.Close()
}

// TestPlan: a game goes into its system's folder with its parent beside
// it and its image into bios; a second export finds it all the same; an
// archive that changed is kept unless overwrite, an image always.
func TestPlan(t *testing.T) {
	src, sys, dest := t.TempDir(), t.TempDir(), t.TempDir()
	writeZip(t, filepath.Join(src, "Base.dosp"), map[string]string{"AUTOBOOT.DBP": "O*dosbox_pure/Windows 98\n", "X.DLL": "x"})
	writeZip(t, filepath.Join(src, "Game.dosz"), map[string]string{"Base.dosp.parent": "", "GAME.EXE": "MZ"})
	writeZip(t, filepath.Join(src, "Old.dosz"), map[string]string{"AUTOBOOT.DBP": "S*Win31\n"})
	writeZip(t, filepath.Join(src, "Plain.dosz"), map[string]string{"AUTOBOOT.DBP": "C:\\GAME.EXE\n"})
	img := filepath.Join(sys, "dosbox_pure", "Windows 98.vhd")
	os.MkdirAll(filepath.Dir(img), 0o755)
	os.WriteFile(img, make([]byte, 7<<20), 0o644)
	tg := Target{ROMs: filepath.Join(dest, "roms"), BIOS: filepath.Join(dest, "bios")}
	archives := []string{filepath.Join(src, "Game.dosz"), filepath.Join(src, "Old.dosz"), filepath.Join(src, "Plain.dosz")}

	actions := func(steps []Step) []string {
		var out []string
		for _, s := range steps {
			rel, _ := filepath.Rel(dest, s.To)
			out = append(out, s.Action+" "+filepath.ToSlash(rel))
		}
		return out
	}
	steps, err := Plan(archives, sys, tg, false)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"copy roms/windows9x/Game.dosz",
		"copy roms/windows9x/Base.dosp",
		"copy bios/dosbox_pure/Windows 98.vhd",
		"copy roms/windows3x/Old.dosz",
		"missing bios/Win31",
		"copy roms/dos/Plain.dosz",
	}
	if got := actions(steps); !reflect.DeepEqual(got, want) {
		t.Fatalf("first export:\n got %q\nwant %q", got, want)
	}
	if err := Apply(steps, nil); err != nil {
		t.Fatal(err)
	}
	steps, _ = Plan(archives[:1], sys, tg, false)
	if got, want := actions(steps), []string{"same roms/windows9x/Game.dosz", "same roms/windows9x/Base.dosp", "same bios/dosbox_pure/Windows 98.vhd"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("second export:\n got %q\nwant %q", got, want)
	}

	// A shell not here but there already is no problem.
	os.MkdirAll(filepath.Join(tg.BIOS), 0o755)
	writeZip(t, filepath.Join(tg.BIOS, "Win31.dosz"), map[string]string{"WINDOWS/WIN.COM": "x"})
	steps, _ = Plan(archives[1:2], sys, tg, false)
	if got, want := actions(steps), []string{"same roms/windows3x/Old.dosz", "same bios/Win31.dosz"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("shell there already:\n got %q\nwant %q", got, want)
	}

	writeZip(t, filepath.Join(src, "Game.dosz"), map[string]string{"Base.dosp.parent": "", "GAME.EXE": "MZ2"})
	os.WriteFile(img, make([]byte, 8<<20), 0o644)
	for _, tt := range []struct {
		overwrite bool
		want      []string
	}{
		{false, []string{"conflict roms/windows9x/Game.dosz", "same roms/windows9x/Base.dosp", "conflict bios/dosbox_pure/Windows 98.vhd"}},
		{true, []string{"replace roms/windows9x/Game.dosz", "same roms/windows9x/Base.dosp", "conflict bios/dosbox_pure/Windows 98.vhd"}},
	} {
		steps, _ = Plan(archives[:1], sys, tg, tt.overwrite)
		if got := actions(steps); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("changed, overwrite %v:\n got %q\nwant %q", tt.overwrite, got, tt.want)
		}
	}
}
