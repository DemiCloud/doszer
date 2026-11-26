package install

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/DemiCloud/doszer/internal/config"
	"github.com/DemiCloud/doszer/internal/interrupt"
	"github.com/DemiCloud/doszer/internal/manifest"
)

// Play runs archive in DOSBox Pure and returns when its window closes.
// Its saves are named after it, in saveDir, else PlaySaveDir's, else
// Discover's, so a later Play of the same archive continues from them -
// unless doszer isn't set up (config.Configured), when they go to a
// temporary folder, removed after the session. An archive that
// boots an OS image runs it under "Discard" (playOptions): the image is
// never written to, and C: starts from it every time. options are more
// DOSBox Pure core options, by key with or without its dosbox_pure_
// prefix; specification §12's (playOptions) can't be changed.
func Play(archive, systemDir, saveDir string, options map[string]string) (Env, error) {
	coreOpts, err := sessionOptions(options)
	if err != nil {
		return Env{}, err
	}
	abs, err := filepath.Abs(archive)
	if err != nil {
		return Env{}, err
	}
	name := strings.TrimSuffix(filepath.Base(abs), filepath.Ext(abs))
	boot, title, err := readArchive(abs, name)
	if err != nil {
		return Env{}, err
	}
	if saveDir == "" {
		if saveDir, err = PlaySaveDir(abs); err != nil {
			return Env{}, err
		}
	}
	// Not set up, doszer keeps nothing: the saves live in a temporary
	// folder for the session.
	temp := false
	if saveDir == "" && !config.Configured() {
		if saveDir, err = os.MkdirTemp("", "doszer-saves-"); err != nil {
			return Env{}, err
		}
		temp = true
		done := interrupt.Remove(saveDir)
		defer func() {
			os.RemoveAll(saveDir)
			done()
		}()
	}
	env, err := Discover(systemDir, saveDir)
	env.Temporary = temp
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
		CoreOptions: coreOpts,
	}
	defer s.Close()
	return env, launch(env, s)
}

// sessionOptions is playOptions plus extra, whose keys may omit the
// dosbox_pure_ prefix.
func sessionOptions(extra map[string]string) (map[string]string, error) {
	opts := make(map[string]string, len(playOptions)+len(extra))
	for k, v := range playOptions {
		opts[k] = v
	}
	for k, v := range extra {
		if !strings.HasPrefix(k, "dosbox_pure_") {
			k = "dosbox_pure_" + k
		}
		if _, fixed := playOptions[k]; fixed {
			return nil, fmt.Errorf("option %s: archives run with %s = %s (specification §12)", k, k, playOptions[k])
		}
		opts[k] = v
	}
	return opts, nil
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
