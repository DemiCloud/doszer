package install

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// coreName is DOSBox Pure's libretro library_name
// (dosbox_pure_libretro.cpp, retro_get_system_info): RetroArch names the
// per-core save and options directories after it.
const coreName = "DOSBox-pure"

// retroArch is a RetroArch install running the DOSBox Pure core.
type retroArch struct {
	cmd     []string // RetroArch command line before its arguments; cmd[0] resolved
	config  string   // retroarch.cfg
	core    string   // dosbox_pure_libretro.{dll,so,dylib}, as RetroArch sees it
	options map[string]string
}

// coreFile is DOSBox Pure's core file name for a RetroArch executable.
func coreFile(exe string) string {
	switch {
	case runtime.GOOS == "windows" || strings.EqualFold(filepath.Ext(exe), ".exe"):
		return "dosbox_pure_libretro.dll"
	case runtime.GOOS == "darwin":
		return "dosbox_pure_libretro.dylib"
	}
	return "dosbox_pure_libretro.so"
}

// installed is a RetroArch install findRetroArch looks for.
type installed struct {
	cmd    []string // command line that runs it
	config string   // its retroarch.cfg, when not found from the executable
}

// findRetroArch looks for RetroArch with the DOSBox Pure core, trying
// each of retroArchInstalls in turn.
func findRetroArch(f Frontend) (Env, error) {
	var tried []string
	for _, c := range retroArchInstalls() {
		e, err := discoverRetroArch(f, c.cmd, c.config)
		if err == nil {
			return e, nil
		}
		tried = append(tried, err.Error())
	}
	msg := "no RetroArch found (set frontend_dir or pass --frontend-dir)"
	if len(tried) > 0 {
		msg = "no RetroArch with the DOSBox Pure core found (set frontend_dir or pass --frontend-dir):\n  " + strings.Join(tried, "\n  ")
	}
	return Env{}, errors.New(msg)
}

// retroArchInstalls lists the RetroArch installs findRetroArch tries, in
// order: on PATH, at its usual install locations (on every drive, under
// Windows and WSL), and as a Flatpak (Flathub's, then RetroDECK's).
func retroArchInstalls() []installed {
	var cs []installed
	seen := map[string]bool{}
	addExe := func(p string) {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && !seen[p] {
			seen[p] = true
			cs = append(cs, installed{cmd: []string{p}})
		}
	}
	for _, name := range []string{"retroarch", "retroarch.exe"} {
		if p, err := exec.LookPath(name); err == nil {
			addExe(p)
		}
	}
	windowsDirs := []string{
		"RetroArch-Win64", "RetroArch", "Program Files/RetroArch",
		"Program Files (x86)/Steam/steamapps/common/RetroArch",
		"Program Files/Steam/steamapps/common/RetroArch",
	}
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "windows":
		for _, d := range windowsDrives() {
			for _, w := range windowsDirs {
				addExe(filepath.Join(d, filepath.FromSlash(w), "retroarch.exe"))
			}
		}
		if appData := os.Getenv("APPDATA"); appData != "" {
			addExe(filepath.Join(appData, "RetroArch", "retroarch.exe"))
		}
	case "darwin":
		addExe("/Applications/RetroArch.app/Contents/MacOS/RetroArch")
	case "linux":
		if _, err := exec.LookPath("wslpath"); err == nil {
			mounts, _ := filepath.Glob("/mnt/[a-z]")
			for _, d := range mounts {
				for _, w := range windowsDirs {
					addExe(filepath.Join(d, filepath.FromSlash(w), "retroarch.exe"))
				}
			}
		}
		if home != "" {
			addExe(filepath.Join(home, ".local/share/Steam/steamapps/common/RetroArch/retroarch"))
		}
		for _, app := range []struct{ id, command string }{
			{"org.libretro.RetroArch", ""},
			{"net.retrodeck.retrodeck", "retroarch"},
		} {
			if !flatpakInstalled(home, app.id) {
				continue
			}
			cmd := []string{"flatpak", "run"}
			if app.command != "" {
				cmd = append(cmd, "--command="+app.command)
			}
			cs = append(cs, installed{
				cmd:    append(cmd, app.id),
				config: filepath.Join(home, ".var/app", app.id, "config/retroarch/retroarch.cfg"),
			})
		}
	}
	return cs
}

