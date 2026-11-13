// Package repack implements `doszer repack`: rebuilding an archive from its
// own embedded manifest, with no source directory - just the archive's own
// entries, re-emitted deterministically (§8) with the runtime files §6.8
// generates (AUTOBOOT.DBP, the parent marker, DOSZ.TOML itself) refreshed
// from the current embedded manifest.
package repack

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/DemiCloud/doszer/internal/build"
	"github.com/DemiCloud/doszer/internal/manifest"
	"github.com/DemiCloud/doszer/internal/zipw"
)

// Options configures a repack.
type Options struct {
	InputPath  string
	OutputPath string // defaults to "<effective name>.dosz" in the working directory
}

// Result reports what a repack did.
type Result struct {
	OutputPath string
	Entries    []string // final, sorted archive entry paths
}

// Run reads opts.InputPath's embedded manifest, regenerates its runtime
// files (§6.8), and writes every entry to a new, deterministic archive
// (§8) at opts.OutputPath.
func Run(opts Options) (*Result, error) {
	r, err := zip.OpenReader(opts.InputPath)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", opts.InputPath, err)
	}
	defer r.Close()

	byName := make(map[string]*zip.File, len(r.File))
	for _, f := range r.File {
		byName[f.Name] = f
	}

	mf, ok := findFold(byName, manifest.FileName)
	if !ok {
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

	outputPath := opts.OutputPath
	if outputPath == "" {
		outputPath = m.Name + ".dosz"
	}

	manifestData, err := build.EffectiveManifest(m, m.Name)
	if err != nil {
		return nil, err
	}
	regenerated := map[string][]byte{manifest.FileName: manifestData}
	if build.NeedsAutoboot(&m.Boot) {
		regenerated["AUTOBOOT.DBP"] = build.BuildAutoboot(&m.Boot)
	}
	if m.Boot.Parent != "" {
		regenerated[m.Boot.Parent+".parent"] = []byte{}
	}

	entries := mergeEntries(byName, regenerated)
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)

	if err := build.ValidateArchiveContent(names, m.Model()); err != nil {
		return nil, err
	}

	out, err := os.Create(outputPath)
	if err != nil {
		return nil, err
	}
	if err := writeArchive(out, names, entries, regenerated, m); err != nil {
		out.Close()
		return nil, err
	}
	if err := out.Close(); err != nil {
		return nil, err
	}

	return &Result{OutputPath: outputPath, Entries: names}, nil
}

// mergeEntries applies regenerated on top of the original archive's
// entries, case-insensitively (§2): a regenerated path replaces any
// existing entry of the same name, adopting the regenerated path's casing.
// A nil value marks an entry whose content comes from regenerated, not
// from the original archive.
func mergeEntries(original map[string]*zip.File, regenerated map[string][]byte) map[string]*zip.File {
	result := make(map[string]*zip.File, len(original))
	lower := make(map[string]string, len(original))
	for name, f := range original {
		result[name] = f
		lower[strings.ToLower(name)] = name
	}

	for name := range regenerated {
		if existing, ok := lower[strings.ToLower(name)]; ok && existing != name {
			delete(result, existing)
		}
		result[name] = nil
		lower[strings.ToLower(name)] = name
	}

	return result
}

// writeArchive writes every entry in names to w, streaming an original
// entry's content directly rather than buffering it, except for the small
// regenerated entries.
func writeArchive(w io.Writer, names []string, entries map[string]*zip.File, regenerated map[string][]byte, m *manifest.Manifest) error {
	zw := zipw.New(w)

	for _, name := range names {
		store := build.ShouldStore(name, m.Pack.Store)

		if data, ok := regenerated[name]; ok {
			if err := zw.Add(name, bytes.NewReader(data), store); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			continue
		}

		rc, err := entries[name].Open()
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		addErr := zw.Add(name, rc, store)
		rc.Close()
		if addErr != nil {
			return fmt.Errorf("%s: %w", name, addErr)
		}
	}

	return zw.Close()
}

func findFold(byName map[string]*zip.File, name string) (*zip.File, bool) {
	for n, f := range byName {
		if strings.EqualFold(n, name) {
			return f, true
		}
	}
	return nil, false
}

func readEntry(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}
