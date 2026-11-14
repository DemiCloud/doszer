// Package build implements `doszer build`: spec §6's processing model,
// producing a deterministic archive per §8. [[extract]] (§5.7) and media
// identification (§7) and disc image conversion (§6.4) are implemented.
package build

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

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
	OggQuality int    // libvorbis quality for Ogg audio tracks, -1 to 10 (internal/config's ogg_quality).
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

	Converted     []Conversion // Disc images §6.4 converted or renamed.
	MediaWarnings []string     // media §7.3 could not find or fully verify; CUE sheets §6.4 could not convert.

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
	w := append(append([]string{}, r.TrimWarnings...), r.RewriteWarnings...)
	return append(w, r.MediaWarnings...)
}

// Run executes the §6 build pipeline and, unless opts.DryRun, writes the
// archive.
func Run(opts Options) (*Result, error) {
	m, err := manifest.Load(opts.SourceDir)
	if err != nil {
		return nil, err
	}

	fs, mediaWarnings, err := collect(opts.SourceDir, m)
	if err != nil {
		return nil, err
	}

	return runPipeline(fs, m, opts, mediaWarnings)
}

// Entry is one already-collected file for Repack: its content and the
// modification time (§8.2) to carry forward - typically whatever was
// already recorded on the archive's own entry, or Epoch for an entry with
// no source of its own (e.g. a [[file]] entry layered on top).
//
// Exactly one of Data or Source should be set. Source lets Repack defer
// decompressing an entry until the pipeline actually reads or changes its
// content, so writeArchive can copy an untouched entry's already-compressed
// bytes straight through instead of decompressing and recompressing it.
type Entry struct {
	Data     []byte
	Source   *RawSource
	Modified time.Time
}

// Repack runs §6.2 onward (trim through the deterministic write) against an
// already-collected set of entries, rather than a source directory: used by
// `doszer repack` to rebuild an archive from nothing but its own embedded
// manifest, treating the archive's own content as the collected build.
// entries' keys are archive-relative paths; DOSZ.TOML must not be among
// them (§6.1 excludes it from collect, same as a source directory).
func Repack(entries map[string]Entry, m *manifest.Manifest, opts Options) (*Result, error) {
	fs := newFileSet()
	for p, e := range entries {
		if e.Source != nil {
			fs.set(p, sourceEntry(e.Source, e.Modified))
		} else {
			fs.set(p, dataEntry(e.Data, e.Modified))
		}
	}
	// The archive's disc images are verified as a source's would be (§7.3);
	// [[extract]] already happened, so a missing media is only a warning.
	_, mediaWarnings, err := locateMedia(fs, m.Source.Media, nil)
	if err != nil {
		return nil, err
	}
	return runPipeline(fs, m, opts, mediaWarnings)
}

// runPipeline runs §6.2 onward against fs, shared by Run and Repack.
// mediaWarnings are §7.3's, from verifying fs's media.
func runPipeline(fs *fileSet, m *manifest.Manifest, opts Options, mediaWarnings []string) (_ *Result, err error) {
	effectiveName := m.Name
	if opts.Name != "" {
		effectiveName = opts.Name
	}

	outputPath := opts.OutputPath
	if outputPath == "" {
		outputPath = effectiveName + ".dosz"
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

	if err := renameMediaCue(fs, m.Source.Media, &m.Boot, effectiveName); err != nil {
		return nil, err
	}

	// Converted tracks are written next to the output archive, which needs
	// room for them anyway, rather than to a possibly RAM-backed /tmp; the
	// directory lives until writeArchive has copied them in.
	var tmp string
	defer func() {
		if tmp != "" {
			if rmErr := os.RemoveAll(tmp); err == nil {
				err = rmErr
			}
		}
	}()
	tmpDir := func() (string, error) {
		var mkErr error
		if tmp == "" {
			tmp, mkErr = os.MkdirTemp(filepath.Dir(outputPath), ".doszer-media-")
		}
		return tmp, mkErr
	}
	if opts.OggQuality < -1 || opts.OggQuality > 10 {
		return nil, fmt.Errorf("ogg_quality %d is out of range (-1 to 10)", opts.OggQuality)
	}
	var convertWarnings []string
	res.Converted, convertWarnings, err = convertMedia(fs, m.AudioEncoding(), opts.OggQuality, opts.DryRun, tmpDir)
	if err != nil {
		return nil, err
	}
	res.MediaWarnings = append(mediaWarnings, convertWarnings...)

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
	spoolDir, err := tmpDir()
	if err != nil {
		out.Close()
		return res, err
	}
	if err := writeArchive(fs, m, out, spoolDir); err != nil {
		out.Close()
		return res, err
	}
	if err := out.Close(); err != nil {
		return res, err
	}

	return res, nil
}

// writeArchive writes every file in fs to w as a deterministic ZIP (§8). An
// entry whose content was never read or changed by the pipeline, and whose
// store decision (§8.1) still matches the compression method it already
// had, is copied through in its already-compressed form rather than being
// decompressed and recompressed - repack's main cost on an archive most of
// whose content a given run leaves untouched.
//
// spoolDir holds zipw's temporary files for large compressed entries.
func writeArchive(fs *fileSet, m *manifest.Manifest, w io.Writer, spoolDir string) error {
	zw := zipw.New(w)
	zw.TempDir = spoolDir

	for _, p := range fs.paths() {
		e, _ := fs.get(p)
		store := ShouldStore(p, m.Pack.Store)
		wantMethod := uint16(zip.Deflate)
		if store {
			wantMethod = zip.Store
		}

		if src := e.rawPassthrough(); src != nil && src.Method == wantMethod {
			raw, err := src.Raw()
			if err != nil {
				return fmt.Errorf("%s: %w", p, err)
			}
			err = zw.AddRaw(p, src.Method, src.CRC32, src.CompressedSize, src.UncompressedSize, raw, e.modified)
			if c, ok := raw.(io.Closer); ok {
				c.Close()
			}
			if err != nil {
				return fmt.Errorf("%s: %w", p, err)
			}
			continue
		}

		r, err := e.reader()
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		addErr := zw.Add(p, r, store, e.modified)
		if c, ok := r.(io.Closer); ok {
			c.Close()
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
