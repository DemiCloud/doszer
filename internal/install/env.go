// Package install implements `doszer install`: it runs an installer under
// DOSBox Pure, the core embedded in doszer, against a temporary copy of a
// source, then extracts what the installer wrote from DOSBox Pure's save
// files (README "Install"). It also runs a built archive (Play).
package install

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/DemiCloud/doszer/internal/builtin"
	"github.com/DemiCloud/doszer/internal/config"
	"github.com/DemiCloud/doszer/internal/libretro"
	"github.com/DemiCloud/doszer/internal/platform"
)

// Env is where DOSBox Pure finds OS images and shells, and keeps saves.
type Env struct {
	SystemDir string // "" when none is configured or found
	SaveDir   string
	Temporary bool // SaveDir is removed after the session (Play, not set up)
}

// Discover settles DOSBox Pure's directories. A given directory must
// exist. Otherwise the system directory is the first that exists of a
// system/ folder in the working directory, one beside the doszer
// executable and RetroDECK's BIOS folder (config.FindSystemDir); the save
// directory is doszer's
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
		e.SystemDir = config.FindSystemDir().Path
	}
	if e.SaveDir == "" {
		var err error
		if e.SaveDir, err = config.CacheSaveDir(); err != nil {
			return Env{}, err
		}
		if err := os.MkdirAll(e.SaveDir, 0o755); err != nil {
			return Env{}, err
		}
	}
	return e, nil
}

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
	dir, err := PlaySaveDirNoCreate(archive)
	if err != nil || dir == "" {
		return dir, err
	}
	return dir, os.MkdirAll(dir, 0o755)
}

// PlaySaveDirNoCreate is PlaySaveDir without creating the directory.
func PlaySaveDirNoCreate(archive string) (string, error) {
	if d := config.FindSaveDir(); d.Path != "" {
		return d.Path, nil
	}
	rd := config.RetroDECK()
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
	return filepath.Join(rd.Saves, filepath.Base(filepath.Dir(abs))), nil
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

// osName reads the Windows version in a shell's or OS image's name.
var osName = regexp.MustCompile(`(?i)(?:^|[^0-9])(3\.?11|3\.?1|3\.?0|95|98|me)(?:[^0-9]|$)`)

// GuessOS reads what a shell or OS image is from its name, as init's name
// lists do (ShellNames, Win95Names, Win98Names): "Windows 98 (games)" is
// Windows 98, "WFW311" Windows 3.11. A shell is Windows 3.x unless its
// name says otherwise. NotProgram when the name doesn't tell.
func GuessOS(f SystemFile) platform.Target {
	base := path.Base(f.Name)
	m := osName.FindStringSubmatch(base)
	switch {
	case m == nil && f.Shell:
		return platform.Target{Platform: platform.Win16, Major: 3, Minor: 10}
	case m == nil:
		return platform.Target{}
	}
	switch v := strings.ToLower(strings.ReplaceAll(m[1], ".", "")); v {
	case "95":
		return platform.Target{Platform: platform.Win32, Major: 4, Minor: 0}
	case "98":
		return platform.Target{Platform: platform.Win32, Major: 4, Minor: 10}
	case "me":
		return platform.Target{Platform: platform.Win32, Major: 4, Minor: 90}
	case "30":
		return platform.Target{Platform: platform.Win16, Major: 3, Minor: 0}
	default: // 31, 311
		return platform.Target{Platform: platform.Win16, Major: 3, Minor: 10}
	}
}

// Satisfies reports whether a system that is have runs programs that need
// need: Windows 9x runs Windows 3.x programs, a newer Windows an older
// one's, and every Windows DOS programs.
func Satisfies(have, need platform.Target) bool {
	switch {
	case need.Platform == platform.DOS || need.Platform == platform.NotProgram:
		return true
	case have.Platform == platform.Win32 && need.Platform == platform.Win16:
		return true
	case have.Platform != need.Platform:
		return false
	}
	return !have.Less(need)
}
