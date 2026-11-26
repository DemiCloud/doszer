package repack

import (
	"archive/zip"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/DemiCloud/doszer/internal/build"
	"github.com/DemiCloud/doszer/internal/chain"
	"github.com/DemiCloud/doszer/internal/manifest"
	"github.com/DemiCloud/doszer/internal/zipw"
)

// Change replaces a boot setting: with From, only where the setting is
// From now (exactly, as DOSBox Pure matches names).
type Change struct {
	From    string
	HasFrom bool
	To      string
}

// ParseChange reads "NEW" or "OLD:NEW". A colon can't be part of a name:
// images, shells and parents are file names, and no file name on Windows
// contains one.
func ParseChange(s string) Change {
	if from, to, ok := strings.Cut(s, ":"); ok {
		return Change{From: from, HasFrom: true, To: to}
	}
	return Change{To: s}
}

// SetOptions configures Set.
type SetOptions struct {
	Path   string
	Parent *Change // "" To removes the parent
	OS     *Change // applies only to an archive that boots an OS image
	Shell  *Change // applies only to an archive that boots a shell
	DryRun bool
	// Retrofit gives an archive with no embedded manifest (one not built
	// by doszer) a minimal one (Retrofit) to change, instead of refusing
	// it.
	Retrofit bool

	Provenance *build.Provenance
}

// SetResult reports what Set did to one archive.
type SetResult struct {
	Before, After manifest.Boot
	Skipped       []string // why a requested change didn't apply
}

// Changed reports whether the archive was (or would be) rewritten.
func (r *SetResult) Changed() bool { return r.Before != r.After }

// Set changes an archive's boot settings in place: the embedded manifest,
// and the runtime files that follow from it (build.Refresh). Every other
// entry is copied through unchanged, so this is quick whatever the
// archive's size. The archive must have an embedded manifest.
func Set(opts SetOptions) (*SetResult, error) {
	r, err := zip.OpenReader(opts.Path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", opts.Path, err)
	}
	var closeOnce sync.Once
	closeInput := func() { closeOnce.Do(func() { r.Close() }) }
	defer closeInput()

	var mf *zip.File
	entries := make(map[string]build.Entry, len(r.File))
	for _, f := range r.File {
		if strings.HasSuffix(f.Name, "/") || strings.HasSuffix(f.Name, `\`) {
			continue
		}
		name, err := entryPath(f.Name)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", opts.Path, err)
		}
		if strings.EqualFold(name, manifest.FileName) {
			mf = f
			continue
		}
		entries[name] = build.Entry{Source: rawSource(f), Modified: zipw.DOSModified(&f.FileHeader)}
	}
	defaultName := strings.TrimSuffix(filepath.Base(opts.Path), filepath.Ext(opts.Path))
	var m *manifest.Manifest
	switch {
	case mf == nil && opts.Retrofit:
		m = Retrofit(opts.Path, defaultName)
	case mf == nil:
		return nil, fmt.Errorf("%s: no embedded %s (repack --manifest adds one)", opts.Path, manifest.FileName)
	default:
		data, err := readEntry(mf)
		if err != nil {
			return nil, fmt.Errorf("%s: %s: %w", opts.Path, manifest.FileName, err)
		}
		if m, err = manifest.Parse(data, defaultName); err != nil {
			return nil, fmt.Errorf("%s's embedded %s is invalid: %w", opts.Path, manifest.FileName, err)
		}
	}

	res := &SetResult{Before: m.Boot}
	apply := func(c *Change, field *string, what string, applies bool, why string) {
		switch {
		case c == nil:
		case !applies:
			res.Skipped = append(res.Skipped, why)
		case c.HasFrom && *field != c.From:
			cur := *field
			if cur == "" {
				cur = "none"
			}
			res.Skipped = append(res.Skipped, fmt.Sprintf("%s is %s, not %s", what, cur, c.From))
		default:
			*field = c.To
		}
	}
	apply(opts.OS, &m.Boot.OS, "OS image", m.Boot.OS != "", "doesn't boot an OS image")
	apply(opts.Shell, &m.Boot.Shell, "shell", m.Boot.Shell != "", "doesn't boot a shell")
	apply(opts.Parent, &m.Boot.Parent, "parent", true, "")
	res.After = m.Boot
	if !res.Changed() {
		return res, nil
	}
	if err := m.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", opts.Path, err)
	}
	if opts.DryRun {
		return res, nil
	}
	_, err = build.Refresh(entries, m, build.Options{
		OutputPath:   opts.Path,
		Provenance:   opts.Provenance,
		BeforeRename: closeInput,
	})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", opts.Path, err)
	}
	return res, nil
}

// Retrofit is a minimal manifest for an archive built without doszer:
// its name, its parent marker's parent, and what its own AUTOBOOT.DBP
// boots (line 1: S*shell, O*image or a program; dosbox_pure_run.h,
// ReadAutoBoot), so rebuilding it keeps how it starts.
func Retrofit(path, name string) *manifest.Manifest {
	m := &manifest.Manifest{Format: manifest.FormatVersion, Name: name}
	links := chain.Resolve(path).Links
	if len(links) > 1 {
		m.Boot.Parent = links[1].Name
	}
	if l := links[0]; l.Has {
		switch line := l.Autoboot; {
		case strings.HasPrefix(line, "S*"):
			m.Boot.Shell = line[2:]
		case strings.HasPrefix(line, "O*"):
			m.Boot.OS = line[2:]
		default:
			m.Boot.Exec = line
		}
	}
	return m
}
