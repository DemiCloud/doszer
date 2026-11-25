package env

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/DemiCloud/doszer/internal/config"
)

func TestPortable(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Chdir(t.TempDir())
	src := t.TempDir()
	write := func(rel string, size int64) {
		p := filepath.Join(src, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("bios/dosbox_pure/Win98.img", 8<<20)
	write("bios/dosbox_pure/Win31.dosz", 100)
	write("bios/GeneralUser.sf2", 10)
	write("bios/MT32_CONTROL.ROM", 10)
	write("bios/psx/scph1001.bin", 10) // another system's BIOS: left alone
	write("roms/dos/Game.dosz", 50)
	write("roms/snes/Other.sfc", 50) // not an archive
	write("saves/Game.pure.zip", 5)
	write("saves/Game-CDRIVE.sav", 5)
	write("saves/Gamer.pure.zip", 5) // another game's

	from := config.Default()
	from.SystemDir = filepath.Join(src, "bios")
	from.ROMDir = filepath.Join(src, "roms")
	from.SaveDir = filepath.Join(src, "saves")
	from.DefaultOS = "dosbox_pure/Win98"
	keys := map[string]bool{"system_dir": true, "rom_dir": true, "save_dir": true, "default_os": true}

	dir := filepath.Join(t.TempDir(), "doszer")
	opts := PortableOptions{Dir: dir, Settings: from, Keys: keys, Copy: true, From: from}
	if _, err := Portable(opts); err != nil {
		t.Fatal(err)
	}
	var got []string
	filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() {
			rel, _ := filepath.Rel(dir, p)
			got = append(got, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(got)
	exe, _ := os.Executable()
	want := []string{
		filepath.Base(exe), "doszer.toml",
		"roms/dos/Game.dosz",
		"saves/Game-CDRIVE.sav", "saves/Game.pure.zip",
		"system/GeneralUser.sf2", "system/MT32_CONTROL.ROM",
		"system/dosbox_pure/Win31.dosz", "system/dosbox_pure/Win98.img",
	}
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("files:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// Its doszer.toml keeps the settings but not the directories, which
	// default to the portable ones.
	cfg, set, err := config.LoadFile(filepath.Join(dir, "doszer.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultOS != "dosbox_pure/Win98" || set["system_dir"] || set["rom_dir"] || set["save_dir"] {
		t.Errorf("doszer.toml: %+v, keys %v", cfg, set)
	}

	// A second run copies nothing and keeps doszer.toml.
	acts, err := Portable(opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range acts {
		if a.Action != "skip" {
			t.Errorf("second run: %+v", a)
		}
	}
}
