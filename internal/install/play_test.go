package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlay(t *testing.T) {
	systemDir, saveDir := fakeDOSBoxPure(t)
	save := filepath.Join(t.TempDir(), "fixture.pure.zip")
	os.WriteFile(save, zipBytes(t, map[string]string{"GAME/SAVE.DAT": "x"}), 0o644)
	t.Setenv("FAKE_SAVE", save)
	t.Setenv("FAKE_SUFFIX", ".pure.zip")
	writeOSImage(t, saveDir, "Win98.img", make([]byte, 64<<10))

	tests := []struct {
		name    string
		files   map[string]string
		system  string
		wantErr string
	}{
		{"plain", map[string]string{"GAME/GAME.EXE": "MZ", "AUTOBOOT.DBP": "C:\\GAME\\GAME.EXE\n"}, "", ""},
		{"OS image", map[string]string{"GAME/GAME.EXE": "MZ", "AUTOBOOT.DBP": "O*dosbox_pure/Win98"}, systemDir, ""},
		{"OS image missing", map[string]string{"AUTOBOOT.DBP": "O*dosbox_pure/Win95"}, systemDir, "not found"},
		{"no system directory", map[string]string{"AUTOBOOT.DBP": "O*dosbox_pure/Win98\r\n0\r\n"}, "", "system directory"},
	}
	t.Setenv("HOME", t.TempDir()) // no RetroDECK
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			archive := filepath.Join(t.TempDir(), "Example Game.dosz")
			os.WriteFile(archive, zipBytes(t, tt.files), 0o644)
			os.Remove(filepath.Join(saveDir, "Example Game.pure.zip"))
			_, err := Play(archive, tt.system, saveDir)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			// The save is named after the archive, and kept.
			if _, err := os.Stat(filepath.Join(saveDir, "Example Game.pure.zip")); err != nil {
				t.Fatal(err)
			}
		})
	}

	if _, err := Play(save+".missing", "", saveDir); err == nil {
		t.Fatal("missing archive accepted")
	}
}
