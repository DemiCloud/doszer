package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// isolate sandboxes Load from the real environment: an empty working
// directory and an empty XDG_CONFIG_HOME, so tests never read the actual
// user's config files.
func isolate(t *testing.T) {
	t.Helper()
	t.Chdir(t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDefault(t *testing.T) {
	if got := Default().OggQuality; got != 6 {
		t.Errorf("OggQuality = %d, want 6", got)
	}
}

func TestLoadNoFilesReturnsDefaults(t *testing.T) {
	isolate(t)

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(cfg, Default()) {
		t.Errorf("cfg = %+v, want defaults %+v", cfg, Default())
	}
}

func TestLoadUserConfigLayer(t *testing.T) {
	isolate(t)

	userPath, err := UserConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, userPath, `ogg_quality = 8`)

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.OggQuality != 8 {
		t.Errorf("OggQuality = %d, want 8", cfg.OggQuality)
	}
}

func TestLoadCwdOverridesUserConfig(t *testing.T) {
	isolate(t)

	userPath, err := UserConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, userPath, `ogg_quality = 8`)
	writeFile(t, FileName, `ogg_quality = 3`)

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.OggQuality != 3 {
		t.Errorf("OggQuality = %d, want 3", cfg.OggQuality)
	}
}

func TestLoadSourceOverridesCwd(t *testing.T) {
	isolate(t)

	writeFile(t, FileName, `ogg_quality = 3`)

	source := t.TempDir()
	writeFile(t, filepath.Join(source, "DOSZ.TOML"), `
format = 1

[x-doszer]
ogg_quality = 9
`)

	cfg, err := Load(source)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.OggQuality != 9 {
		t.Errorf("OggQuality = %d, want 9", cfg.OggQuality)
	}
}

func TestLoadIgnoresOrdinaryManifestKeys(t *testing.T) {
	isolate(t)

	source := t.TempDir()
	writeFile(t, filepath.Join(source, "DOSZ.TOML"), `
format = 1
name   = "Example Game"

[boot]
exec = 'C:\GAME.EXE'

[[file]]
path = "A.BAT"
text = "@ECHO OFF\r\n"
`)

	cfg, err := Load(source)
	if err != nil {
		t.Fatalf("Load should ignore ordinary manifest keys: %v", err)
	}
	if !reflect.DeepEqual(cfg, Default()) {
		t.Errorf("cfg = %+v, want unchanged defaults %+v", cfg, Default())
	}
}

func TestLoadMergesTemplatesByKey(t *testing.T) {
	isolate(t)

	userPath, err := UserConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, userPath, `
[templates]
a = "user-a"
b = "user-b"
`)
	writeFile(t, FileName, `
[templates]
b = "cwd-b"
c = "cwd-c"
`)

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := map[string]string{"a": "user-a", "b": "cwd-b", "c": "cwd-c"}
	if !reflect.DeepEqual(cfg.Templates, want) {
		t.Errorf("Templates = %v, want %v", cfg.Templates, want)
	}
}

func TestLoadRejectsUnknownKeyInOwnFile(t *testing.T) {
	isolate(t)

	writeFile(t, FileName, `ogg_qualityy = 3`)

	if _, err := Load(""); err == nil {
		t.Fatal("Load should reject an unknown key in doszer.toml")
	}
}

func TestLoadMissingSourceDirSkipsLayer(t *testing.T) {
	isolate(t)

	cfg, err := Load(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(cfg, Default()) {
		t.Errorf("cfg = %+v, want defaults %+v", cfg, Default())
	}
}

func TestUserConfigPathRespectsXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/xdg")

	got, err := UserConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join("/xdg", "doszer", "config.toml"); got != want {
		t.Errorf("UserConfigPath() = %q, want %q", got, want)
	}
}

func TestUserConfigPathFallsBackToHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "/home/example")

	got, err := UserConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join("/home/example", ".config", "doszer", "config.toml"); got != want {
		t.Errorf("UserConfigPath() = %q, want %q", got, want)
	}
}
