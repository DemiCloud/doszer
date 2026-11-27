package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
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

// PortableConfigPath returns the config.toml beside the executable, or ""
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
	FromTemp      = "not set up: a temporary folder, deleted after use"
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
// else paths.system, else FindSystemDir.
func SystemDirOf(cfg Config, flag string) Dir {
	return dirOf(flag, "--system-dir", cfg.Paths.System, "paths.system", FindSystemDir)
}

// SaveDirOf returns the save directory: flag (--save-dir) if given, else
// paths.saves, else FindSaveDir, else doszer's cache (CacheSaveDir) - but
// only once doszer is set up (Configured): until then, nothing is kept,
// and run uses a temporary folder ("" with From FromTemp). run also looks
// for RetroDECK's per-folder saves first (install.PlaySaveDir).
func SaveDirOf(cfg Config, flag string) Dir {
	d := dirOf(flag, "--save-dir", cfg.Paths.Saves, "paths.saves", FindSaveDir)
	if d.Path == "" {
		if !Configured() {
			return Dir{From: FromTemp}
		}
		if c, err := CacheSaveDir(); err == nil {
			d = Dir{Path: c, From: FromCache}
		}
	}
	return d
}

// ROMDirsOf returns the ROM directories: flag (--rom-dir) if given, else
// paths.roms (one or several), else FindROMDir, or none.
func ROMDirsOf(cfg Config, flag string) []Dir {
	switch {
	case flag != "":
		return []Dir{{Path: flag, From: "--rom-dir"}}
	case len(cfg.Paths.ROMs) > 0:
		var out []Dir
		for _, p := range cfg.Paths.ROMs {
			if p != "" {
				out = append(out, Dir{Path: p, From: "paths.roms"})
			}
		}
		return out
	}
	if d := FindROMDir(); d.Path != "" {
		return []Dir{d}
	}
	return nil
}

// SourcesDirOf returns where the window keeps the sources it dumps:
// paths.sources, else ./sources or sources/ beside the executable (always,
// when portable), else, once set up, DataDir's sources folder. Until then none: the window
// dumps to a temporary folder, removed after the build ("" with From
// FromTemp).
func SourcesDirOf(cfg Config) Dir {
	d := dirOf("", "", cfg.Paths.Sources, "paths.sources", func() Dir { return findDir("sources", "") })
	if d.Path != "" {
		return d
	}
	if Portable() { // everything beside doszer, made when first used
		return Dir{Path: filepath.Join(ExeDir(), "sources"), From: FromExe}
	}
	if !Configured() {
		return Dir{From: FromTemp}
	}
	data, err := DataDir()
	if err != nil {
		return Dir{}
	}
	return Dir{Path: filepath.Join(data, "sources"), From: "the data folder"}
}

// DirPaths returns the paths of dirs.
func DirPaths(dirs []Dir) []string {
	out := make([]string, len(dirs))
	for i, d := range dirs {
		out[i] = d.Path
	}
	return out
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

// StateDir is where doszer keeps what it records - play time, the
// window's state, logs: state/ beside the executable when portable (the
// same shape as the installed layout, and out of the way of the config
// and content folders), else $XDG_STATE_HOME/doszer
// (default ~/.local/state/doszer) on Linux, %LOCALAPPDATA%\doszer on
// Windows (not roaming: nothing here is worth syncing) and the user
// config directory's doszer folder elsewhere.
func StateDir() (string, error) {
	if Portable() {
		return filepath.Join(ExeDir(), "state"), nil
	}
	switch runtime.GOOS {
	case "linux":
		base := os.Getenv("XDG_STATE_HOME")
		if base == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			base = filepath.Join(home, ".local", "state")
		}
		return filepath.Join(base, "doszer"), nil
	case "windows":
		if base := os.Getenv("LOCALAPPDATA"); base != "" {
			return filepath.Join(base, "doszer"), nil
		}
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "doszer"), nil
}

// StatsPath returns the file doszer's window keeps play time in, in
// StateDir.
func StatsPath() (string, error) {
	dir, err := StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "playtime.json"), nil
}

// LogsDir returns the folder doszer records its runs in: logs in
// StateDir.
func LogsDir() (string, error) {
	dir, err := StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "logs"), nil
}

// DataDir is where a user-profile (not portable) doszer keeps its own
// system, save and ROM folders when it sets them up (the window's first
// run): $XDG_DATA_HOME/doszer (default ~/.local/share/doszer) on Linux,
// %USERPROFILE%\doszer on Windows, ~/doszer elsewhere.
func DataDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "linux" {
		base := os.Getenv("XDG_DATA_HOME")
		if base == "" {
			base = filepath.Join(home, ".local", "share")
		}
		return filepath.Join(base, "doszer"), nil
	}
	return filepath.Join(home, "doszer"), nil
}

// Configured reports whether any config file exists for the window to
// use: the portable or user config.
func Configured() bool {
	for _, l := range Layers("") {
		if l.Found {
			return true
		}
	}
	return false
}

// SetupUser makes a user-profile configuration (not portable): the user
// config file, with system, save and ROM folders in DataDir - unless
// RetroDECK is installed, whose folders are then the defaults - and
// returns the config's path.
func SetupUser() (string, error) {
	path, err := XDGConfigPath()
	if err != nil {
		return "", err
	}
	cfg, keys := Default(), map[string]bool{}
	if RetroDECK() == (RetroDECKDirs{}) {
		data, err := DataDir()
		if err != nil {
			return "", err
		}
		cfg.Paths = Paths{ROMs: List{filepath.Join(data, "roms")}, System: filepath.Join(data, "system"), Saves: filepath.Join(data, "saves"), Sources: filepath.Join(data, "sources")}
		for _, p := range []string{cfg.Paths.ROMs[0], cfg.Paths.System, cfg.Paths.Saves, cfg.Paths.Sources} {
			if err := os.MkdirAll(p, 0o755); err != nil {
				return "", err
			}
		}
		keys = map[string]bool{"paths.roms": true, "paths.system": true, "paths.saves": true, "paths.sources": true}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if _, err := os.Stat(path); err == nil {
		return path, nil // already there: kept
	}
	return path, os.WriteFile(path, []byte(Template(cfg, keys)), 0o644)
}

// Writable reports whether files can be created in dir.
func Writable(dir string) bool {
	f, err := os.CreateTemp(dir, ".doszer-write-test-*")
	if err != nil {
		return false
	}
	f.Close()
	os.Remove(f.Name())
	return true
}
