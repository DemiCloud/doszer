package install

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/DemiCloud/doszer/internal/interrupt"
)

// Play runs archive in DOSBox Pure and returns when its window closes.
// Its saves are named after it, in the save directory Discover settles on,
// so a later Play of the same archive continues from them. An archive that
// boots an OS image runs it under "Save Difference Per Content"
// (osCoreOptions): the image is never written to.
func Play(archive, systemDir, saveDir string) (Env, error) {
	abs, err := filepath.Abs(archive)
	if err != nil {
		return Env{}, err
	}
	boot, err := autobootLine(abs)
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
			if err := checkDiffSafe(env.SystemDir, p); err != nil {
				return env, err
			}
		}
	}

	dir, err := os.MkdirTemp("", "doszer-run-")
	if err != nil {
		return env, err
	}
	name := strings.TrimSuffix(filepath.Base(abs), filepath.Ext(abs))
	s := &Session{
		unregister:  interrupt.Remove(dir),
		Name:        name,
		Content:     abs,
		Dir:         dir,
		Title:       "doszer run - " + name,
		CoreOptions: osCoreOptions,
	}
	defer s.Close()
	return env, launch(env, s)
}

// autobootLine returns the first line of archive's AUTOBOOT.DBP (what
// DOSBox Pure starts: dosbox_pure_run.h, ReadAutoBoot), or "" if it has
// none. It also checks that archive is a ZIP at all.
func autobootLine(archive string) (string, error) {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return "", fmt.Errorf("%s: %w", archive, err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if !strings.EqualFold(f.Name, "AUTOBOOT.DBP") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", err
		}
		defer rc.Close()
		data, err := io.ReadAll(io.LimitReader(rc, 4096))
		if err != nil {
			return "", fmt.Errorf("%s: AUTOBOOT.DBP: %w", archive, err)
		}
		line, _, _ := strings.Cut(string(data), "\n")
		return strings.TrimRight(line, "\r"), nil
	}
	return "", nil
}
