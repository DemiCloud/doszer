package install

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/DemiCloud/doszer/internal/interrupt"
	"github.com/DemiCloud/doszer/internal/manifest"
)

// Play runs archive in DOSBox Pure and returns when its window closes.
// Its saves are named after it, in the save directory Discover settles on,
// so a later Play of the same archive continues from them. An archive that
// boots an OS image runs it under "Discard" (playOptions): the image is
// never written to, and C: starts from it every time.
func Play(archive, systemDir, saveDir string) (Env, error) {
	abs, err := filepath.Abs(archive)
	if err != nil {
		return Env{}, err
	}
	name := strings.TrimSuffix(filepath.Base(abs), filepath.Ext(abs))
	boot, title, err := readArchive(abs, name)
	if err != nil {
		return Env{}, err
	}
	env, err := Discover(systemDir, saveDir)
	if err != nil {
		return Env{}, err
	}
	if kind, p, ok := strings.Cut(boot, "*"); ok && (kind == "O" || kind == "S") {
		if env.SystemDir == "" {
			return env, fmt.Errorf("%s boots %s from the system directory, and none is configured or found (set system_dir or pass --system-dir)", filepath.Base(archive), p)
		}
		if kind == "O" {
			if _, err := osImagePath(env.SystemDir, p); err != nil {
				return env, err
			}
		}
	}

	dir, err := os.MkdirTemp("", "doszer-run-")
	if err != nil {
		return env, err
	}
	s := &Session{
		unregister:  interrupt.Remove(dir),
		Name:        name,
		Content:     abs,
		Dir:         dir,
		Title:       "Doszer (" + title + ")",
		CoreOptions: playOptions,
	}
	defer s.Close()
	return env, launch(env, s)
}

// readArchive returns the first line of archive's AUTOBOOT.DBP (what
// DOSBox Pure starts: dosbox_pure_run.h, ReadAutoBoot), or "" if it has
// none, and the game's name from its embedded manifest, else fallback.
// It also checks that archive is a ZIP at all.
func readArchive(archive, fallback string) (boot, name string, err error) {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return "", "", fmt.Errorf("%s: %w", archive, err)
	}
	defer zr.Close()
	name = fallback
	for _, f := range zr.File {
		switch {
		case strings.EqualFold(f.Name, "AUTOBOOT.DBP"):
			data, err := readSmall(f)
			if err != nil {
				return "", "", fmt.Errorf("%s: AUTOBOOT.DBP: %w", archive, err)
			}
			line, _, _ := strings.Cut(string(data), "\n")
			boot = strings.TrimRight(line, "\r")
		case f.Name == manifest.FileName:
			// Only for the title: a manifest that doesn't parse leaves
			// the fallback.
			if data, err := readSmall(f); err == nil {
				if m, err := manifest.Parse(data, fallback); err == nil {
					name = m.Name
				}
			}
		}
	}
	return boot, name, nil
}

// readSmall reads an archive entry of at most 1 MiB.
func readSmall(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, 1<<20))
}
