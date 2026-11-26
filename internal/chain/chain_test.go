package chain

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func archive(t *testing.T, p string, files map[string]string) {
	t.Helper()
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for name, data := range files {
		fw, _ := w.Create(name)
		fw.Write([]byte(data))
	}
	w.Close()
	f.Close()
}

func TestResolve(t *testing.T) {
	dir := t.TempDir()
	sys := t.TempDir()
	os.MkdirAll(filepath.Join(sys, "dosbox_pure"), 0o755)
	os.WriteFile(filepath.Join(sys, "dosbox_pure", "Win31.dosz"), []byte("x"), 0o644)

	// As the windows3x set: games over a shared shell archive, which
	// boots the system directory's Windows 3.1 shell.
	archive(t, filepath.Join(dir, "BOWEP.shell"), map[string]string{"AUTOBOOT.DBP": "S*dosbox_pure/Win31", "BOWEP/SOL.EXE": "MZ"})
	archive(t, filepath.Join(dir, "SkiFree.dosz"), map[string]string{"BOWEP.shell.parent": "", "WINDOWS.BAT": "SKI"})
	archive(t, filepath.Join(dir, "Own.dosz"), map[string]string{"BOWEP.shell.parent": "", "AUTOBOOT.DBP": "C:\\GAME.EXE\r\n"})
	archive(t, filepath.Join(dir, "Orphan.dosz"), map[string]string{"Gone.shell.parent": ""})
	archive(t, filepath.Join(dir, "Two.dosz"), map[string]string{"A.parent": "", "B.parent": ""})
	archive(t, filepath.Join(dir, "LoopA.dosz"), map[string]string{"LoopB.dosz.parent": ""})
	archive(t, filepath.Join(dir, "LoopB.dosz"), map[string]string{"LoopA.dosz.parent": ""})
	archive(t, filepath.Join(dir, "Win98.dosz"), map[string]string{"AUTOBOOT.DBP": "O*dosbox_pure/Win98"})

	tests := []struct {
		name     string
		links    int
		bootFrom int
		kind     string
		boot     string
		found    bool // the system file
		broken   bool
	}{
		{"SkiFree.dosz", 2, 1, KindShell, "dosbox_pure/Win31", true, false},
		{"Own.dosz", 2, 0, KindDOS, `C:\GAME.EXE`, false, false}, // its own AUTOBOOT.DBP wins
		{"Orphan.dosz", 2, -1, KindDOS, "", false, true},
		{"Two.dosz", 1, -1, KindDOS, "", false, true},
		{"LoopA.dosz", 3, -1, KindDOS, "", false, true},
		{"Win98.dosz", 1, 0, KindOS, "dosbox_pure/Win98", false, true}, // not in the system directory
	}
	for _, tt := range tests {
		c := Resolve(filepath.Join(dir, tt.name))
		c.Locate(sys)
		if len(c.Links) != tt.links || c.BootFrom != tt.bootFrom || c.Kind != tt.kind || c.Boot != tt.boot || (c.System != "") != tt.found || (c.Broken() != "") != tt.broken {
			t.Errorf("%s: %d links, from %d, %s %q, system %q, broken %q", tt.name, len(c.Links), c.BootFrom, c.Kind, c.Boot, c.System, c.Broken())
		}
	}

	used := UsedBy(dir, filepath.Join(dir, "BOWEP.shell"))
	if len(used) != 2 {
		t.Errorf("UsedBy(BOWEP.shell) = %v, want SkiFree and Own", used)
	}
}
