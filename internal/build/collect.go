package build

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/DemiCloud/doszer/internal/manifest"
	"github.com/DemiCloud/doszer/internal/status"
)

// collect performs §6.1: the source directory's files, excluding
// DOSZ.TOML, then every [[extract]] entry, then every [[file]] entry. It
// verifies [[source.media]] against the collected disc images (§7.3) and
// returns that step's warnings.
//
// Files extracted from media stay in temporary images created in
// outputPath's directory and appended to *tempFiles, which the caller
// removes once the archive is written. outputPath itself is never
// collected: an earlier build's output in the source would otherwise be
// packed into the next one.
//
// Two source files whose paths differ only in case (possible on a
// case-sensitive file system) are one file to §2: the later in walk
// order, which is lexical, is kept, and pathWarnings names both.
func collect(sourceDir string, m *manifest.Manifest, outputPath string, tempFiles *[]*os.File) (_ *fileSet, pathWarnings, mediaWarnings []string, err error) {
	status.Step("Collecting files")
	result := newFileSet()
	tempDir := filepath.Dir(outputPath)
	output, _ := os.Stat(outputPath) // nil if not built yet

	err = filepath.WalkDir(sourceDir, func(p string, d fs.DirEntry, err error) error {
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
		if output != nil && os.SameFile(info, output) {
			return nil
		}
		if w := result.collected(rel, diskEntry(abs, info.ModTime())); w != "" {
			pathWarnings = append(pathWarnings, w)
		}
		return nil
	})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("collect: %w", err)
	}

	media, mediaWarnings, err := locateMedia(result, m.Source.Media, mediaUses(m), &teeTarget{dir: tempDir, files: tempFiles})
	if err != nil {
		return nil, pathWarnings, mediaWarnings, err
	}
	if err := applyExtracts(result, media, m.Extract, tempDir, tempFiles); err != nil {
		return nil, pathWarnings, mediaWarnings, err
	}
	if m.Icon != nil && m.Icon.Media != "" {
		if result.mediaIcon, err = iconFromMedia(media, m.Icon, tempDir, tempFiles); err != nil {
			return nil, pathWarnings, mediaWarnings, err
		}
	}

	for _, f := range m.File {
		result.set(f.Path, dataEntry([]byte(*f.Text), Epoch))
	}

	return result, pathWarnings, mediaWarnings, nil
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
