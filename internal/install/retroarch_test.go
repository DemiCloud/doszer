package install

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/DemiCloud/doszer/internal/builtin"
)

// fakeRetroArch writes a RetroArch install: a script that stands in for
// RetroArch, a retroarch.cfg with paths relative to it (':'), the DOSBox
// Pure core and a per-core options file. The script copies $FAKE_SAVE
// into the save directory its --appendconfig names, under the content's
// name with $FAKE_SUFFIX, and the content's game options file to
// $FAKE_OPT.
func fakeRetroArch(t *testing.T) (dir string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	dir = t.TempDir()
	for _, d := range []string{"cores", "system", "saves/" + coreName, "config/" + coreName} {
		os.MkdirAll(filepath.Join(dir, d), 0o755)
	}
	os.WriteFile(filepath.Join(dir, "cores", coreFile("retroarch")), nil, 0o644)
	os.WriteFile(filepath.Join(dir, "retroarch.cfg"), []byte(`# RetroArch
libretro_directory = ":/cores"
system_directory = ":/system"
savefile_directory = ":/saves"
rgui_config_directory = ":/config"
sort_savefiles_enable = "true"
global_core_options = "false"
`), 0o644)
	os.WriteFile(filepath.Join(dir, "config", coreName, coreName+".opt"),
		[]byte("dosbox_pure_cycles = \"max\"\ndosbox_pure_bootos_ramdisk = \"false\"\nother_core_option = \"x\"\n"), 0o644)
	script := `#!/bin/sh
while [ $# -gt 1 ]; do
	case "$1" in --appendconfig) app="$2"; shift;; esac
	shift
done
get() { sed -n "s/^$1 = \"\(.*\)\"$/\1/p" "$app"; }
name=$(basename "$1" .dosz)
[ -z "$FAKE_SAVE" ] || cp "$FAKE_SAVE" "$(get savefile_directory)/$name$FAKE_SUFFIX"
[ -z "$FAKE_OPT" ] || cp "$(get rgui_config_directory)/` + coreName + `/$name.opt" "$FAKE_OPT"
`
	os.WriteFile(filepath.Join(dir, "retroarch"), []byte(script), 0o755)
	return dir
}

func TestDiscoverRetroArch(t *testing.T) {
	dir := fakeRetroArch(t)
	kinds := []string{RetroArch}
	if !builtin.Available() {
		kinds = append(kinds, "") // RetroArch is the default without the embedded core
	}
	for _, kind := range kinds {
		e, err := Discover(Frontend{Kind: kind, Dir: dir})
		if err != nil {
			t.Fatal(err)
		}
		if e.ra == nil || e.ra.core != filepath.Join(dir, "cores", coreFile("retroarch")) {
			t.Fatalf("core %+v", e.ra)
		}
		if e.SystemDir != filepath.Join(dir, "system") || e.SaveDir != filepath.Join(dir, "saves", coreName) {
			t.Fatalf("dirs %s %s", e.SystemDir, e.SaveDir)
		}
		if len(e.ra.options) != 2 || e.ra.options["dosbox_pure_cycles"] != "max" {
			t.Fatalf("options %v", e.ra.options)
		}
	}

	// The executable's own path names its directory.
	if _, err := Discover(Frontend{Kind: RetroArch, Dir: filepath.Join(dir, "retroarch")}); err != nil {
		t.Fatal(err)
	}

	sys, saves := t.TempDir(), t.TempDir()
	e, err := Discover(Frontend{Kind: RetroArch, Dir: dir, SystemDir: sys, SaveDir: saves})
	if err != nil {
		t.Fatal(err)
	}
	if e.SystemDir != sys || e.SaveDir != saves {
		t.Fatal("explicit dirs ignored")
	}

	if _, err := Discover(Frontend{Kind: "mame", Dir: dir}); err == nil {
		t.Fatal("unknown frontend accepted")
	}
	os.Remove(filepath.Join(dir, "cores", coreFile("retroarch")))
	if _, err := Discover(Frontend{Kind: RetroArch, Dir: dir}); err == nil || !strings.Contains(err.Error(), "Core Downloader") {
		t.Fatalf("missing core: %v", err)
	}
}

