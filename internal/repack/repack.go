// Package repack implements `doszer repack`: rebuilding an archive from
// nothing but a manifest - no source directory, since none is available
// once an archive already exists. By default that manifest is the one
// already embedded in the archive; ManifestPath substitutes another one
// (e.g. an edited copy), which also lets repack retrofit a manifest onto an
// archive with no embedded one at all. The archive's own entries stand in
// for §6.1's collect step (extract and media identification don't apply:
// whatever they would have produced is already sitting in the archive),
// then the rest of spec §6 runs exactly as it does for `build`.
//
// This is deliberately not scoped to "archives doszer built": an archive's
// content, plus a manifest describing it, is exactly what repack needs to
// apply layout.trim/rewrite and §6.6 text normalization to a legacy archive
// that never went through doszer build at all.
package repack

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/DemiCloud/doszer/internal/build"
	"github.com/DemiCloud/doszer/internal/manifest"
	"github.com/DemiCloud/doszer/internal/status"
)

// Options configures a repack.
type Options struct {
	InputPath    string
	OutputPath   string // defaults to "<effective name>.dosz" in the working directory
	ManifestPath string // use this manifest instead of the archive's embedded one
	OggQuality   int    // libvorbis quality for Ogg audio tracks (build.Options.OggQuality)
	DryRun       bool   // compute and report the plan without writing an archive
	Strict       bool   // fail if any §11 warning condition occurs

	Provenance *build.Provenance // recorded in the archive comment, if set (§8.4)
}

// Run reads opts.InputPath's manifest - opts.ManifestPath if set, else the
// archive's embedded one - treats the archive's own entries as the
// collected build, and runs §6.2 onward (build.Repack).
func Run(opts Options) (*build.Result, error) {
	status.Step("Reading %s", filepath.Base(opts.InputPath))
	r, err := zip.OpenReader(opts.InputPath)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", opts.InputPath, err)
	}
	defer r.Close()

	var mf *zip.File
	entries := make(map[string]build.Entry, len(r.File))
	for _, f := range r.File {
		if strings.EqualFold(f.Name, manifest.FileName) {
			mf = f
			continue
		}
		if strings.HasSuffix(f.Name, "/") {
			continue // a directory entry: §8.2 allows none
		}
		// Deferred: an entry the pipeline never reads or changes is copied
		// through in its already-compressed form by writeArchive, rather
		// than being decompressed here and recompressed there - repack's
		// main cost on an archive most of whose content a given run leaves
		// untouched.
		entries[f.Name] = build.Entry{Source: rawSource(f), Modified: f.Modified}
	}

	defaultName := strings.TrimSuffix(filepath.Base(opts.InputPath), filepath.Ext(opts.InputPath))

	var data []byte
	manifestSource := opts.InputPath + "'s embedded " + manifest.FileName
	if opts.ManifestPath != "" {
		manifestSource = opts.ManifestPath
		data, err = os.ReadFile(opts.ManifestPath)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", opts.ManifestPath, err)
		}
	} else {
		if mf == nil {
			return nil, fmt.Errorf("%s: missing %s (§8.3)", opts.InputPath, manifest.FileName)
		}
		data, err = readEntry(mf)
		if err != nil {
			return nil, fmt.Errorf("%s: %s: %w", opts.InputPath, manifest.FileName, err)
		}
	}

	m, err := manifest.Parse(data, defaultName)
	if err != nil {
		return nil, fmt.Errorf("%s is invalid: %w", manifestSource, err)
	}

	// §6.1's final layering step: [[file]] entries replace a collected
	// file at the same path (case-insensitively, §2), same as build's
	// collect(). A [[file]] entry has no source of its own, so it gets the
	// fixed epoch (§8.2).
	for _, f := range m.File {
		for p := range entries {
			if strings.EqualFold(p, f.Path) {
				delete(entries, p)
			}
		}
		entries[f.Path] = build.Entry{Data: []byte(*f.Text), Modified: build.Epoch}
	}

	return build.Repack(entries, m, build.Options{
		OutputPath: opts.OutputPath,
		DryRun:     opts.DryRun,
		Strict:     opts.Strict,
		OggQuality: opts.OggQuality,
		Provenance: opts.Provenance,
	})
}

func readEntry(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// rawSource wraps f as a build.RawSource, deferring decompression until
// the pipeline actually needs f's content.
func rawSource(f *zip.File) *build.RawSource {
	return &build.RawSource{
		Method:           f.Method,
		CRC32:            f.CRC32,
		CompressedSize:   f.CompressedSize64,
		UncompressedSize: f.UncompressedSize64,
		Decompressed:     func() ([]byte, error) { return readEntry(f) },
		Open:             f.Open,
		Raw:              f.OpenRaw,
	}
}
