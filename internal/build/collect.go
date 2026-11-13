package build

import (
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"strings"

	"github.com/DemiCloud/doszer/internal/manifest"
)

// collect performs §6.1: the source directory's files, excluding
// DOSZ.TOML, then every [[extract]] entry, then every [[file]] entry.
//
// Media conversion (§6.4 - disc images already sitting in the build,
// independent of [[extract]]) is not implemented yet.
func collect(sourceDir string, m *manifest.Manifest) (*fileSet, error) {
	result := newFileSet()

	err := filepath.WalkDir(sourceDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(sourceDir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if strings.EqualFold(rel, manifest.FileName) {
			return nil
		}

		abs, err := filepath.Abs(p)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		result.set(rel, diskEntry(abs, info.ModTime()))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("collect: %w", err)
	}

	if len(m.Source.Media) > 0 {
		media, err := locateMedia(sourceDir, m.Source.Media)
		if err != nil {
			return nil, err
		}
		if err := applyExtracts(result, media, m.Extract); err != nil {
			return nil, err
		}
	}

	for _, f := range m.File {
		result.set(f.Path, dataEntry([]byte(*f.Text), Epoch))
	}

	return result, nil
}

// trim removes files matching any pattern (§6.2), warning per §11 on a
// pattern that matched nothing.
func trim(fs *fileSet, patterns []string) (removed []string, warnings []string) {
	for _, pattern := range patterns {
		matched := false
		for _, p := range fs.paths() {
			if matchGlob(pattern, p) {
				fs.delete(p)
				removed = append(removed, p)
				matched = true
			}
		}
		if !matched {
			warnings = append(warnings, fmt.Sprintf("layout.trim: pattern %q matched nothing", pattern))
		}
	}

	return removed, warnings
}

// exclusionPatterns are the base-name globs excluded by §6.7.
var exclusionPatterns = []string{
	"desktop.ini", "Thumbs.db", "ehthumbs.db", ".DS_Store", "._*", "*:Zone.Identifier",
}

// exclude removes files matching §6.7.
func exclude(fs *fileSet) {
	for _, p := range fs.paths() {
		if IsExcluded(p) {
			fs.delete(p)
		}
	}
}

// IsExcluded reports whether a path matches one of §6.7's exclusion
// patterns.
func IsExcluded(p string) bool {
	return matchAny(exclusionPatterns, path.Base(p))
}
