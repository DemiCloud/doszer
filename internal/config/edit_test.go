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
		{"new file", "", func(p string) error { return Set(p, "build.ogg_quality", "8") },
			"[build]\nogg_quality = 8\n", false},
		{"replace keeps comments", "# mine\n[build]\nogg_quality = 3 # low\n[defaults]\nos = 'x'\n", func(p string) error { return Set(p, "build.ogg_quality", "9") },
			"# mine\n[build]\nogg_quality = 9\n[defaults]\nos = 'x'\n", false},
		{"key into its table", "[build]\nogg_quality = 3\n\n[install.templates]\na = '/a'\n", func(p string) error { return Set(p, "build.redump_dat", "/d.dat") },
			"[build]\nogg_quality = 3\nredump_dat = '/d.dat'\n\n[install.templates]\na = '/a'\n", false},
		{"table entry into its table", "[install.os]\n'a/b' = 'c'\n\n[install.templates]\n", func(p string) error { return Set(p, "install.os.x/y", "z") },
			"[install.os]\n'a/b' = 'c'\n'x/y' = 'z'\n\n[install.templates]\n", false},
		{"table created", "[build]\nogg_quality = 3\n", func(p string) error { return Set(p, "install.os.a", "b") },
			"[build]\nogg_quality = 3\n\n[install.os]\na = 'b'\n", false},
		{"replace quoted table key", "[install.os]\n\"a/b\" = 'c'\n", func(p string) error { return Set(p, "install.os.a/b", "d") },
			"[install.os]\n'a/b' = 'd'\n", false},
		{"several ROM folders", "", func(p string) error { return Set(p, "paths.roms", "/a", "/b") },
			"[paths]\nroms = ['/a', '/b']\n", false},
		{"one ROM folder", "[paths]\nroms = ['/a', '/b']\n", func(p string) error { return Set(p, "paths.roms", "/c") },
			"[paths]\nroms = '/c'\n", false},
		{"unset", "[build]\nogg_quality = 3\n[defaults]\nos = 'x'\n", func(p string) error { return Unset(p, "build.ogg_quality") },
			"[build]\n[defaults]\nos = 'x'\n", false},
		{"unset table key", "[install.templates]\na = '/a'\nb = '/b'\n", func(p string) error { return Unset(p, "install.templates.a") },
			"[install.templates]\nb = '/b'\n", false},
		{"unknown key", "", func(p string) error { return Set(p, "ogg", "1") }, "", true},
		{"flat key", "", func(p string) error { return Set(p, "system_dir", "/s") }, "", true},
		{"several values", "", func(p string) error { return Set(p, "paths.system", "/a", "/b") }, "", true},
		{"bad quality", "", func(p string) error { return Set(p, "build.ogg_quality", "11") }, "", true},
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
	if err := Set(p, "paths.system", "system"); err != nil {
		t.Fatal(err)
	}
	if err := Set(p, "paths.saves", outside); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if want := "[paths]\nsystem = 'system'\nsaves = '" + outside + "'\n"; string(got) != want {
		t.Errorf("file =\n%s\nwant\n%s", got, want)
	}
}

func TestTemplateLoads(t *testing.T) {
	isolate(t)
	for _, current := range []bool{false, true} {
		set, keys := Default(), map[string]bool{}
		if current {
			set.Paths.System, set.Build.OggQuality = "/s", 9
			set.Paths.ROMs = List{"/r1", "/r2"}
			set.Install.OS = map[string]string{"a/b": "c"}
			keys = map[string]bool{"paths.system": true, "paths.roms": true, "build.ogg_quality": true, "install.os.a/b": true}
		}
		text := Template(set, keys)
		userPath, err := UserConfigPath()
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, userPath, text)
		cfg, err := Load("")
		if err != nil {
			t.Fatalf("current %v: %v\n%s", current, err, text)
		}
		if current && (cfg.Paths.System != "/s" || cfg.Build.OggQuality != 9 || cfg.Install.OS["a/b"] != "c" || len(cfg.Paths.ROMs) != 2) {
			t.Errorf("loaded %+v from\n%s", cfg, text)
		}
		if !current && cfg.Paths.System != "" {
			t.Errorf("default template sets paths.system")
		}
		for _, k := range Keys {
			if !strings.Contains(text, k[strings.LastIndexByte(k, '.')+1:]) {
				t.Errorf("template lacks %s", k)
			}
		}
	}
}
