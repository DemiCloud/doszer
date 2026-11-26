// Package start implements `doszer start`: which program a source's game
// starts with. Under the plain model that is boot.exec (spec §5.3); under
// the shell and OS image models, a WINDOWS.BAT in the source's root,
// which the shell (README "Preparing a shell") or the OS image (spec §12)
// runs at startup.
package start

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/DemiCloud/doszer/internal/manifest"
	"github.com/DemiCloud/doszer/internal/platform"
)

// Candidate is a program the game could start with.
type Candidate struct {
	Path    string `json:"path" yaml:"path" toml:"path"` // relative to the source, slash-separated
	Target  string `json:"target" yaml:"target" toml:"target"`
	Runs    string `json:"runs,omitempty" yaml:"runs,omitempty" toml:"runs,omitempty"` // a batch file: the program it starts
	Likely  bool   `json:"likely,omitempty" yaml:"likely,omitempty" toml:"likely,omitempty"`
	Current bool   `json:"current,omitempty" yaml:"current,omitempty" toml:"current,omitempty"`
}

// Candidates lists the programs installed in source (not on its discs),
// helpers (setup, uninstall, readme viewers...) aside, the likeliest first:
// for the plain model, DOS programs, each by the batch file that starts
// it if the game has one (platform.Launcher); for the shell and OS image
// models, Windows programs too.
func Candidates(source string) ([]Candidate, *manifest.Manifest, error) {
	m, err := manifest.Load(source)
	if err != nil {
		return nil, nil, err
	}
	progs, err := platform.Scan(source)
	if err != nil {
		return nil, m, err
	}
	model := m.Model()
	var keep []platform.Program
	for _, p := range progs {
		if strings.Contains(p.Path, ":") || platform.IsHelper(p.Path) {
			continue
		}
		switch p.Target.Platform {
		case platform.DOS:
		case platform.Win16, platform.Win32:
			if model == manifest.ModelPlain {
				continue
			}
		default:
			continue
		}
		keep = append(keep, p)
	}
	likely := platform.Likely(keep, m.Name)
	cur := current(source, m)
	var out []Candidate
	for _, p := range keep {
		c := Candidate{Path: p.Path, Target: p.Target.String(), Likely: likely != nil && likely.Path == p.Path}
		if p.Target.Platform == platform.DOS {
			if b := platform.Launcher(source, p.Path); b != "" {
				c.Runs, c.Path = c.Path, b
			}
		}
		c.Current = cur != "" && strings.EqualFold(c.Path, cur)
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Likely != out[j].Likely {
			return out[i].Likely
		}
		di, dj := strings.Count(out[i].Path, "/"), strings.Count(out[j].Path, "/")
		if di != dj {
			return di < dj
		}
		return out[i].Path < out[j].Path
	})
	return out, m, nil
}

