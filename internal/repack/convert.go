package repack

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DemiCloud/doszer/internal/build"
	"github.com/DemiCloud/doszer/internal/chain"
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
	if dryRun {
		return res, nil
	}
	if err := os.Rename(abs, to); err != nil {
		return nil, err
	}
	for _, c := range res.Children {
		if _, err := Set(SetOptions{Path: c, Parent: &Change{From: oldName, HasFrom: true, To: newName}, Retrofit: true, Provenance: prov}); err != nil {
			return res, fmt.Errorf("%s: %w", filepath.Base(c), err)
		}
	}
	return res, nil
}
