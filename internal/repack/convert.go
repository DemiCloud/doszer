package repack

import (
	"archive/zip"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DemiCloud/doszer/internal/build"
	"github.com/DemiCloud/doszer/internal/chain"
	"github.com/DemiCloud/doszer/internal/manifest"
	"github.com/pelletier/go-toml/v2"
)

// Extensions: a game, and a parent-only archive (spec §5.3).
const (
	ExtGame   = ".dosz"
	ExtParent = ".dosp"
)

// ConvertResult is what Convert did.
type ConvertResult struct {
	From, To string   // the archive's path before and after
	Children []string // the archives beside it re-pointed at its new name
	Removed  []string // files left out: a parent's WINDOWS.BAT
}

// Convert renames an archive to ext - ExtParent for one that only holds
// files games share, ExtGame back - and re-points the archives beside it
// that have it as their parent (their parent markers name its file:
// Set, retrofitting a manifest on an archive without one). A child that
// can't be changed stops the conversion before the rename.
func Convert(path, ext string, dryRun bool, prov *build.Provenance) (*ConvertResult, error) {
	if ext != ExtGame && ext != ExtParent {
		return nil, fmt.Errorf("convert to %s: want %s or %s", ext, ExtGame, ExtParent)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	to := strings.TrimSuffix(abs, filepath.Ext(abs)) + ext
	res := &ConvertResult{From: abs, To: to}
	if strings.EqualFold(to, abs) {
		return res, nil
	}
	if _, err := os.Stat(to); err == nil {
		return nil, fmt.Errorf("%s exists", to)
	}
	oldName, newName := filepath.Base(abs), filepath.Base(to)
	// Its direct children only: a grandchild names its own parent.
	for _, c := range chain.UsedBy(filepath.Dir(abs), abs) {
		links := chain.Resolve(c).Links
		if len(links) > 1 && strings.EqualFold(links[1].Path, abs) {
			res.Children = append(res.Children, c)
		}
	}
	for _, c := range res.Children {
		if _, err := Set(SetOptions{Path: c, Parent: &Change{From: oldName, HasFrom: true, To: newName}, Retrofit: true, DryRun: true}); err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(c), err)
		}
	}
	// A parent's WINDOWS.BAT would start its program in every child
	// without one of its own (DOSBox Pure runs the first of WINDOWS.BAT,
	// AUTOEXEC.BAT and WIN.COM it finds in the content: dosbox_pure_run.h,
	// RunShell): a parent leaves starting to its children.
	bat := ext == ExtParent && hasEntry(abs, "WINDOWS.BAT")
	if bat {
		res.Removed = []string{"WINDOWS.BAT"}
	}
	if dryRun {
		return res, nil
	}
	if err := os.Rename(abs, to); err != nil {
		return nil, err
	}
	if bat {
		if err := removeBatch(to, prov); err != nil {
			return res, err
		}
	}
	for _, c := range res.Children {
		if _, err := Set(SetOptions{Path: c, Parent: &Change{From: oldName, HasFrom: true, To: newName}, Retrofit: true, Provenance: prov}); err != nil {
			return res, fmt.Errorf("%s: %w", filepath.Base(c), err)
		}
	}
	return res, nil
}

// hasEntry reports whether the archive at path has the file name (a
// root entry, case-insensitively).
func hasEntry(path, name string) bool {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return false
	}
	defer zr.Close()
	for _, f := range zr.File {
		if strings.EqualFold(f.Name, name) {
			return true
		}
	}
	return false
}

// removeBatch repacks the archive at path in place without WINDOWS.BAT,
// from its manifest without a [[file]] for it (a retrofitted one for an
// archive built without doszer).
func removeBatch(path string, prov *build.Provenance) error {
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	var m *manifest.Manifest
	data, _, err := EmbeddedManifest(path)
	switch {
	case errors.Is(err, ErrNoManifest):
		m = Retrofit(path, name)
	case err != nil:
		return err
	default:
		if m, err = manifest.Parse(data, name); err != nil {
			return err
		}
	}
	var files []manifest.FileSpec
	for _, f := range m.File {
		if !strings.EqualFold(f.Path, "WINDOWS.BAT") {
			files = append(files, f)
		}
	}
	m.File = files
	out, err := toml.Marshal(m)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp("", "doszer-manifest-*.toml")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()
	_, err = Run(Options{InputPath: path, OutputPath: path, ManifestPath: tmp.Name(), Remove: []string{"WINDOWS.BAT"}, Provenance: prov})
	return err
}