// current is the program the game starts with now, as a source path, or
// "": boot.exec's, or the one WINDOWS.BAT's last START or WIN line names.
func current(source string, m *manifest.Manifest) string {
	if m.Model() == manifest.ModelPlain {
		if m.Boot.Exec == "" {
			return ""
		}
		p, _, _ := strings.Cut(m.Boot.Exec, " ")
		p = strings.TrimPrefix(strings.TrimPrefix(p, `C:\`), `c:\`)
		return strings.ReplaceAll(p, `\`, "/")
	}
	data, err := os.ReadFile(filepath.Join(source, "WINDOWS.BAT"))
	if err != nil {
		return ""
	}
	unquote := func(s string) string { return strings.Trim(s, `"`) }
	rel := func(p string) string { // C:\X\Y or D:\X\Y as X/Y
		if len(p) > 3 && p[1] == ':' && p[2] == '\\' {
			return strings.ReplaceAll(p[3:], `\`, "/")
		}
		return ""
	}
	cd, cur := "", ""
	for _, l := range strings.Split(strings.ReplaceAll(string(data), "\r", ""), "\n") {
		l = strings.TrimSpace(l)
		up := strings.ToUpper(l)
		switch {
		case strings.HasPrefix(up, "CD "):
			cd = strings.Trim(unquote(strings.TrimSpace(l[3:])), `\`)
		case strings.HasPrefix(up, "START /W "):
			f := unquote(strings.TrimSpace(l[len("START /W "):]))
			if r := rel(f); r != "" {
				cur = r
			} else if strings.HasSuffix(strings.ToUpper(f), ".EXE") {
				cur = strings.Trim(path.Join(strings.ReplaceAll(cd, `\`, "/"), f), "/")
			}
		case strings.HasPrefix(up, "WIN "):
			f := strings.Fields(l)
			if r := rel(f[len(f)-1]); r != "" {
				cur = r
			}
		}
	}
	return cur
}

// Result is what Set changed.
type Result struct {
	Model   string `json:"model" yaml:"model" toml:"model"`
	Exec    string `json:"exec,omitempty" yaml:"exec,omitempty" toml:"exec,omitempty"`                      // boot.exec, plain model
	Batch   string `json:"windows_bat,omitempty" yaml:"windows_bat,omitempty" toml:"windows_bat,omitempty"` // WINDOWS.BAT's text, shell and OS image models
	Registy string `json:"registry,omitempty" yaml:"registry,omitempty" toml:"registry,omitempty"`          // the INSTALL.REG WINDOWS.BAT restores
}

// Set makes the game start with program (relative to source,
// slash-separated): boot.exec under the plain model; under the shell and
// OS image models, a WINDOWS.BAT in source's root - one already there is
// replaced only with force. An INSTALL.REG beside the program (install's
// registry settings) is restored at startup under the OS image model.
func Set(source, program string, force, dryRun bool) (*Result, error) {
	m, err := manifest.Load(source)
	if err != nil {
		return nil, err
	}
	program = strings.Trim(filepath.ToSlash(program), "/")
	if fi, err := os.Stat(filepath.Join(source, filepath.FromSlash(program))); err != nil || fi.IsDir() {
		return nil, fmt.Errorf("%s: no such program in %s", program, source)
	}
	dos, ok := platform.DOSPath(program)
	if !ok && m.Model() != manifest.ModelOS { // Windows 9x takes long names (quoted below)
		return nil, fmt.Errorf("%s: not a DOS path (each name at most 8.3 characters, no spaces)", program)
	}
	res := &Result{Model: m.Model().String()}
	if m.Model() == manifest.ModelPlain {
		res.Exec = dos
		if dryRun {
			return res, nil
		}
		return res, manifest.SetExecFile(source, dos)
	}

	bat := filepath.Join(source, "WINDOWS.BAT")
	if _, err := os.Stat(bat); err == nil && !force {
		return nil, errors.New("WINDOWS.BAT exists: --force replaces it")
	}
	dir, file := path.Dir(program), path.Base(program)
	var b strings.Builder
	b.WriteString("@ECHO OFF\r\n")
	switch m.Model() {
	case manifest.ModelShell:
		// Run instead of the shell's AUTOEXEC.BAT, so it sets PATH and
		// TEMP itself; RunExit (a prepared shell) closes Windows, and so
		// DOSBox Pure, when the game quits.
		b.WriteString("PATH C:\\WINDOWS\r\nSET TEMP=C:\\WINDOWS\\TEMP\r\n")
		b.WriteString("IF NOT EXIST C:\\RUNEXIT\\RUNEXIT.EXE GOTO PLAIN\r\n")
		b.WriteString("WIN C:\\RUNEXIT\\RUNEXIT.EXE /show=3 " + dos + "\r\nGOTO END\r\n")
		b.WriteString(":PLAIN\r\nWIN " + dos + "\r\n:END\r\n")
	case manifest.ModelOS:
		// The archive is D:; the image shuts Windows down when this ends
		// (spec §12), so START /W waits for the game.
		win := func(rel string) string { // a D: path, quoted if it needs it
			p := `D:\` + strings.ReplaceAll(rel, "/", `\`)
			if strings.ContainsAny(p, " +,;=[]") {
				p = `"` + p + `"`
			}
			return p
		}
		quote := func(s string) string {
			if strings.ContainsAny(s, " +,;=[]") {
				return `"` + s + `"`
			}
			return s
		}
		reg := path.Join(dir, "INSTALL.REG")
		if _, err := os.Stat(filepath.Join(source, filepath.FromSlash(reg))); err == nil {
			res.Registy = win(reg)
			b.WriteString("REGEDIT /S " + res.Registy + "\r\n")
		}
		b.WriteString("D:\r\n")
		if dir != "." {
			b.WriteString("CD " + quote(`\`+strings.ReplaceAll(dir, "/", `\`)) + "\r\n")
		}
		b.WriteString("START /W " + quote(file) + "\r\n")
	}
	res.Batch = b.String()
	if dryRun {
		return res, nil
	}
	return res, os.WriteFile(bat, []byte(res.Batch), 0o644)
}
