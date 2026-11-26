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
	// Relative template paths are relative to the file that sets them.
	cwd, _ := os.Getwd()
	userDir := filepath.Dir(userPath)
	want := map[string]string{"a": filepath.Join(userDir, "user-a"), "b": filepath.Join(cwd, "cwd-b"), "c": filepath.Join(cwd, "cwd-c")}
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

func TestLoadResolvesRelativePaths(t *testing.T) {
	isolate(t)
	userPath, _ := UserConfigPath()
	abs := filepath.Join(t.TempDir(), "saves")
	writeFile(t, userPath, "system_dir = \"system\"\nsave_dir = '"+abs+"'\nrom_dir = \"../roms\"\ndefault_os = \"dosbox_pure/Win98\"\n")

	cfg, origins, err := LoadOrigins("")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(userPath)
	tests := []struct{ got, want string }{
		{cfg.SystemDir, filepath.Join(dir, "system")},
		{cfg.SaveDir, abs},
		{cfg.ROMDir, filepath.Join(filepath.Dir(dir), "roms")},
		{cfg.DefaultOS, "dosbox_pure/Win98"}, // a system directory path, not a host one
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("got %q, want %q", tt.got, tt.want)
		}
	}
	for _, k := range []string{"system_dir", "save_dir", "rom_dir", "default_os"} {
		if origins[k] != userPath {
			t.Errorf("origins[%s] = %q, want %q", k, origins[k], userPath)
		}
	}
	if _, ok := origins["ogg_quality"]; ok {
		t.Error("ogg_quality has an origin but no file sets it")
	}
}

func TestPortable(t *testing.T) {
	isolate(t)
	exeDir := t.TempDir()
	executable = func() (string, error) { return filepath.Join(exeDir, "doszer"), nil }
	t.Cleanup(func() { executable = os.Executable })

	xdg, _ := XDGConfigPath()
	writeFile(t, xdg, "ogg_quality = 2")
	if Portable() {
		t.Fatal("portable without a doszer.toml beside the executable")
	}
	if p, _ := UserConfigPath(); p != xdg {
		t.Fatalf("UserConfigPath = %q, want %q", p, xdg)
	}

	portable := filepath.Join(exeDir, FileName)
	writeFile(t, portable, "ogg_quality = 4")
	if !Portable() {
		t.Fatal("not portable with a doszer.toml beside the executable")
	}
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OggQuality != 4 {
		t.Errorf("OggQuality = %d, want the portable file's 4 (XDG's ignored)", cfg.OggQuality)
	}

	// Run from the executable's directory, ./doszer.toml is the same file:
	// one layer, not two.
	t.Chdir(exeDir)
	if l := Layers(""); len(l) != 1 || l[0].Kind != "portable" {
		t.Errorf("Layers = %+v, want the portable file only", l)
	}

	// Directories beside the executable are defaults.
	os.MkdirAll(filepath.Join(exeDir, "roms"), 0o755)
	t.Chdir(t.TempDir())
	if d := FindROMDir(); d.From != FromExe || filepath.Base(d.Path) != "roms" {
		t.Errorf("FindROMDir = %+v", d)
	}
	os.MkdirAll("roms", 0o755)
	if d := FindROMDir(); d.From != "./roms" {
		t.Errorf("FindROMDir = %+v, want ./roms first", d)
	}
}

func TestDirOf(t *testing.T) {
	isolate(t)
	t.Setenv("HOME", t.TempDir())
	cfg := Default()
	if d := ROMDirOf(cfg, ""); d != (Dir{}) {
		t.Errorf("ROMDirOf with nothing = %+v", d)
	}
	rd := t.TempDir()
	home, _ := os.UserHomeDir()
	writeFile(t, filepath.Join(home, RetroDECKConfig), `{"paths": {"roms_path": "`+rd+`"}}`)
	if d := ROMDirOf(cfg, ""); d != (Dir{Path: rd, From: FromRetroDECK}) {
		t.Errorf("ROMDirOf = %+v, want RetroDECK's", d)
	}
	cfg.ROMDir = "/r"
	if d := ROMDirOf(cfg, ""); d != (Dir{Path: "/r", From: "rom_dir"}) {
		t.Errorf("ROMDirOf = %+v, want rom_dir", d)
	}
	if d := ROMDirOf(cfg, "/f"); d != (Dir{Path: "/f", From: "--rom-dir"}) {
		t.Errorf("ROMDirOf = %+v, want the flag", d)
	}
}

func TestSetupUser(t *testing.T) {
	isolate(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if Configured() {
		t.Fatal("configured before setup")
	}
	p, err := SetupUser()
	if err != nil {
		t.Fatal(err)
	}
	if !Configured() {
		t.Fatal("not configured after setup")
	}
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := DataDir()
	if cfg.ROMDir != filepath.Join(data, "roms") || cfg.SystemDir != filepath.Join(data, "system") {
		t.Errorf("cfg = %+v", cfg)
	}
	if fi, err := os.Stat(cfg.SaveDir); err != nil || !fi.IsDir() {
		t.Errorf("save dir not made: %v", err)
	}
	if xdg, _ := XDGConfigPath(); p != xdg {
		t.Errorf("path = %s, want %s", p, xdg)
	}
}
