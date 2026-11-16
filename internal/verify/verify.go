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
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/DemiCloud/doszer/internal/build"
	"github.com/DemiCloud/doszer/internal/manifest"
	"github.com/DemiCloud/doszer/internal/status"
)

// Verify checks archivePath and returns every way it fails to conform, or
// nil if it conforms.
func Verify(archivePath string) error {
	_, err := Check(archivePath)
	return err
}

// Check is Verify, also returning warnings: conditions a conforming
// archive can have but that are worth knowing, such as a declared media
// its disc images don't match (§7.3).
func Check(archivePath string) (warnings []string, err error) {
	status.Step("Checking %s", filepath.Base(archivePath))
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", archivePath, err)
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
		return nil, errors.Join(errs...)
	}
	if !isEpoch(mf.Modified) {
		errs = append(errs, fmt.Errorf("%s: a generated file must use the fixed epoch (§8.2)", mf.Name))
	}

	data, err := readZipFile(mf)
	if err != nil {
		errs = append(errs, fmt.Errorf("%s: %w", manifest.FileName, err))
		return nil, errors.Join(errs...)
	}

	defaultName := strings.TrimSuffix(filepath.Base(archivePath), filepath.Ext(archivePath))
	m, err := manifest.Parse(data, defaultName)
	if err != nil {
		errs = append(errs, fmt.Errorf("embedded %s is invalid: %w", manifest.FileName, err))
		return nil, errors.Join(errs...)
	}

	// First, so the disc images it reads through have had their CRC-32
	// checked and checkWithManifest needn't decompress them again.
	warnings, crcChecked, mediaErr := build.ArchiveMedia(r.File, m.Source.Media)

	errs = append(errs, checkWithManifest(names, byName, m, crcChecked)...)
	if mediaErr != nil {
		errs = append(errs, mediaErr)
	}

	return warnings, errors.Join(errs...)
}

// isEpoch reports whether t is the fixed 1980-01-01 00:00:00 timestamp
// §8.2 requires for a generated file.
func isEpoch(t time.Time) bool {
	return t.UTC().Format("2006-01-02T15:04:05Z") == "1980-01-01T00:00:00Z"
}

// checkDeterministic enforces the structural part of §8.2 that applies to
// every entry regardless of its source: compression method, external
// attributes, extra fields, no directory entries and path order. A collected or extracted
// entry's modification time is its source's own (§8.2), which verify has
// no independent way to check; only the three generated files (checked
// against the manifest in checkWithManifest and in Verify) must use the
// fixed epoch.
func checkDeterministic(files []*zip.File) []error {
	var errs []error

	names := make([]string, len(files))
	for i, f := range files {
		names[i] = f.Name

		if f.Method != zip.Store && f.Method != zip.Deflate {
			errs = append(errs, fmt.Errorf("%s: compression method must be Store or Deflate (§8.1)", f.Name))
		}
		if f.ExternalAttrs != 0 {
			errs = append(errs, fmt.Errorf("%s: external attributes must be zero (§8.2)", f.Name))
		}
		if len(f.Extra) != 0 {
			errs = append(errs, fmt.Errorf("%s: must not have extra fields (§8.2)", f.Name))
		}
		if strings.HasSuffix(f.Name, "/") {
			errs = append(errs, fmt.Errorf("%s: must not be a directory entry (§8.2)", f.Name))
		}
	}

	if !sort.StringsAreSorted(names) {
		errs = append(errs, fmt.Errorf("entries are not ordered by path (§8.2)"))
	}

	return errs
}

