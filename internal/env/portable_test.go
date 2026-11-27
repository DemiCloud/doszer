package env

import (
	"archive/zip"
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
	// A game over a shared shell archive: the parent comes along.
	zipFile(t, filepath.Join(src, "roms", "win3x", "BOWEP.shell"), map[string]string{"AUTOBOOT.DBP": "S*dosbox_pure/Win31"})
	zipFile(t, filepath.Join(src, "roms", "win3x", "SkiFree.dosz"), map[string]string{"BOWEP.shell.parent": ""})
	write("saves/Game.pure.zip", 5)
	write("saves/Game-CDRIVE.sav", 5)
	write("saves/Gamer.pure.zip", 5) // another game's

	from := config.Default()
	from.Paths.System = filepath.Join(src, "bios")
	from.Paths.ROMs = config.List{filepath.Join(src, "roms")}
	from.Paths.Saves = filepath.Join(src, "saves")
	from.Defaults.OS = "dosbox_pure/Win98"
	keys := map[string]bool{"paths.system": true, "paths.roms": true, "paths.saves": true, "defaults.os": true}

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
		filepath.Base(exe), "config.toml",
		"roms/dos/Game.dosz",
		"roms/win3x/BOWEP.shell", "roms/win3x/SkiFree.dosz",
		"saves/Game-CDRIVE.sav", "saves/Game.pure.zip",
		"system/GeneralUser.sf2", "system/MT32_CONTROL.ROM",
		"system/dosbox_pure/Win31.dosz", "system/dosbox_pure/Win98.img",
	}
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("files:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// Its config.toml keeps the settings but not the directories, which
	// default to the portable ones.
	cfg, set, err := config.LoadFile(filepath.Join(dir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Defaults.OS != "dosbox_pure/Win98" || set["paths.system"] || set["paths.roms"] || set["paths.saves"] {
		t.Errorf("config.toml: %+v, keys %v", cfg, set)
	}

	// A second run copies nothing and keeps config.toml.
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

func zipFile(t *testing.T, p string, files map[string]string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
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
