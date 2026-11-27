package dump

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	pathpkg "path"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/DemiCloud/doszer/internal/manifest"
	"github.com/DemiCloud/doszer/internal/zipw"
)

// What an archive (.zip, .dosz, .dosp) holds, for Import.
const (
	ArchiveDisc   = "disc"   // one CUE sheet: a disc image, Import's
	ArchiveDoszer = "doszer" // a valid manifest at its root: repack it instead
	ArchiveFiles  = "files"  // a game's files, with no valid manifest: ImportArchive's
)

// IsArchive reports whether p is named as an archive Import or
// ImportArchive reads.
func IsArchive(p string) bool {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".zip", ".dosz", ".dosp":
		return true
	}
	return false
}

// ArchiveKind says what an archive holds (Inspect's Kind).
func ArchiveKind(p string) (string, error) {
	st, err := Inspect(p)
	return st.Kind, err
}

// Status is what an archive holds and, when it has no valid manifest,
// why not.
type Status struct {
	Kind    string // ArchiveDisc, ArchiveDoszer or ArchiveFiles
	Problem string // "" for a valid manifest; else, e.g. "no manifest", "an old manifest (format 0)"
}

// Inspect says what an archive holds: a doszer archive (a DOSZ.TOML at
// its root that is a valid manifest of this format), a disc image
// (exactly one CUE sheet), or a game's files.
func Inspect(p string) (Status, error) {
	zr, err := zip.OpenReader(p)
	if err != nil {
		return Status{}, err
	}
	defer zr.Close()
	cues := 0
	problem := "no manifest"
	for _, f := range zr.File {
		switch {
		case f.Name == manifest.FileName:
			problem = manifestProblem(f)
			if problem == "" {
				return Status{Kind: ArchiveDoszer}, nil
			}
		case strings.EqualFold(pathpkg.Ext(f.Name), ".cue"):
			cues++
		}
	}
	if cues == 1 {
		return Status{Kind: ArchiveDisc, Problem: problem}, nil
	}
	return Status{Kind: ArchiveFiles, Problem: problem}, nil
}

// manifestProblem says why an archive's DOSZ.TOML isn't a valid
// manifest of this format, or "" if it is.
func manifestProblem(f *zip.File) string {
	rc, err := f.Open()
	if err != nil {
		return "a manifest that can't be read: " + err.Error()
	}
	data, err := io.ReadAll(io.LimitReader(rc, 16<<20))
	rc.Close()
	if err != nil {
		return "a manifest that can't be read: " + err.Error()
	}
	var head struct {
		Format int `toml:"format"`
	}
	if toml.Unmarshal(data, &head) == nil {
		switch {
		case head.Format > 0 && head.Format < manifest.FormatVersion:
			return fmt.Sprintf("an old manifest (format %d; this doszer writes format %d)", head.Format, manifest.FormatVersion)
		case head.Format > manifest.FormatVersion:
			return fmt.Sprintf("a newer manifest (format %d; this doszer reads format %d)", head.Format, manifest.FormatVersion)
		}
	}
	if _, err := manifest.Parse(data, ""); err != nil {
		first, _, _ := strings.Cut(err.Error(), "\n")
		return "a manifest that isn't valid: " + first
	}
	return ""
}

// ImportArchive extracts a game's archive into dir, every file at its
// path with the date recorded for it (its MS-DOS date and time, the one
// DOSBox Pure reads), and returns how many files it wrote. A file already
// in dir is an error, as is an entry naming a path outside it.
func ImportArchive(archive, dir string) (int, error) {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return 0, err
	}
	defer zr.Close()
	type entry struct {
		f    *zip.File
		path string
	}
	var entries []entry
	for _, f := range zr.File {
		name := strings.ReplaceAll(f.Name, `\`, "/")
		if strings.HasSuffix(name, "/") {
			continue // a folder: made for the files in it
		}
		clean := pathpkg.Clean(name)
		if pathpkg.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(clean, ":") {
			return 0, fmt.Errorf("%s: entry %q is outside the archive's folder", archive, f.Name)
		}
		entries = append(entries, entry{f, filepath.Join(dir, filepath.FromSlash(clean))})
	}
	if len(entries) == 0 {
		return 0, fmt.Errorf("%s: no files", archive)
	}
	for _, e := range entries {
		if _, err := os.Stat(e.path); err == nil {
			return 0, fmt.Errorf("%s already exists", e.path)
		}
	}
	n := 0
	for _, e := range entries {
		if err := os.MkdirAll(filepath.Dir(e.path), 0o755); err != nil {
			return n, err
		}
		rc, err := e.f.Open()
		if err != nil {
			return n, fmt.Errorf("%s: %w", e.f.Name, err)
		}
		err = writeNew(e.path, func(w io.Writer) error {
			_, err := io.Copy(w, rc)
			return err
		})
		rc.Close()
		if err != nil {
			return n, fmt.Errorf("%s: %w", e.f.Name, err)
		}
		// The recorded wall-clock time, as the files' own clock tells it.
		m := zipw.DOSModified(&e.f.FileHeader)
		local := time.Date(m.Year(), m.Month(), m.Day(), m.Hour(), m.Minute(), m.Second(), 0, time.Local)
		os.Chtimes(e.path, local, local)
		n++
	}
	return n, nil
}

// ErrDoszerArchive is ImportArchive's caller's answer for an archive
// with a valid manifest: it is rebuilt as it is (repack), not imported.
var ErrDoszerArchive = errors.New("it has a valid manifest: repack it instead")