func TestReadRetroArchConfig(t *testing.T) {
	p := filepath.Join(t.TempDir(), "retroarch.cfg")
	os.WriteFile(p, []byte("# comment\na = \"1\"\n  b=\"two words\"\nc = 3\nnot a setting\nd = \"C:\\temp\\new\"\n"), 0o644)
	cfg, err := readRetroArchConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"a": "1", "b": "two words", "c": "3", "d": `C:\temp\new`}
	for k, v := range want {
		if cfg[k] != v {
			t.Errorf("%s = %q, want %q", k, cfg[k], v)
		}
	}
	if len(cfg) != len(want) {
		t.Errorf("config %v", cfg)
	}
}

func TestRunRetroArchSession(t *testing.T) {
	dir := fakeRetroArch(t)
	saveDir := filepath.Join(dir, "saves", coreName)
	save := filepath.Join(t.TempDir(), "fixture.pure.zip")
	os.WriteFile(save, zipBytes(t, map[string]string{"GAME/GAME.EXE": "installed", "GAME/GAME.CFG": "cfg"}), 0o644)
	t.Setenv("FAKE_SAVE", save)
	t.Setenv("FAKE_SUFFIX", ".pure.zip")
	before, _ := os.ReadFile(filepath.Join(dir, "retroarch.cfg"))

	src := writeSource(t, "format = 1\nname = \"Example Game\"\n", map[string]string{"INSTALL/SETUP.EXE": "MZ"})
	rep, err := Run(Options{Source: src, Frontend: RetroArch, FrontendDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Dir != "GAME" || len(rep.Extracted.Written) != 2 {
		t.Fatalf("report %+v", rep)
	}
	if entries, _ := os.ReadDir(saveDir); len(entries) != 0 {
		t.Fatal("save file not cleaned up")
	}
	if after, _ := os.ReadFile(filepath.Join(dir, "retroarch.cfg")); string(after) != string(before) {
		t.Fatal("retroarch.cfg changed")
	}
}

func TestRetroArchCoreOptions(t *testing.T) {
	dir := fakeRetroArch(t)
	e, err := Discover(Frontend{Kind: RetroArch, Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	opt := filepath.Join(t.TempDir(), "game.opt")
	t.Setenv("FAKE_OPT", opt)
	s := &Session{Name: "doszer-install-0", Dir: t.TempDir(), CoreOptions: osCoreOptions}
	s.Content = filepath.Join(s.Dir, s.Name+".dosz")
	if err := e.Run(s); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(opt)
	if err != nil {
		t.Fatal(err)
	}
	// RetroArch's own DOSBox Pure options, the session's over them.
	want := "dosbox_pure_bootos_dfreespace = \"1024\"\ndosbox_pure_bootos_ramdisk = \"diff\"\ndosbox_pure_cycles = \"max\"\n"
	if string(got) != want {
		t.Fatalf("game options:\n%s", got)
	}
}

func TestDiscoverBuiltin(t *testing.T) {
	if !builtin.Available() {
		if _, err := Discover(Frontend{Kind: Builtin}); err == nil {
			t.Fatal("builtin frontend without an embedded core")
		}
		t.Skip("no embedded core")
	}
	sys, saves := t.TempDir(), t.TempDir()
	for _, kind := range []string{"", Builtin} {
		e, err := Discover(Frontend{Kind: kind, SystemDir: sys, SaveDir: saves})
		if err != nil {
			t.Fatal(err)
		}
		if !e.builtin || e.ra != nil || e.SystemDir != sys || e.SaveDir != saves {
			t.Fatalf("env %+v", e)
		}
	}
}
