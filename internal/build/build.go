// Package build implements `doszer build`: spec §6's processing model,
// producing a deterministic archive per §8. [[extract]] (§5.7) and media
// identification (§7) are implemented; converting whole disc images already
// sitting in the build (§6.4) is not, yet.
package build

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"github.com/DemiCloud/doszer/internal/manifest"
	"github.com/DemiCloud/doszer/internal/zipw"
)

// Options configures a build.
type Options struct {
	SourceDir  string // Directory containing DOSZ.TOML.
	OutputPath string // Archive path to write; defaults to "<effective name>.dosz" in the working directory.
	Name       string // Overrides the manifest's effective name, if non-empty.
	DryRun     bool   // Compute and report the plan without writing an archive.
	Strict     bool   // Fail the build if any §11 warning condition occurs.
}

// Result reports what a build did or would do.
type Result struct {
	Manifest      *manifest.Manifest
	EffectiveName string

	Trimmed      []string // Paths removed by layout.trim.
	TrimWarnings []string // layout.trim patterns that matched nothing.

	Moved []Move // Paths renamed by flatten, oldest first.

	RewriteHits     []RewriteHit // Files each layout.rewrite rule changed.
	RewriteWarnings []string     // layout.rewrite rules that matched nothing.

	Suggestions      []string // Rewrite rules suggested to cover paths flatten changed.
	BinaryReferences []string // Binary files referencing a path flatten changed.

	Entries []string // Final, sorted archive entry paths.

	OutputPath string // Where the archive was (or would be) written.

	fs *fileSet
}

// Move is one path renamed by flatten.
type Move struct {
	From string
	To   string
}

// Warnings returns every §11 SHOULD-warn condition hit during the build.
func (r *Result) Warnings() []string {
	return append(append([]string{}, r.TrimWarnings...), r.RewriteWarnings...)
}

// Run executes the §6 build pipeline and, unless opts.DryRun, writes the
// archive.
func Run(opts Options) (*Result, error) {
	m, err := manifest.Load(opts.SourceDir)
	if err != nil {
		return nil, err
	}

	effectiveName := m.Name
	if opts.Name != "" {
		effectiveName = opts.Name
	}

	outputPath := opts.OutputPath
	if outputPath == "" {
		outputPath = effectiveName + ".dosz"
	}

	fs, err := collect(opts.SourceDir, m)
	if err != nil {
		return nil, err
	}

	res := &Result{Manifest: m, EffectiveName: effectiveName, OutputPath: outputPath, fs: fs}

	res.Trimmed, res.TrimWarnings = trim(fs, m.Layout.Trim)

	moved, dropped := flatten(fs, m.Layout.Flatten)
	for from, to := range moved {
		res.Moved = append(res.Moved, Move{From: from, To: to})
	}

	if opts.DryRun {
		res.Suggestions, res.BinaryReferences = suggestRewrites(fs, dropped)
	}

	hits, rewriteWarnings, err := applyRewrite(fs, m.Layout.Rewrite)
	if err != nil {
		return nil, err
	}
	res.RewriteHits = hits
	res.RewriteWarnings = rewriteWarnings

	if err := normalizeText(fs); err != nil {
		return nil, err
	}

	exclude(fs)

	if err := ValidateArchiveContent(fs.paths(), m.Model()); err != nil {
		return nil, err
	}

	if err := addRuntimeFiles(fs, m, effectiveName); err != nil {
		return nil, err
	}

	res.Entries = fs.paths()

	if opts.Strict {
		if warnings := res.Warnings(); len(warnings) > 0 {
			return res, fmt.Errorf("strict mode: %s", strings.Join(warnings, "; "))
		}
	}

	if opts.DryRun {
		return res, nil
	}

	out, err := os.Create(outputPath)
	if err != nil {
		return res, err
	}
	if err := writeArchive(fs, m, out); err != nil {
		out.Close()
		return res, err
	}
	if err := out.Close(); err != nil {
		return res, err
	}

	return res, nil
}

// writeArchive writes every file in fs to w as a deterministic ZIP (§8).
func writeArchive(fs *fileSet, m *manifest.Manifest, w io.Writer) error {
	zw := zipw.New(w)

	for _, p := range fs.paths() {
		e, _ := fs.get(p)
		store := ShouldStore(p, m.Pack.Store)

		f, data, err := e.open()
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}

		var r io.Reader = bytes.NewReader(data)
		if f != nil {
			r = f
		}

		addErr := zw.Add(p, r, store)
		if f != nil {
			f.Close()
		}
		if addErr != nil {
			return fmt.Errorf("%s: %w", p, addErr)
		}
	}

	return zw.Close()
}

// ShouldStore reports whether a path must use the Store method (§8.1):
// an extension listed in pack.store, or .ogg always.
func ShouldStore(p string, storeExts []string) bool {
	ext := strings.TrimPrefix(path.Ext(p), ".")
	if strings.EqualFold(ext, "ogg") {
		return true
	}
	for _, se := range storeExts {
		if strings.EqualFold(ext, se) {
			return true
		}
	}
	return false
}
