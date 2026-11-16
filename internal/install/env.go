// Package install implements `doszer install`: it runs an installer under
// standalone DOSBox Pure against a temporary copy of a source, then
// extracts what the installer wrote from DOSBox Pure's save files (README
// "Install").
package install

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Env locates standalone DOSBox Pure and its system and save directories.
type Env struct {
	Exe       string // DOSBox Pure executable
	SystemDir string
	SaveDir   string
	wsl       bool // Exe is a Windows program run from WSL

	// settings, when the system or save directory was given rather than
	// read from DOSBox Pure's configuration: that configuration (empty if
	// there is none), which Run passes on with both directories set.
	settings map[string]any
}

// Discover fills in Env for exe. systemDir and saveDir override what
// DOSBox Pure's own configuration says, for doszer and for the DOSBox Pure
// it runs (Run). Otherwise they are read the way
// standalone DOSBox Pure resolves them (dosbox-pure-unleashed main.cpp,
// sDOSBoxPure::Load) when started from its own directory, as install does:
// DOSBoxPure.cfg in that directory, its path_system/path_saves settings,
// else the system/ and saves/ directories beside it.
func Discover(exe, systemDir, saveDir string) (Env, error) {
	if exe == "" {
		return Env{}, errors.New("no DOSBox Pure executable configured (set dosbox_pure or pass --dosbox-pure)")
	}
	exe, err := filepath.Abs(exe)
	if err != nil {
		return Env{}, err
	}
	if _, err := os.Stat(exe); err != nil {
		return Env{}, fmt.Errorf("DOSBox Pure executable: %w", err)
	}
	e := Env{Exe: exe, wsl: runningWSLWindowsExe(exe)}

	given := systemDir != "" || saveDir != ""
	for _, d := range []string{systemDir, saveDir} {
		if d == "" {
			continue
		}
		if fi, err := os.Stat(d); err != nil || !fi.IsDir() {
			return Env{}, fmt.Errorf("%s: not a directory", d)
		}
	}
	dir := filepath.Dir(exe)
	cfg := map[string]any{}
	data, err := os.ReadFile(filepath.Join(dir, "DOSBoxPure.cfg"))
	switch {
	case err == nil:
		if err := json.Unmarshal(data, &cfg); err != nil {
			return Env{}, fmt.Errorf("DOSBoxPure.cfg: %w", err)
		}
	case systemDir == "" || saveDir == "":
		return Env{}, fmt.Errorf("no DOSBoxPure.cfg beside %s to locate its system and save directories (set system_dir and save_dir): %w", exe, err)
	}
	if given {
		e.settings = cfg
	}
	resolve := func(key, fallback string) (string, error) {
		if v, ok := cfg[key].(string); ok && v != "" {
			return e.hostPath(v, dir)
		}
		return filepath.Join(dir, fallback), nil
	}
	if systemDir == "" {
		if systemDir, err = resolve("path_system", "system"); err != nil {
			return Env{}, err
		}
	}
	if saveDir == "" {
		if saveDir, err = resolve("path_saves", "saves"); err != nil {
			return Env{}, err
		}
	}
	e.SystemDir, e.SaveDir = systemDir, saveDir
	return e, nil
}

// runningWSLWindowsExe reports whether exe is a Windows program being run
// from WSL, whose path arguments must be converted with wslpath.
func runningWSLWindowsExe(exe string) bool {
	if runtime.GOOS != "linux" || !strings.EqualFold(filepath.Ext(exe), ".exe") {
		return false
	}
	_, err := exec.LookPath("wslpath")
	return err == nil
}

// hostPath converts a path from DOSBox Pure's configuration to a local
// one, relative paths resolving against dir.
func (e Env) hostPath(p, dir string) (string, error) {
	if e.wsl && (strings.Contains(p, `\`) || (len(p) > 1 && p[1] == ':')) {
		out, err := exec.Command("wslpath", "-u", p).Output()
		if err != nil {
			return "", fmt.Errorf("wslpath -u %s: %w", p, err)
		}
		p = strings.TrimSpace(string(out))
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	return p, nil
}

// exePath converts a local path for DOSBox Pure's command line.
func (e Env) exePath(p string) (string, error) {
	if !e.wsl {
		return p, nil
	}
	out, err := exec.Command("wslpath", "-w", p).Output()
	if err != nil {
		return "", fmt.Errorf("wslpath -w %s: %w", p, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// Run starts DOSBox Pure on content and waits for it to exit. Standalone
// DOSBox Pure uses the DOSBoxPure.cfg in its working directory if there is
// one (dosbox-pure-unleashed main.cpp, sDOSBoxPure::Load), and takes its
// system and save directories from that file's path_system/path_saves. So
// it runs in its own directory, using its own configuration, unless
// Discover was given a directory: then it runs in workDir, with a copy of
// that configuration naming both directories.
func (e Env) Run(content, workDir string) error {
	arg, err := e.exePath(content)
	if err != nil {
		return err
	}
	cmd := exec.Command(e.Exe, arg)
	cmd.Dir = filepath.Dir(e.Exe)
	if e.settings != nil {
		if err := e.writeSettings(workDir); err != nil {
			return err
		}
		cmd.Dir = workDir
	}
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("DOSBox Pure: %w", err)
	}
	return nil
}

// writeSettings writes the DOSBoxPure.cfg Run uses in dir: DOSBox Pure's
// own settings, with the system and save directories Discover settled on.
func (e Env) writeSettings(dir string) error {
	cfg := make(map[string]any, len(e.settings)+2)
	for k, v := range e.settings {
		cfg[k] = v
	}
	for key, p := range map[string]string{"path_system": e.SystemDir, "path_saves": e.SaveDir} {
		abs, err := filepath.Abs(p)
		if err != nil {
			return err
		}
		if cfg[key], err = e.exePath(abs); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(cfg, "", "\t")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "DOSBoxPure.cfg"), data, 0o644)
}
