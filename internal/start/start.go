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
	"regexp"
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
	likely := Pick(keep, m.Name)
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
	p, _ := ParseBatch(string(data))
	return p
}

// ParseBatch reads a WINDOWS.BAT's program, as a source path, and the
// display mode it switches to (QuickRes), if any: what Batch wrote, or
// a hand-written one in the same forms (START /W, WIN).
func ParseBatch(text string) (program, display string) {
	unquote := func(s string) string { return strings.Trim(s, `"`) }
	rel := func(p string) string { // C:\X\Y or D:\X\Y as X/Y
		if len(p) > 3 && p[1] == ':' && p[2] == '\\' {
			return strings.ReplaceAll(p[3:], `\`, "/")
		}
		return ""
	}
	cd := ""
	for _, l := range strings.Split(strings.ReplaceAll(text, "\r", ""), "\n") {
		l = strings.TrimSpace(l)
		up := strings.ToUpper(l)
		switch {
		case strings.Contains(up, "QUICKRES_RUNDLLENTRY"):
			f := strings.Fields(l)
			display = f[len(f)-1]
		case strings.HasPrefix(up, "CD "):
			cd = strings.Trim(unquote(strings.TrimSpace(l[3:])), `\`)
		case strings.HasPrefix(up, "START /W "):
			f := unquote(strings.TrimSpace(l[len("START /W "):]))
			if r := rel(f); r != "" {
				program = r
			} else if strings.HasSuffix(strings.ToUpper(f), ".EXE") {
				program = strings.Trim(path.Join(strings.ReplaceAll(cd, `\`, "/"), f), "/")
			}
		case strings.HasPrefix(up, "WIN "):
			f := strings.Fields(l)
			if r := rel(f[len(f)-1]); r != "" {
				program = r
			}
		}
	}
	return program, display
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
// Options say how Set writes the start.
type Options struct {
	Force   bool   // replace an existing WINDOWS.BAT
	DryRun  bool   // report without writing
	Display string // OS image model: a display mode to switch to first, WxHxBPP (640x480x8: 256 colours)
}

// displayMode is a QuickRes mode: width x height x bits per pixel.
var displayMode = regexp.MustCompile(`^[0-9]{3,4}x[0-9]{3,4}x(4|8|15|16|24|32)$`)

func Set(source, program string, o Options) (*Result, error) {
	force, dryRun := o.Force, o.DryRun
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
	if o.Display != "" {
		if m.Model() != manifest.ModelOS {
			return nil, errors.New("--display: only under the OS image model (Windows 9x)")
		}
		if !displayMode.MatchString(o.Display) {
			return nil, fmt.Errorf("--display %s: want WIDTHxHEIGHTxBITS, e.g. 640x480x8", o.Display)
		}
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
	// INSTALL.REG: install writes it at the extracted folder's root, the
	// program's folder or one above it (SIERRA/INSTALL.REG for
	// SIERRA/ANTARA/ANTARAR.EXE).
	reg := FindRegistry(program, func(p string) bool {
		_, err := os.Stat(filepath.Join(source, filepath.FromSlash(p)))
		return err == nil
	})
	batch, err := Batch(m.Model(), program, reg, o.Display)
	if err != nil {
		return nil, err
	}
	if reg != "" {
		res.Registy = winPath(reg)
	}
	res.Batch = batch
	if dryRun {
		return res, nil
	}
	return res, os.WriteFile(bat, []byte(res.Batch), 0o644)
}

// FindRegistry is the INSTALL.REG for program (both source paths): in
// its folder or one above it, as exists reports; "" if none.
func FindRegistry(program string, exists func(string) bool) string {
	for d := path.Dir(program); ; d = path.Dir(d) {
		reg := strings.TrimPrefix(path.Join(d, "INSTALL.REG"), "./")
		if exists(reg) {
			return reg
		}
		if d == "." || d == "/" {
			return ""
		}
	}
}

// winPath is a source path as a D: path, quoted if it needs it.
func winPath(rel string) string {
	p := `D:\` + strings.ReplaceAll(rel, "/", `\`)
	if strings.ContainsAny(p, " +,;=[]") {
		p = `"` + p + `"`
	}
	return p
}

// Batch is the WINDOWS.BAT that starts program (a source path) under the
// shell or OS image model: restoring registry (an INSTALL.REG source
// path, or ""), switching to display first (WxHxBPP, or "").
func Batch(model manifest.Model, program, registry, display string) (string, error) {
	dir, file := path.Dir(program), path.Base(program)
	var b strings.Builder
	b.WriteString("@ECHO OFF\r\n")
	switch model {
	case manifest.ModelShell:
		dos, ok := platform.DOSPath(program)
		if !ok {
			return "", fmt.Errorf("%s: not a DOS path (each name at most 8.3 characters, no spaces)", program)
		}
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
		quote := func(s string) string {
			if strings.ContainsAny(s, " +,;=[]") {
				return `"` + s + `"`
			}
			return s
		}
		if registry != "" {
			b.WriteString("REGEDIT /S " + winPath(registry) + "\r\n")
		}
		if display != "" {
			if !displayMode.MatchString(display) {
				return "", fmt.Errorf("display %s: want WIDTHxHEIGHTxBITS, e.g. 640x480x8", display)
			}
			// Windows 98's own mode switch (QuickRes, in DESKCP16.DLL, a
			// 16-bit DLL: RUNDLL, not RUNDLL32), with no restart prompt:
			// for a game that wants 256 colours and warns at more.
			b.WriteString("START /W RUNDLL DESKCP16.DLL,QUICKRES_RUNDLLENTRY " + display + "\r\n")
		}
		b.WriteString("D:\r\n")
		if dir != "." {
			b.WriteString("CD " + quote(`\`+strings.ReplaceAll(dir, "/", `\`)) + "\r\n")
		}
		b.WriteString("START /W " + quote(file) + "\r\n")
	default:
		return "", errors.New("WINDOWS.BAT is for the shell and OS image models")
	}
	return b.String(), nil
}

// Pick is the program a game most likely starts with, of progs (its
// programs, helpers and disc programs aside): the only one, else
// likeliest's, else nil. init, install and the start list all use it.
func Pick(progs []platform.Program, name string) *platform.Program {
	if len(progs) == 1 {
		return &progs[0]
	}
	return likeliest(progs, name)
}

// Exec is the program a plain DOS game in source starts with, as the
// source path boot.exec would name (the batch file that runs it, if the
// game has one: platform.Launcher), or "": Pick of its DOS programs.
func Exec(source string, progs []platform.Program, name string) string {
	p := Pick(platform.Starters(progs), name)
	if p == nil {
		return ""
	}
	if b := platform.Launcher(source, p.Path); b != "" {
		return b
	}
	return p.Path
}

// likeliest is the program the game most likely starts with: platform.Likely's
// (named after its folder or the game), else the one program whose name
// begins with its folder's or the game's last word (ANTARAR.EXE in
// SIERRA/ANTARA, of Betrayal in Antara), else nil.
func likeliest(progs []platform.Program, name string) *platform.Program {
	if p := platform.Likely(progs, name); p != nil {
		return p
	}
	norm := func(s string) string {
		var b strings.Builder
		for _, r := range strings.ToLower(s) {
			if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
				b.WriteRune(r)
			}
		}
		return b.String()
	}
	words := strings.Fields(name)
	last := ""
	if len(words) > 0 {
		last = norm(words[len(words)-1])
	}
	var hit *platform.Program
	for i, p := range progs {
		base := norm(strings.TrimSuffix(path.Base(p.Path), path.Ext(p.Path)))
		dir := norm(path.Base(path.Dir(p.Path)))
		match := func(stem string) bool { return len(stem) >= 4 && strings.HasPrefix(base, stem) }
		if match(dir) || match(last) {
			if hit != nil {
				return nil
			}
			hit = &progs[i]
		}
	}
	return hit
}
