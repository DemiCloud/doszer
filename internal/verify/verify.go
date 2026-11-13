// Package verify implements `doszer verify`: checking a built archive
// against its embedded manifest and the specification (§8, §11).
package verify

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/DemiCloud/doszer/internal/build"
	"github.com/DemiCloud/doszer/internal/manifest"
)

// epoch is the fixed modification time spec §8.2 requires.
const epoch = "1980-01-01T00:00:00Z"

// Verify checks archivePath and returns every way it fails to conform, or
// nil if it conforms.
func Verify(archivePath string) error {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("%s: %w", archivePath, err)
	}
	defer r.Close()

	var errs []error
	errs = append(errs, checkDeterministic(r.File)...)

	names := make([]string, len(r.File))
	byName := make(map[string]*zip.File, len(r.File))
	for i, f := range r.File {
		names[i] = f.Name
		byName[f.Name] = f
	}

	mf, ok := findFold(byName, manifest.FileName)
	if !ok {
		errs = append(errs, fmt.Errorf("missing %s (§8.3)", manifest.FileName))
		return errors.Join(errs...)
	}

	data, err := readZipFile(mf)
	if err != nil {
		errs = append(errs, fmt.Errorf("%s: %w", manifest.FileName, err))
		return errors.Join(errs...)
	}

	defaultName := strings.TrimSuffix(filepath.Base(archivePath), filepath.Ext(archivePath))
	m, err := manifest.Parse(data, defaultName)
	if err != nil {
		errs = append(errs, fmt.Errorf("embedded %s is invalid: %w", manifest.FileName, err))
		return errors.Join(errs...)
	}

	errs = append(errs, checkWithManifest(names, byName, m)...)

	return errors.Join(errs...)
}

// checkDeterministic enforces §8.2's structural requirements, independent
// of the embedded manifest.
func checkDeterministic(files []*zip.File) []error {
	var errs []error

	names := make([]string, len(files))
	for i, f := range files {
		names[i] = f.Name

		if f.Method != zip.Store && f.Method != zip.Deflate {
			errs = append(errs, fmt.Errorf("%s: compression method must be Store or Deflate (§8.1)", f.Name))
		}
		if got := f.Modified.UTC().Format("2006-01-02T15:04:05Z"); got != epoch {
			errs = append(errs, fmt.Errorf("%s: modification time is %s, want %s (§8.2)", f.Name, got, epoch))
		}
		if f.ExternalAttrs != 0 {
			errs = append(errs, fmt.Errorf("%s: external attributes must be zero (§8.2)", f.Name))
		}
		if len(f.Extra) != 0 {
			errs = append(errs, fmt.Errorf("%s: must not have extra fields (§8.2)", f.Name))
		}
	}

	if !sort.StringsAreSorted(names) {
		errs = append(errs, fmt.Errorf("entries are not ordered by path (§8.2)"))
	}

	return errs
}

// checkWithManifest enforces the rules that depend on the embedded
// manifest: §8.1's store requirements, §6.6's text normalization, §6.7's
// exclusions, §11's archive-content rules, and the runtime files §6.8
// requires - not just their presence, but their content.
func checkWithManifest(names []string, byName map[string]*zip.File, m *manifest.Manifest) []error {
	var errs []error

	for _, name := range names {
		f := byName[name]

		if build.ShouldStore(name, m.Pack.Store) && f.Method != zip.Store {
			errs = append(errs, fmt.Errorf("%s: must be stored, not deflated (§8.1)", name))
		}
		if build.IsExcluded(name) {
			errs = append(errs, fmt.Errorf("%s: must not be in the archive (§6.7)", name))
		}

		content, err := readZipFile(f)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			continue
		}
		if build.IsTextFile(name, content) {
			if !bytes.Equal(content, build.NormalizeCRLF(content)) {
				errs = append(errs, fmt.Errorf("%s: not CRLF-normalized (§6.6)", name))
			}
		}
	}

	if err := build.ValidateArchiveContent(names, m.Model()); err != nil {
		errs = append(errs, err)
	}

	if build.NeedsAutoboot(&m.Boot) {
		f, ok := findFold(byName, "AUTOBOOT.DBP")
		if !ok {
			errs = append(errs, fmt.Errorf("missing AUTOBOOT.DBP (§6.8)"))
		} else if content, err := readZipFile(f); err == nil {
			if want := build.BuildAutoboot(&m.Boot); !bytes.Equal(content, want) {
				errs = append(errs, fmt.Errorf("%s: content does not match the manifest's boot settings (§6.8), got %q want %q", f.Name, content, want))
			}
		}
	}

	if m.Boot.Parent != "" {
		name := m.Boot.Parent + ".parent"
		f, ok := findFold(byName, name)
		switch {
		case !ok:
			errs = append(errs, fmt.Errorf("missing %s (§6.8)", name))
		case f.UncompressedSize64 != 0:
			errs = append(errs, fmt.Errorf("%s: must be zero bytes (§6.8)", f.Name))
		}
	}

	return errs
}

// findFold looks up name in byName case-insensitively (§2).
func findFold(byName map[string]*zip.File, name string) (*zip.File, bool) {
	for n, f := range byName {
		if strings.EqualFold(n, name) {
			return f, true
		}
	}
	return nil, false
}

func readZipFile(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}