func windowsDrives() []string {
	var ds []string
	for c := 'C'; c <= 'Z'; c++ {
		d := string(c) + `:\`
		if _, err := os.Stat(d); err == nil {
			ds = append(ds, d)
		}
	}
	return ds
}

func flatpakInstalled(home, id string) bool {
	if _, err := exec.LookPath("flatpak"); err != nil {
		return false
	}
	for _, d := range []string{filepath.Join(home, ".local/share/flatpak/app", id), filepath.Join("/var/lib/flatpak/app", id)} {
		if _, err := os.Stat(d); err == nil {
			return true
		}
	}
	return false
}

// discoverRetroArch fills in Env for the DOSBox Pure core in the RetroArch
// cmd runs: its retroarch.cfg (config, else findRetroArchConfig), the core
// in its libretro_directory, and the system and save directories from
// retroarch.cfg unless f gives them.
func discoverRetroArch(f Frontend, cmd []string, config string) (Env, error) {
	exe, err := exec.LookPath(cmd[0])
	if err != nil {
		return Env{}, fmt.Errorf("RetroArch: %w", err)
	}
	if exe, err = filepath.Abs(exe); err != nil {
		return Env{}, err
	}
	cmd = append([]string{exe}, cmd[1:]...)
	ra := &retroArch{cmd: cmd}
	e := Env{Exe: exe, wsl: runningWSLWindowsExe(exe), ra: ra}

	if ra.config = config; ra.config == "" {
		if ra.config, err = findRetroArchConfig(exe); err != nil {
			return Env{}, err
		}
	}
	cfg, err := readRetroArchConfig(ra.config)
	if err != nil {
		return Env{}, err
	}
	cfgDir := filepath.Dir(ra.config)
	path := func(key, fallback string) (string, error) {
		v := cfg[key]
		if v == "" || v == "default" {
			return fallback, nil
		}
		return e.retroArchPath(v)
	}

	cores, err := path("libretro_directory", filepath.Join(filepath.Dir(exe), "cores"))
	if err != nil {
		return Env{}, err
	}
	ra.core = filepath.Join(cores, coreFile(exe))
	// A Flatpak's own files (/app/...) aren't visible from outside it.
	if !strings.HasPrefix(ra.core, "/app/") {
		if _, err := os.Stat(ra.core); err != nil {
			return Env{}, fmt.Errorf("RetroArch at %s has no DOSBox Pure core (%s): install it from Online Updater, Core Downloader", exe, ra.core)
		}
	}

	if e.SystemDir = f.SystemDir; e.SystemDir == "" {
		if e.SystemDir, err = path("system_directory", filepath.Join(cfgDir, "system")); err != nil {
			return Env{}, err
		}
	}
	if e.SaveDir = f.SaveDir; e.SaveDir == "" {
		if e.SaveDir, err = path("savefile_directory", filepath.Join(cfgDir, "saves")); err != nil {
			return Env{}, err
		}
		if cfg["sort_savefiles_enable"] == "true" {
			e.SaveDir = filepath.Join(e.SaveDir, coreName)
		}
	}

	// The core options the session starts from: RetroArch's per-core
	// file if it uses one, else its global core options file
	// (runloop.c, runloop_init_core_options_path).
	configDir, err := path("rgui_config_directory", cfgDir)
	if err != nil {
		return Env{}, err
	}
	optsFile := filepath.Join(configDir, coreName, coreName+".opt")
	if _, err := os.Stat(optsFile); err != nil || cfg["global_core_options"] == "true" {
		if optsFile, err = path("core_options_path", filepath.Join(cfgDir, "retroarch-core-options.cfg")); err != nil {
			return Env{}, err
		}
	}
	all, err := readRetroArchConfig(optsFile)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Env{}, err
	}
	ra.options = map[string]string{}
	for k, v := range all {
		if strings.HasPrefix(k, "dosbox_pure_") {
			ra.options[k] = v
		}
	}
	return e, nil
}

// findRetroArchConfig finds the retroarch.cfg exe uses: beside it (a
// portable or Windows install), else the platform's default location.
func findRetroArchConfig(exe string) (string, error) {
	paths := []string{filepath.Join(filepath.Dir(exe), "retroarch.cfg")}
	home, _ := os.UserHomeDir()
	switch {
	case runtime.GOOS == "windows" || strings.EqualFold(filepath.Ext(exe), ".exe"):
		if appData := os.Getenv("APPDATA"); appData != "" {
			paths = append(paths, filepath.Join(appData, "RetroArch", "retroarch.cfg"))
		}
	case runtime.GOOS == "darwin":
		paths = append(paths, filepath.Join(home, "Library/Application Support/RetroArch/config/retroarch.cfg"))
	default:
		base := os.Getenv("XDG_CONFIG_HOME")
		if base == "" {
			base = filepath.Join(home, ".config")
		}
		paths = append(paths, filepath.Join(base, "retroarch", "retroarch.cfg"))
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("no retroarch.cfg found for %s ; looked in %s", exe, strings.Join(paths, ", "))
}

// readRetroArchConfig reads a RetroArch configuration file's
// `key = "value"` lines (retroarch.cfg, core options files). Values are
// literal - no escapes - so a Windows path keeps single backslashes.
func readRetroArchConfig(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	cfg := map[string]string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
			v = v[1 : len(v)-1]
		}
		cfg[strings.TrimSpace(k)] = v
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// retroArchPath converts a path from retroarch.cfg to a local one: a
// leading ':' is RetroArch's own directory and '~' the home directory
// (libretro-common file_path.c, fill_pathname_expand_special).
func (e Env) retroArchPath(v string) (string, error) {
	dir := filepath.Dir(e.Exe)
	switch {
	case strings.HasPrefix(v, ":"):
		rest := strings.TrimLeft(v[1:], `/\`)
		if e.wsl {
			rest = strings.ReplaceAll(rest, `\`, "/")
		}
		return filepath.Join(dir, filepath.FromSlash(rest)), nil
	case strings.HasPrefix(v, "~"):
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, strings.TrimLeft(v[1:], `/\`)), nil
	}
	return e.hostPath(v, dir)
}

// retroArchSession is the configuration runRetroArch appends to
// RetroArch's own for a session, besides the directories: nothing is
// written back to retroarch.cfg, the temporary content stays out of the
// history, no save state is loaded into it, game-specific core options
// are read, saves go straight into the save directory, and closing the
// content quits RetroArch, which ends the session.
var retroArchSession = [][2]string{
	{"config_save_on_exit", "false"},
	{"history_list_enable", "false"},
	{"savestate_auto_load", "false"},
	{"game_specific_options", "true"},
	{"sort_savefiles_enable", "false"},
	{"sort_savefiles_by_content_enable", "false"},
	{"savefiles_in_content_dir", "false"},
	{"systemfiles_in_content_dir", "false"},
	{"quit_on_close_content", "1"},
}

// runRetroArch runs the session's content in RetroArch with the DOSBox
// Pure core. The session's settings go in a file passed with
// --appendconfig, so RetroArch's own configuration is left unchanged. Its
// config directory points at the session's folder, holding only a
// game-specific options file for the content: RetroArch prefers that over
// every other core options file (runloop.c,
// runloop_init_core_options_path; named after the content without its
// extension), so it carries RetroArch's DOSBox Pure options with the
// session's (Session.CoreOptions) over them, and no override file in the
// user's config directory can move the save or system directory.
func (e Env) runRetroArch(s *Session) error {
	configDir := filepath.Join(s.Dir, "retroarch-config")
	if err := os.MkdirAll(filepath.Join(configDir, coreName), 0o755); err != nil {
		return err
	}
	opts := make(map[string]string, len(e.ra.options)+len(s.CoreOptions))
	for k, v := range e.ra.options {
		opts[k] = v
	}
	for k, v := range s.CoreOptions {
		opts[k] = v
	}
	if err := writeRetroArchConfig(filepath.Join(configDir, coreName, s.Name+".opt"), sortedPairs(opts)); err != nil {
		return err
	}

	settings := append([][2]string(nil), retroArchSession...)
	for _, d := range [][2]string{{"system_directory", e.SystemDir}, {"savefile_directory", e.SaveDir}, {"rgui_config_directory", configDir}} {
		abs, err := filepath.Abs(d[1])
		if err != nil {
			return err
		}
		p, err := e.exePath(abs)
		if err != nil {
			return err
		}
		settings = append(settings, [2]string{d[0], p})
	}
	appendCfg := filepath.Join(s.Dir, "retroarch-session.cfg")
	if err := writeRetroArchConfig(appendCfg, settings); err != nil {
		return err
	}

	args := append([]string(nil), e.ra.cmd[1:]...)
	for _, a := range []struct{ flag, path string }{{"--config", e.ra.config}, {"--appendconfig", appendCfg}, {"--libretro", e.ra.core}, {"", s.Content}} {
		p := a.path
		if !strings.HasPrefix(p, "/app/") {
			var err error
			if p, err = e.exePath(p); err != nil {
				return err
			}
		}
		if a.flag != "" {
			args = append(args, a.flag)
		}
		args = append(args, p)
	}
	cmd := exec.Command(e.ra.cmd[0], args...)
	cmd.Dir = s.Dir
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("RetroArch: %w", err)
	}
	return nil
}

func sortedPairs(m map[string]string) [][2]string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([][2]string, len(keys))
	for i, k := range keys {
		out[i] = [2]string{k, m[k]}
	}
	return out
}

func writeRetroArchConfig(path string, settings [][2]string) error {
	var b strings.Builder
	for _, kv := range settings {
		fmt.Fprintf(&b, "%s = \"%s\"\n", kv[0], kv[1]) // literal, as readRetroArchConfig reads it
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}
