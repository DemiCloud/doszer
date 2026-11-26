package start

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/DemiCloud/doszer/internal/build"
	"github.com/DemiCloud/doszer/internal/chain"
	"github.com/DemiCloud/doszer/internal/icon"
	"github.com/DemiCloud/doszer/internal/manifest"
	"github.com/DemiCloud/doszer/internal/platform"
)

// ParentCandidates lists the programs in a parent archive a child could
// start, helpers aside: for a parent booting DOS, DOS programs; booting a
// shell or an OS image, Windows programs too. Windows programs first,
// then by depth and path.
func ParentCandidates(parent string) ([]Candidate, error) {
	zr, err := zip.OpenReader(parent)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	c := chain.Resolve(parent)
	var out []Candidate
	for _, f := range zr.File {
		if !platform.IsProgramName(f.Name) || platform.IsHelper(f.Name) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		t := platform.Classify(f.Name, rc)
		rc.Close()
		switch t.Platform {
		case platform.DOS:
		case platform.Win16, platform.Win32:
			if c.Kind == chain.KindDOS {
				continue
			}
		default:
			continue
		}
		out = append(out, Candidate{Path: f.Name, Target: t.String()})
	}
	win := func(c Candidate) bool { return strings.HasPrefix(c.Target, "Windows") }
	sort.SliceStable(out, func(i, j int) bool {
		if win(out[i]) != win(out[j]) {
			return win(out[i])
		}
		di, dj := strings.Count(out[i].Path, "/"), strings.Count(out[j].Path, "/")
		if di != dj {
			return di < dj
		}
		return out[i].Path < out[j].Path
	})
	return out, nil
}

// ChildOptions are Child's choices.
type ChildOptions struct {
	Name   string // the child's name; default: the program's file name
	Output string // the archive; default: <name>.dosz beside the parent (DOSBox Pure looks for a parent beside its child)
	Force  bool   // replace an existing output
	DryRun bool

	Provenance *build.Provenance
}

// ChildResult is what Child made.
type ChildResult struct {
	Output   string
	Manifest *manifest.Manifest
}

// Child builds a game over parent that starts program (a path in the
// parent): a manifest naming the parent (§5.3) and booting what the
// parent boots, with a WINDOWS.BAT starting the program (Batch) under a
// shell or an OS image, or boot.exec under DOS; and the program's icon,
// as an ICO file named DOSZ.ICO, which §6.8.4 keeps unchanged.
func Child(parent, program string, o ChildOptions) (*ChildResult, error) {
	zr, err := zip.OpenReader(parent)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	program = strings.Trim(strings.ReplaceAll(program, `\`, "/"), "/")
	if len(program) > 2 && strings.EqualFold(program[:2], "C:") {
		program = strings.TrimPrefix(program[2:], "/")
	}
	var prog *zip.File
	exists := map[string]bool{}
	for _, f := range zr.File {
		exists[strings.ToUpper(f.Name)] = true
		if strings.EqualFold(f.Name, program) {
			prog, program = f, f.Name
		}
	}
	if prog == nil {
		return nil, fmt.Errorf("%s: no %s in it", filepath.Base(parent), program)
	}

	name := o.Name
	if name == "" {
		name = strings.TrimSuffix(path.Base(program), path.Ext(program))
	}
	m := &manifest.Manifest{Format: manifest.FormatVersion, Name: name}
	m.Boot.Parent = filepath.Base(parent)
	c := chain.Resolve(parent)
	var bat string
	switch c.Kind {
	case chain.KindShell, chain.KindOS:
		model := manifest.ModelShell
		if c.Kind == chain.KindOS {
			model, m.Boot.OS = manifest.ModelOS, c.Boot
		} else {
			m.Boot.Shell = c.Boot
		}
		reg := FindRegistry(program, func(p string) bool { return exists[strings.ToUpper(p)] })
		if bat, err = Batch(model, program, reg, ""); err != nil {
			return nil, err
		}
		m.File = []manifest.FileSpec{{Path: "WINDOWS.BAT", Text: &bat}}
	default:
		dos, ok := platform.DOSPath(program)
		if !ok {
			return nil, fmt.Errorf("%s: not a DOS path (each name at most 8.3 characters, no spaces)", program)
		}
		m.Boot.Exec = dos
	}

	var ico []byte
	if data, err := readFile(prog); err == nil {
		if ico, err = icon.Make(bytes.NewReader(data), int64(len(data)), 0); err == nil {
			m.Icon = &manifest.Icon{Path: build.IconName}
		}
	}

	out := o.Output
	if out == "" {
		out = filepath.Join(filepath.Dir(parent), safeFileName(name)+".dosz")
	}
	res := &ChildResult{Output: out, Manifest: m}
	if _, err := os.Stat(out); err == nil && !o.Force {
		return nil, fmt.Errorf("%s exists (-f replaces it)", out)
	}
	if o.DryRun {
		return res, nil
	}

	src, err := os.MkdirTemp("", "doszer-child-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(src)
	data, err := toml.Marshal(m)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(src, manifest.FileName), data, 0o644); err != nil {
		return nil, err
	}
	if ico != nil {
		if err := os.WriteFile(filepath.Join(src, build.IconName), ico, 0o644); err != nil {
			return nil, err
		}
	}
	if _, err := build.Run(build.Options{SourceDir: src, OutputPath: out, Provenance: o.Provenance}); err != nil {
		return nil, err
	}
	return res, nil
}

func readFile(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// safeFileName is name without the characters Windows refuses in a file
// name.
func safeFileName(name string) string {
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune(`<>:"/\|?*`, r) || r < 32 {
			return -1
		}
		return r
	}, name)
}
