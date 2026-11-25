// Package install implements `doszer install`: it runs an installer under
// DOSBox Pure, the core embedded in doszer, against a temporary copy of a
// source, then extracts what the installer wrote from DOSBox Pure's save
// files (README "Install"). It also runs a built archive (Play).
package install

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

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
// own, in the user cache directory (Play searches further first:
// PlaySaveDir).
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
		e.SystemDir = FindSystemDir()
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

// FindSystemDir returns the system directory Discover uses when none is
// given, or "" if there is none.
func FindSystemDir() string {
	d, _ := FindSystemDirFrom()
	return d
}

// FindSystemDirFrom is FindSystemDir, also saying where the directory
// came from: "./system", "beside doszer" or "RetroDECK".
func FindSystemDirFrom() (dir, from string) {
	if d := existingDir("system"); d != "" {
		return d, "./system"
	}
	if exe, err := os.Executable(); err == nil {
		if d := existingDir(filepath.Join(filepath.Dir(exe), "system")); d != "" {
			return d, "beside doszer"
		}
	}
	if d := retroDECKBIOS(); d != "" {
		return d, "RetroDECK"
	}
	return "", ""
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

// RetroDECK returns the folders RetroDECK's settings name - its BIOS
// (system), ROM and save folders - each "" if RetroDECK isn't installed
// or the folder doesn't exist.
func RetroDECK() (bios, roms, saves string) {
	d := retroDECK()
	return d.BIOS, d.ROMs, d.Saves
}

// LocalSaveDir returns the save directory Play uses before RetroDECK's,
// and where it came from: a saves/ folder in the working directory
// ("./saves") or beside the doszer executable ("beside doszer"), or "".
func LocalSaveDir() (dir, from string) {
	if d := existingDir("saves"); d != "" {
		return d, "./saves"
	}
	if exe, err := os.Executable(); err == nil {
		if d := existingDir(filepath.Join(filepath.Dir(exe), "saves")); d != "" {
			return d, "beside doszer"
		}
	}
	return "", ""
}

// retroDECKConfig is RetroDECK's settings file, under the home directory.
var retroDECKConfig = filepath.Join(".var", "app", "net.retrodeck.retrodeck", "config", "retrodeck", "retrodeck.json")

// retroDECKDirs are the folders RetroDECK's settings name (paths.bios_path,
// roms_path and saves_path), each "" if unset or not an existing folder.
type retroDECKDirs struct{ BIOS, ROMs, Saves string }

// retroDECK reads RetroDECK's folders from its settings, or returns none
// if RetroDECK isn't installed.
func retroDECK() retroDECKDirs {
	var d retroDECKDirs
	home, err := os.UserHomeDir()
	if err != nil {
		return d
	}
	data, err := os.ReadFile(filepath.Join(home, retroDECKConfig))
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
	return retroDECKDirs{BIOS: isDir(cfg.Paths.BIOS), ROMs: isDir(cfg.Paths.ROMs), Saves: isDir(cfg.Paths.Saves)}
}

// retroDECKBIOS returns RetroDECK's BIOS folder, where archives' OS images
// live in production, or "".
func retroDECKBIOS() string { return retroDECK().BIOS }

// PlaySaveDir returns the save directory Play uses for archive when none
// is given: the first that exists of a saves/ folder in the working
// directory and one beside the doszer executable (as for system/), else,
// for an archive in RetroDECK's ROM folders, the folder RetroDECK's
// RetroArch keeps its saves in: <saves_path>/<the archive's folder name>
// (RetroArch's "sort saves by content directory", so roms/dos and
// roms/windows9x each get their own), created if missing. Else "", for
// Discover's default. install doesn't search: its sessions' throwaway
// saves stay in doszer's cache.
func PlaySaveDir(archive string) (string, error) {
	if d, _ := LocalSaveDir(); d != "" {
		return d, nil
	}
	rd := retroDECK()
	if rd.ROMs == "" || rd.Saves == "" {
		return "", nil
	}
	abs, err := filepath.Abs(archive)
	if err != nil {
		return "", err
	}
	if !within(abs, rd.ROMs) {
		return "", nil // a test build, say: not RetroDECK's to keep
	}
	dir := filepath.Join(rd.Saves, filepath.Base(filepath.Dir(abs)))
	return dir, os.MkdirAll(dir, 0o755)
}

// within reports whether p is inside dir, following symbolic links (a
// Steam Deck's ROM folder is often a link to an SD card).
func within(p, dir string) bool {
	resolve := func(s string) string {
		if r, err := filepath.EvalSymlinks(s); err == nil {
			return r
		}
		return s
	}
	rel, err := filepath.Rel(resolve(dir), resolve(p))
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
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

// Well-known names of a Windows 3.x shell and a Windows 9x image, in the
// order FindSystemFile tries them (README "Boot models").
var (
	ShellNames = []string{"Windows 3.1", "Windows 3.11", "Win31", "Win311", "WFW311"}
	Win98Names = []string{"Windows 98 (games)", "Windows 98", "Win98", "Windows 98 SE", "Win98SE"}
	Win95Names = append(append([]string{}, Win98Names...), "Windows 95 (games)", "Windows 95", "Win95")
)

// SystemFile is a shell or OS image DOSBox Pure finds in a system
// directory.
type SystemFile struct {
	Name  string // relative to the system directory, slash-separated, without extension: as boot.shell and boot.os take it
	File  string // the same with its extension
	Shell bool   // a .dosz shell, else an OS image
	Size  int64
}

// SystemFiles lists the shells and OS images in systemDir as DOSBox Pure
// counts them (dosbox_pure_libretro.cpp, DBP_ScanSystem): a shell is a
// .dosz, an image an .img, .ima or .vhd of at least 7 MB in whole
// 512-byte sectors, at any depth. Sorted by name.
func SystemFiles(systemDir string) []SystemFile {
	if systemDir == "" {
		return nil
	}
	var out []SystemFile
	filepath.WalkDir(systemDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(p))
		shell := ext == ".dosz"
		if !shell && !(ext == ".img" || ext == ".ima" || ext == ".vhd") {
			return nil
		}
		if !shell && (info.Size() < 7<<20 || info.Size()%512 != 0) {
			return nil
		}
		rel, err := filepath.Rel(systemDir, p)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		out = append(out, SystemFile{Name: strings.TrimSuffix(rel, path.Ext(rel)), File: rel, Shell: shell, Size: info.Size()})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// FindSystemFile returns the first of names that names a shell (shell
// set) or an OS image in systemDir (SystemFiles), as a path relative to
// it without extension (as boot.shell and boot.os take it), or "". Names
// match case-insensitively, the shallowest file first.
func FindSystemFile(systemDir string, shell bool, names []string) string {
	found := map[string]string{} // lower-case base name without extension -> path
	depth := map[string]int{}
	for _, f := range SystemFiles(systemDir) {
		if f.Shell != shell {
			continue
		}
		k := strings.ToLower(path.Base(f.Name))
		if old, ok := depth[k]; !ok || strings.Count(f.Name, "/") < old {
			found[k], depth[k] = f.Name, strings.Count(f.Name, "/")
		}
	}
	for _, n := range names {
		if p, ok := found[strings.ToLower(n)]; ok {
			return p
		}
	}
	return ""
}
