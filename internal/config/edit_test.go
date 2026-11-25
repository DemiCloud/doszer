package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetUnset(t *testing.T) {
	tests := []struct {
		name    string
		before  string
		op      func(p string) error
		want    string
		wantErr bool
	}{
		{"new file", "", func(p string) error { return Set(p, "ogg_quality", "8") },
			"ogg_quality = 8\n", false},
		{"replace keeps comments", "# mine\nogg_quality = 3 # low\ndefault_os = 'x'\n", func(p string) error { return Set(p, "ogg_quality", "9") },
			"# mine\nogg_quality = 9\ndefault_os = 'x'\n", false},
		{"top-level key goes before tables", "ogg_quality = 3\n\n[templates]\na = '/a'\n", func(p string) error { return Set(p, "default_os", "dosbox_pure/Win98") },
			"ogg_quality = 3\ndefault_os = 'dosbox_pure/Win98'\n\n[templates]\na = '/a'\n", false},
		{"table key into its table", "[install_os]\n'a/b' = 'c'\n\n[templates]\n", func(p string) error { return Set(p, "install_os.x/y", "z") },
			"[install_os]\n'a/b' = 'c'\n'x/y' = 'z'\n\n[templates]\n", false},
		{"table created", "ogg_quality = 3\n", func(p string) error { return Set(p, "install_os.a", "b") },
			"ogg_quality = 3\n\n[install_os]\na = 'b'\n", false},
		{"replace quoted table key", "[install_os]\n\"a/b\" = 'c'\n", func(p string) error { return Set(p, "install_os.a/b", "d") },
			"[install_os]\n'a/b' = 'd'\n", false},
		{"unset", "ogg_quality = 3\ndefault_os = 'x'\n", func(p string) error { return Unset(p, "ogg_quality") },
			"default_os = 'x'\n", false},
		{"unset table key", "[templates]\na = '/a'\nb = '/b'\n", func(p string) error { return Unset(p, "templates.a") },
			"[templates]\nb = '/b'\n", false},
		{"unknown key", "", func(p string) error { return Set(p, "ogg", "1") }, "", true},
		{"bad quality", "", func(p string) error { return Set(p, "ogg_quality", "11") }, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "c", FileName)
			if tt.before != "" {
				writeFile(t, p, tt.before)
			}
			err := tt.op(p)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			got, _ := os.ReadFile(p)
			if string(got) != tt.want {
				t.Errorf("file =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

func TestSetPathRelativeToFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	p := filepath.Join(dir, FileName)
	outside := t.TempDir()
	if err := Set(p, "system_dir", "system"); err != nil {
		t.Fatal(err)
	}
	if err := Set(p, "save_dir", outside); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if want := "system_dir = 'system'\nsave_dir = '" + outside + "'\n"; string(got) != want {
		t.Errorf("file =\n%s\nwant\n%s", got, want)
	}
}

func TestTemplateLoads(t *testing.T) {
	isolate(t)
	for _, current := range []bool{false, true} {
		set, keys := Default(), map[string]bool{}
		if current {
			set.SystemDir, set.OggQuality = "/s", 9
			set.InstallOS = map[string]string{"a/b": "c"}
			keys = map[string]bool{"system_dir": true, "ogg_quality": true, "install_os.a/b": true}
		}
		text := Template(set, keys)
		writeFile(t, FileName, text)
		cfg, err := Load("")
		if err != nil {
			t.Fatalf("current %v: %v\n%s", current, err, text)
		}
		if current && (cfg.SystemDir != "/s" || cfg.OggQuality != 9 || cfg.InstallOS["a/b"] != "c") {
			t.Errorf("loaded %+v from\n%s", cfg, text)
		}
		if !current && cfg.SystemDir != "" {
			t.Errorf("default template sets system_dir")
		}
		for _, k := range Keys {
			if !strings.Contains(text, k) {
				t.Errorf("template lacks %s", k)
			}
		}
	}
}
