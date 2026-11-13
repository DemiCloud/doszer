// Package repack implements `doszer repack`: rebuilding an archive from
// nothing but its own embedded manifest - no source directory, since none
// is available once an archive already exists. The archive's own entries
// stand in for §6.1's collect step (extract and media identification don't
// apply: whatever they would have produced is already sitting in the
// archive), then the rest of spec §6 runs exactly as it does for `build`.
//
// This is deliberately not scoped to "archives doszer built": an archive's
// content, plus a manifest describing it (inserted by hand, or edited after
// the fact), is exactly what repack needs to apply layout.trim/rewrite and
// §6.6 text normalization to a legacy archive that never went through
// doszer build at all.
package repack

import (
	"archive/zip"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/DemiCloud/doszer/internal/build"
	"github.com/DemiCloud/doszer/internal/manifest"
)

// Options configures a repack.
type Options struct {
	InputPath  string
	OutputPath string // defaults to "<effective name>.dosz" in the working directory
	DryRun     bool   // compute and report the plan without writing an archive
	Strict     bool   // fail if any §11 warning condition occurs
}

// Run reads opts.InputPath's embedded manifest, treats the archive's own
// entries as the collected build, and runs §6.2 onward (build.Repack).
func Run(opts Options) (*build.Result, error) {
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
		data, err := readEntry(f)
		if err != nil {
			return nil, fmt.Errorf("%s: %s: %w", opts.InputPath, f.Name, err)
		}
		// Preserve whatever modification time (§8.2) was already on this
		// entry, rather than synthesizing one.
		entries[f.Name] = build.Entry{Data: data, Modified: f.Modified}
	}
	if mf == nil {
		return nil, fmt.Errorf("%s: missing %s (§8.3)", opts.InputPath, manifest.FileName)
	}

	data, err := readEntry(mf)
	if err != nil {
		return nil, fmt.Errorf("%s: %s: %w", opts.InputPath, manifest.FileName, err)
	}

	defaultName := strings.TrimSuffix(filepath.Base(opts.InputPath), filepath.Ext(opts.InputPath))
	m, err := manifest.Parse(data, defaultName)
	if err != nil {
		return nil, fmt.Errorf("%s: embedded %s is invalid: %w", opts.InputPath, manifest.FileName, err)
	}

	// §6.1's final layering step: [[file]] entries replace a collected
	// file at the same path, same as build's collect(). A [[file]] entry
	// has no source of its own, so it gets the fixed epoch (§8.2).
	for _, f := range m.File {
		entries[f.Path] = build.Entry{Data: []byte(*f.Text), Modified: build.Epoch}
	}

	return build.Repack(entries, m, build.Options{
		OutputPath: opts.OutputPath,
		DryRun:     opts.DryRun,
		Strict:     opts.Strict,
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
