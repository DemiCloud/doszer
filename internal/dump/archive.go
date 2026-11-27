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

	"github.com/DemiCloud/doszer/internal/manifest"
	"github.com/DemiCloud/doszer/internal/zipw"
)

// What an archive (.zip, .dosz, .dosp) holds, for Import.
const (
	ArchiveDisc   = "disc"   // one CUE sheet: a disc image, Import's
	ArchiveDoszer = "doszer" // a manifest at its root: repack it instead
	ArchiveFiles  = "files"  // a game's files: ImportArchive's
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

// ArchiveKind says what an archive holds: a disc image (exactly one CUE
// sheet), a doszer archive (DOSZ.TOML at its root), or a game's files.
func ArchiveKind(p string) (string, error) {
	zr, err := zip.OpenReader(p)
	if err != nil {
		return "", err
	}
	defer zr.Close()
	cues := 0
	for _, f := range zr.File {
		switch {
		case strings.EqualFold(f.Name, manifest.FileName):
			return ArchiveDoszer, nil
		case strings.EqualFold(pathpkg.Ext(f.Name), ".cue"):
			cues++
		}
	}
	if cues == 1 {
		return ArchiveDisc, nil
	}
	return ArchiveFiles, nil
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
// with a manifest: it is rebuilt as it is (repack), not imported.
var ErrDoszerArchive = errors.New("a doszer archive (it has a DOSZ.TOML): repack it instead")
