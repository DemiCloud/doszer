package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlay(t *testing.T) {
	systemDir, saveDir := fakeDOSBoxPure(t)
	old := launch
	var title string
	launch = func(e Env, s *Session) error {
		title = s.Title
		if s.CoreOptions["dosbox_pure_bootos_ramdisk"] != "true" || s.CoreOptions["dosbox_pure_menu_time"] != "0" {
			t.Errorf("play core options %v", s.CoreOptions)
		}
		data, err := os.ReadFile(os.Getenv("FAKE_SAVE"))
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(e.SaveDir, s.Name+os.Getenv("FAKE_SUFFIX")), data, 0o644)
	}
	t.Cleanup(func() { launch = old })
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
		title   string
	}{
		{"plain", map[string]string{"GAME/GAME.EXE": "MZ", "AUTOBOOT.DBP": "C:\\GAME\\GAME.EXE\n"}, "", "", "Doszer (Example Game)"},
		{"OS image", map[string]string{"GAME/GAME.EXE": "MZ", "AUTOBOOT.DBP": "O*dosbox_pure/Win98"}, systemDir, "", "Doszer (Example Game)"},
		{"manifest name", map[string]string{"DOSZ.TOML": "format = 1\nname = \"The Example Game\"\n", "GAME/GAME.EXE": "MZ"}, "", "", "Doszer (The Example Game)"},
		{"manifest that doesn't parse", map[string]string{"DOSZ.TOML": "format = [", "GAME/GAME.EXE": "MZ"}, "", "", "Doszer (Example Game)"},
		{"OS image missing", map[string]string{"AUTOBOOT.DBP": "O*dosbox_pure/Win95"}, systemDir, "not found", ""},
		{"no system directory", map[string]string{"AUTOBOOT.DBP": "O*dosbox_pure/Win98\r\n0\r\n"}, "", "system directory", ""},
	}
	t.Setenv("HOME", t.TempDir()) // no RetroDECK
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			archive := filepath.Join(t.TempDir(), "Example Game.dosz")
			os.WriteFile(archive, zipBytes(t, tt.files), 0o644)
			os.Remove(filepath.Join(saveDir, "Example Game.pure.zip"))
			_, err := Play(archive, tt.system, saveDir, nil)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if title != tt.title {
				t.Errorf("title %q, want %q", title, tt.title)
			}
			// The save is named after the archive, and kept.
			if _, err := os.Stat(filepath.Join(saveDir, "Example Game.pure.zip")); err != nil {
				t.Fatal(err)
			}
		})
	}

	if _, err := Play(save+".missing", "", saveDir, nil); err == nil {
		t.Fatal("missing archive accepted")
	}
}

func TestSessionOptions(t *testing.T) {
	tests := []struct {
		name    string
		extra   map[string]string
		want    map[string]string // keys that must be set
		wantErr bool
	}{
		{"none", nil, map[string]string{"dosbox_pure_bootos_ramdisk": "true"}, false},
		{"full key", map[string]string{"dosbox_pure_cycles": "max"}, map[string]string{"dosbox_pure_cycles": "max"}, false},
		{"short key", map[string]string{"midi": "mt32"}, map[string]string{"dosbox_pure_midi": "mt32"}, false},
		{"spec option", map[string]string{"bootos_ramdisk": "diff"}, nil, true},
		{"spec option, full key", map[string]string{"dosbox_pure_menu_time": "99"}, nil, true},
	}
	for _, tt := range tests {
		got, err := sessionOptions(tt.extra)
		if (err != nil) != tt.wantErr {
			t.Errorf("%s: err = %v, wantErr %v", tt.name, err, tt.wantErr)
			continue
		}
		for k, v := range tt.want {
			if got[k] != v {
				t.Errorf("%s: %s = %q, want %q", tt.name, k, got[k], v)
			}
		}
	}
}
