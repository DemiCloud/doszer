package build

import (
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"strings"

	"github.com/DemiCloud/doszer/internal/manifest"
)

// collect performs §6.1. [[extract]] needs media processing (§6.4), not yet
// implemented, so a manifest using it is rejected.
func collect(sourceDir string, m *manifest.Manifest) (*fileSet, error) {
	if len(m.Extract) > 0 {
		return nil, fmt.Errorf("build: [[extract]] is not implemented yet (requires media processing)")
	}

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
		result.set(rel, diskEntry(abs))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("collect: %w", err)
	}

	for _, f := range m.File {
		result.set(f.Path, dataEntry([]byte(*f.Text)))
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
		if matchAny(exclusionPatterns, path.Base(p)) {
			fs.delete(p)
		}
	}
}
