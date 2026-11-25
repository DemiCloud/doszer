package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// executable is os.Executable, a variable so tests can place doszer.
var executable = os.Executable

// ExeDir returns the directory holding the doszer executable, or "".
func ExeDir() string {
	exe, err := executable()
	if err != nil {
		return ""
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return filepath.Dir(exe)
}

// PortableConfigPath returns the doszer.toml beside the executable, or ""
// if there is none. Its presence makes doszer portable: that file is the
// user config instead of $XDG_CONFIG_HOME's.
func PortableConfigPath() string {
	d := ExeDir()
	if d == "" {
		return ""
	}
	p := filepath.Join(d, FileName)
	if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
		return p
	}
	return ""
}

// Portable reports whether doszer runs portable (PortableConfigPath).
func Portable() bool { return PortableConfigPath() != "" }

// Dir is a directory and where it came from: a flag, a setting's key, or
// one of the defaults' names ("./system", "beside doszer", "RetroDECK",
// "doszer's cache").
type Dir struct {
	Path string `json:"path,omitempty" yaml:"path,omitempty" toml:"path,omitempty"`
	From string `json:"from,omitempty" yaml:"from,omitempty" toml:"from,omitempty"`
}

// Default names for where a directory came from.
const (
	FromCwd       = "./"             // ./<name>, e.g. "./system"
	FromExe       = "beside doszer"  // <executable's directory>/<name>
	FromRetroDECK = "RetroDECK"      // retrodeck.json's paths
	FromCache     = "doszer's cache" // <user cache>/doszer/saves
)

// FindSystemDir returns the system directory used when none is set: the
// first that exists of ./system, system/ beside the executable (as
// standalone DOSBox Pure keeps it beside its own) and RetroDECK's BIOS
// folder.
func FindSystemDir() Dir { return findDir("system", RetroDECK().BIOS) }

// FindSaveDir returns the save directory found locally when none is set:
// ./saves or saves/ beside the executable. RetroDECK's saves are per ROM
// folder, so they are chosen per archive (install.PlaySaveDir), not here.
func FindSaveDir() Dir { return findDir("saves", "") }

// FindROMDir returns the ROM directory used when none is set: ./roms,
// roms/ beside the executable or RetroDECK's ROM folder.
func FindROMDir() Dir { return findDir("roms", RetroDECK().ROMs) }

func findDir(name, retroDECK string) Dir {
	if d := existingDir(name); d != "" {
		return Dir{Path: d, From: FromCwd + name}
	}
	if exe := ExeDir(); exe != "" {
		if d := existingDir(filepath.Join(exe, name)); d != "" {
			return Dir{Path: d, From: FromExe}
		}
	}
	if retroDECK != "" {
		return Dir{Path: retroDECK, From: FromRetroDECK}
	}
	return Dir{}
}

// CacheSaveDir is the save directory used when nothing else gives one.
func CacheSaveDir() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cache, "doszer", "saves"), nil
}

// existingDir returns d made absolute if it is a directory, else "".
func existingDir(d string) string {
	if fi, err := os.Stat(d); err == nil && fi.IsDir() {
		if abs, err := filepath.Abs(d); err == nil {
			return abs
		}
	}
	return ""
}

// RetroDECKConfig is RetroDECK's settings file, relative to the home
// directory.
var RetroDECKConfig = filepath.Join(".var", "app", "net.retrodeck.retrodeck", "config", "retrodeck", "retrodeck.json")

// RetroDECKDirs are the folders RetroDECK's settings name (paths.bios_path,
// roms_path and saves_path), each "" if unset or not an existing folder.
type RetroDECKDirs struct{ BIOS, ROMs, Saves string }

// RetroDECK reads RetroDECK's folders from its settings, or returns none
// if RetroDECK isn't installed. Read-only: doszer reads no other
// frontend's configuration.
func RetroDECK() RetroDECKDirs {
	var d RetroDECKDirs
	home, err := os.UserHomeDir()
	if err != nil {
		return d
	}
	data, err := os.ReadFile(filepath.Join(home, RetroDECKConfig))
	if err != nil {
		return d
	}
	var cfg struct {
		Paths struct {
			BIOS  string `json:"bios_path"`
			ROMs  string `json:"roms_path"`
			Saves string `json:"saves_path"`
		} `json:"paths"`
	}
	if json.Unmarshal(data, &cfg) != nil {
		return d
	}
	isDir := func(p string) string {
		if p == "" {
			return ""
		}
		if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
			return ""
		}
		return p
	}
	return RetroDECKDirs{BIOS: isDir(cfg.Paths.BIOS), ROMs: isDir(cfg.Paths.ROMs), Saves: isDir(cfg.Paths.Saves)}
}

// SystemDirOf returns the system directory: flag (--system-dir) if given,
// else system_dir, else FindSystemDir.
func SystemDirOf(cfg Config, flag string) Dir {
	return dirOf(flag, "--system-dir", cfg.SystemDir, "system_dir", FindSystemDir)
}

// SaveDirOf returns the save directory: flag (--save-dir) if given, else
// save_dir, else FindSaveDir, else doszer's cache (CacheSaveDir). run
// also looks for RetroDECK's per-folder saves before the cache
// (install.PlaySaveDir).
func SaveDirOf(cfg Config, flag string) Dir {
	d := dirOf(flag, "--save-dir", cfg.SaveDir, "save_dir", FindSaveDir)
	if d.Path == "" {
		if c, err := CacheSaveDir(); err == nil {
			d = Dir{Path: c, From: FromCache}
		}
	}
	return d
}

// ROMDirOf returns the ROM directory: flag (--rom-dir) if given, else
// rom_dir, else FindROMDir.
func ROMDirOf(cfg Config, flag string) Dir {
	return dirOf(flag, "--rom-dir", cfg.ROMDir, "rom_dir", FindROMDir)
}

func dirOf(flag, flagName, setting, key string, find func() Dir) Dir {
	switch {
	case flag != "":
		return Dir{Path: flag, From: flagName}
	case setting != "":
		return Dir{Path: setting, From: key}
	}
	return find()
}
