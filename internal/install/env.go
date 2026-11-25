// Package install implements `doszer install`: it runs an installer under
// DOSBox Pure, the core embedded in doszer, against a temporary copy of a
// source, then extracts what the installer wrote from DOSBox Pure's save
// files (README "Install"). It also runs a built archive (Play).
package install

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/DemiCloud/doszer/internal/builtin"
	"github.com/DemiCloud/doszer/internal/libretro"
)

// Env is where DOSBox Pure finds OS images and shells, and keeps saves.
type Env struct {
	SystemDir string // "" when none is configured or found
	SaveDir   string
}

// Discover settles DOSBox Pure's directories. A given directory must
// exist. Otherwise the system directory is the first that exists of a
// system/ folder in the working directory, one beside the doszer
// executable (as standalone DOSBox Pure keeps it beside its own), and
// RetroDECK's BIOS folder (retroDECKBIOS); the save directory is doszer's
// own, in the user cache directory: doszer never reads or writes a
// frontend's saves.
func Discover(systemDir, saveDir string) (Env, error) {
	for _, d := range []string{systemDir, saveDir} {
		if d == "" {
			continue
		}
		if fi, err := os.Stat(d); err != nil || !fi.IsDir() {
			return Env{}, fmt.Errorf("%s: not a directory", d)
		}
	}
	e := Env{SystemDir: systemDir, SaveDir: saveDir}
	if e.SystemDir == "" {
		e.SystemDir = findSystemDir()
	}
	if e.SaveDir == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			return Env{}, err
		}
		e.SaveDir = filepath.Join(cache, "doszer", "saves")
		if err := os.MkdirAll(e.SaveDir, 0o755); err != nil {
			return Env{}, err
		}
	}
	return e, nil
}

// findSystemDir returns the system directory Discover uses when none is
// given, or "" if there is none.
func findSystemDir() string {
	candidates := []string{"system"}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "system"))
	}
	for _, d := range candidates {
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			if abs, err := filepath.Abs(d); err == nil {
				return abs
			}
		}
	}
	return retroDECKBIOS()
}

// retroDECKConfig is RetroDECK's settings file, under the home directory.
var retroDECKConfig = filepath.Join(".var", "app", "net.retrodeck.retrodeck", "config", "retrodeck", "retrodeck.json")

// retroDECKBIOS returns the BIOS folder RetroDECK's settings name
// (paths.bios_path), where archives' OS images live in production, or ""
// if RetroDECK isn't installed or the folder doesn't exist.
func retroDECKBIOS() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(home, retroDECKConfig))
	if err != nil {
		return ""
	}
	var cfg struct {
		Paths struct {
			BIOS string `json:"bios_path"`
		} `json:"paths"`
	}
	if json.Unmarshal(data, &cfg) != nil || cfg.Paths.BIOS == "" {
		return ""
	}
	if fi, err := os.Stat(cfg.Paths.BIOS); err != nil || !fi.IsDir() {
		return ""
	}
	return cfg.Paths.BIOS
}

// launch runs a session's content in DOSBox Pure and returns when it
// closes. A variable so tests can stand in for DOSBox Pure.
var launch = runBuiltin

// runBuiltin runs the session in doszer's own window with the embedded
// core, which takes its directories and core options from doszer directly
// (internal/libretro).
func runBuiltin(e Env, s *Session) error {
	core, sdlLibs, err := builtin.Extract()
	if err != nil {
		return err
	}
	systemDir := e.SystemDir
	if systemDir == "" {
		systemDir = s.Dir // nothing to boot from: any directory will do
	}
	return libretro.Run(libretro.Config{
		Core:      core,
		SDL:       sdlLibs,
		Content:   s.Content,
		SystemDir: systemDir,
		SaveDir:   e.SaveDir,
		Options:   s.CoreOptions,
		Title:     s.Title,
	})
}
