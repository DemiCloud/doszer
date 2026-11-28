package library

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaves(t *testing.T) {
	roms, saves, other := t.TempDir(), t.TempDir(), t.TempDir()
	game := filepath.Join(roms, "Space Hulk.dosz")
	for _, f := range []string{"Space Hulk.pure.zip", "Space Hulk-CDRIVE.sav", "Space Hulk-0A1B2C3D.sav", "Gone Game.pure.zip", "notes.txt", "Space Hulk-123.sav"} {
		os.WriteFile(filepath.Join(saves, f), []byte("x"), 0o644)
	}
	os.WriteFile(filepath.Join(other, "Space Hulk.pure.zip"), []byte("x"), 0o644) // another folder's
	got := Saves([]Game{{Path: game}}, func(string) string { return saves }, other)
	type row struct{ name, kind, game string }
	want := map[string]row{
		filepath.Join(saves, "Space Hulk.pure.zip"):     {"Space Hulk", SaveC, game},
		filepath.Join(saves, "Space Hulk-CDRIVE.sav"):   {"Space Hulk", SaveCImage, game},
		filepath.Join(saves, "Space Hulk-0A1B2C3D.sav"): {"Space Hulk", SaveD, game},
		filepath.Join(saves, "Gone Game.pure.zip"):      {"Gone Game", SaveC, ""},
		filepath.Join(other, "Space Hulk.pure.zip"):     {"Space Hulk", SaveC, ""},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d saves: %+v", len(got), got)
	}
	for _, s := range got {
		w, ok := want[s.Path]
		if !ok || s.Content != w.name || s.Kind != w.kind || s.Game != w.game {
			t.Errorf("%s: %+v, want %+v", s.Path, s, w)
		}
	}
}

func TestSources(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "Goddess", "DATA"), 0o755)
	os.WriteFile(filepath.Join(dir, "Goddess", "GODDESS.EXE"), make([]byte, 100), 0o644)
	os.WriteFile(filepath.Join(dir, "Goddess", "DATA", "A.DAT"), make([]byte, 50), 0o644)
	os.WriteFile(filepath.Join(dir, "Goddess", "DOSZ.TOML"), []byte("format = 1\nname = 'Vinyl Goddess'\n\n[boot]\nexec = 'C:\\GODDESS.EXE'\n"), 0o644)
	os.MkdirAll(filepath.Join(dir, ".Goddess.doszer-dump"), 0o755)
	got, err := Sources(dir)
	if err != nil || len(got) != 1 {
		t.Fatalf("%+v, %v", got, err)
	}
	if s := got[0]; s.Name != "Vinyl Goddess" || s.Files != 3 || s.Size < 150 {
		t.Errorf("%+v", s)
	}
	if got, err := Sources(filepath.Join(dir, "missing")); err != nil || got != nil {
		t.Errorf("missing: %+v, %v", got, err)
	}
}