// checkWithManifest enforces the rules that depend on the embedded
// manifest: §8.1's store requirements, §6.4's disc image conversion, §6.6's
// text normalization, §6.7's exclusions, §11's archive-content rules, and the runtime files §6.8
// requires - not just their presence, but their content. crcChecked holds
// the CRC-32 results of entries already read through (build.ArchiveMedia).
func checkWithManifest(names []string, byName map[string]*zip.File, m *manifest.Manifest, crcChecked map[string]error) []error {
	var errs []error

	contentErrs := checkContents(names, byName, crcChecked)
	for i, name := range names {
		f := byName[name]

		if build.ShouldStore(name, m.Pack.Store) && f.Method != zip.Store {
			errs = append(errs, fmt.Errorf("%s: must be stored, not deflated (§8.1)", name))
		}
		if build.IsExcluded(name) {
			errs = append(errs, fmt.Errorf("%s: must not be in the archive (§6.7)", name))
		}
		errs = append(errs, contentErrs[i]...)
	}

	files := make([]*zip.File, 0, len(names))
	for _, name := range names {
		files = append(files, byName[name])
	}
	for _, p := range build.UnconvertedMedia(files, m.AudioEncoding()) {
		errs = append(errs, fmt.Errorf("%s: disc image not converted or named as §6.4 requires", p))
	}

	if err := build.ValidateArchiveContent(names, m.Model()); err != nil {
		errs = append(errs, err)
	}

	if build.NeedsAutoboot(&m.Boot) {
		f, ok := findFold(byName, "AUTOBOOT.DBP")
		if !ok {
			errs = append(errs, fmt.Errorf("missing AUTOBOOT.DBP (§6.8)"))
		} else {
			if !isEpoch(f.Modified) {
				errs = append(errs, fmt.Errorf("%s: a generated file must use the fixed epoch (§8.2)", f.Name))
			}
			if content, err := readZipFile(f); err == nil {
				if want := build.BuildAutoboot(&m.Boot); !bytes.Equal(content, want) {
					errs = append(errs, fmt.Errorf("%s: content does not match the manifest's boot settings (§6.8), got %q want %q", f.Name, content, want))
				}
			}
		}
	}

	if want := build.BuildPadmap(m); want != nil {
		errs = append(errs, checkGenerated(byName, "PADMAP.DBP", func([]byte) []byte { return want })...)
	}

	// DOS.YML keeps its own lines other than input mappings (§6.8.3), so
	// what it should be depends on what it is: regenerating a conforming
	// one reproduces it.
	if build.BuildDOSYML(m, nil) != nil {
		errs = append(errs, checkGenerated(byName, "DOS.YML", func(got []byte) []byte { return build.BuildDOSYML(m, got) })...)
	}

	if m.Boot.Parent != "" {
		name := m.Boot.Parent + ".parent"
		f, ok := findFold(byName, name)
		switch {
		case !ok:
			errs = append(errs, fmt.Errorf("missing %s (§6.8)", name))
		case f.UncompressedSize64 != 0:
			errs = append(errs, fmt.Errorf("%s: must be zero bytes (§6.8)", f.Name))
		case !isEpoch(f.Modified):
			errs = append(errs, fmt.Errorf("%s: a generated file must use the fixed epoch (§8.2)", f.Name))
		}
	}

	return errs
}

// checkContents reads every entry, checking its CRC-32 and, for a text
// file, §6.6's CRLF normalization. Decompressing an entry uses one core,
// so entries are read concurrently, one per CPU; errors are returned by
// entry, in names' order. An entry in crcChecked isn't read again unless
// it is a text file.
func checkContents(names []string, byName map[string]*zip.File, crcChecked map[string]error) [][]error {
	out := make([][]error, len(names))
	next := make(chan int)
	var wg sync.WaitGroup
	var mu sync.Mutex
	done := 0
	for range min(runtime.GOMAXPROCS(0), max(len(names), 1)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				out[i] = checkContent(names[i], byName[names[i]], crcChecked)
				mu.Lock()
				done++
				status.Count(done, len(names))
				mu.Unlock()
			}
		}()
	}
	for i := range names {
		next <- i
	}
	close(next)
	wg.Wait()
	return out
}

// checkContent is checkContents for one entry.
func checkContent(name string, f *zip.File, crcChecked map[string]error) []error {
	// Only a file named like a text file (§6.6) needs its content in
	// memory; every other entry is streamed, which still checks its
	// CRC-32, so a large disc image costs no memory.
	if !build.IsTextFile(name, nil) {
		err, checked := crcChecked[name]
		if !checked {
			err = checkCRC(f)
		}
		if err != nil {
			return []error{fmt.Errorf("%s: %w", name, err)}
		}
		return nil
	}
	content, err := readZipFile(f)
	if err != nil {
		return []error{fmt.Errorf("%s: %w", name, err)}
	}
	if build.IsTextFile(name, content) && !bytes.Equal(content, build.NormalizeCRLF(content)) {
		return []error{fmt.Errorf("%s: not CRLF-normalized (§6.6)", name)}
	}
	return nil
}

// checkGenerated checks a §6.8 file the [[input.pad]] mapping implies:
// present, at the fixed epoch, and with the content want returns for it.
func checkGenerated(byName map[string]*zip.File, name string, want func(got []byte) []byte) []error {
	f, ok := findFold(byName, name)
	if !ok {
		return []error{fmt.Errorf("missing %s (§6.8)", name)}
	}
	var errs []error
	if !isEpoch(f.Modified) {
		errs = append(errs, fmt.Errorf("%s: a generated file must use the fixed epoch (§8.2)", f.Name))
	}
	content, err := readZipFile(f)
	if err != nil {
		return append(errs, fmt.Errorf("%s: %w", f.Name, err))
	}
	if w := want(content); !bytes.Equal(content, w) {
		errs = append(errs, fmt.Errorf("%s: content does not match the manifest's [[input.pad]] (§6.8), got %q want %q", f.Name, content, w))
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

// checkCRC reads an entry through, which makes archive/zip verify its
// CRC-32 against the stored one.
func checkCRC(f *zip.File) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	_, err = io.Copy(io.Discard, rc)
	return err
}

func readZipFile(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}
