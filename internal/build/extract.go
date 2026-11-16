package build

import (
	"fmt"
	"io"
	"os"
	"path"
	"time"

	"github.com/DemiCloud/doszer/internal/cue"
	"github.com/DemiCloud/doszer/internal/interrupt"
	"github.com/DemiCloud/doszer/internal/iso9660"
	"github.com/DemiCloud/doszer/internal/manifest"
	"github.com/DemiCloud/doszer/internal/status"
)

// applyExtracts performs every [[extract]] entry, in order (§5.7, §6.1):
// each copies a file or directory out of a media's data track filesystem
// into the build.
//
// Extracted files stay in the data track they came from, as ranges of it
// (rangeEntry), rather than being read into memory: the source file
// itself when the track is already 2048-byte sectors on disk, else a
// temporary ISO image created in tempDir (or while hashing, by
// locateMedia) and appended to *tempFiles for the caller to remove once
// the archive is written.
func applyExtracts(fs *fileSet, media map[string]resolvedMedia, extracts []manifest.Extract, tempDir string, tempFiles *[]*os.File) error {
	if len(extracts) > 0 {
		status.Step("Extracting files from disc images")
	}
	tracks := map[string]*dataTrack{}
	defer func() {
		for _, t := range tracks {
			t.f.Close()
		}
	}()

	for _, ex := range extracts {
		t, ok := tracks[ex.Media]
		if !ok {
			var err error
			t, err = openDataTrack(media[ex.Media], tempDir, tempFiles)
			if err != nil {
				return fmt.Errorf("extract: media %q: %w", ex.Media, err)
			}
			tracks[ex.Media] = t
		}

		entry, err := t.img.Lookup(ex.From)
		if err != nil {
			return fmt.Errorf("extract: media %q: %s: %w", ex.Media, ex.From, err)
		}

		if entry.IsDir {
			if err := extractDir(fs, t, entry, ex.To, map[uint32]bool{}); err != nil {
				return fmt.Errorf("extract: media %q: %w", ex.Media, err)
			}
			continue
		}

		if err := extractFile(fs, t, entry, ex.To); err != nil {
			return fmt.Errorf("extract: media %q: %s: %w", ex.Media, ex.From, err)
		}
	}

	return nil
}

// dataTrack is a media's first data track as an ISO 9660 image: size
// bytes of the file at path, from off.
type dataTrack struct {
	img       *iso9660.Image
	path      string
	off, size int64
	f         *os.File // open for img; closed once every extract is done
}

// openDataTrack opens a media's first data track as an ISO 9660 image:
// in place if it is already 2048-byte sectors on disk (directISO), else
// from the copy locateMedia made while hashing it, else materialized now
// as a temporary file in tempDir, appended to *tempFiles for the caller
// to clean up once the archive is written.
func openDataTrack(m resolvedMedia, tempDir string, tempFiles *[]*os.File) (*dataTrack, error) {
	track, err := firstDataTrack(m.Tracks)
	if err != nil {
		return nil, err
	}

	path, off, size, direct := directISO(m.fs, track)
	if !direct {
		if path = m.isoFile; path == "" {
			if path, err = materializeTrack(m, track, tempDir, tempFiles); err != nil {
				return nil, err
			}
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		off, size = 0, info.Size()
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	img, err := iso9660.Open(io.NewSectionReader(f, off, size))
	if err != nil {
		f.Close()
		return nil, err
	}
	return &dataTrack{img: img, path: path, off: off, size: size, f: f}, nil
}

// materializeTrack writes track's normalized content to a new temporary
// file in tempDir, returning its path.
func materializeTrack(m resolvedMedia, track cue.ResolvedTrack, tempDir string, tempFiles *[]*os.File) (string, error) {
	tmp, err := os.CreateTemp(tempDir, ".doszer-media-*.iso")
	if err != nil {
		return "", err
	}
	*tempFiles = append(*tempFiles, tmp)
	interrupt.Remove(tmp.Name()) // left registered: removing a removed file is harmless

	rc, err := m.openTrack(track)
	if err != nil {
		return "", err
	}
	defer rc.Close()
	r, err := track.Normalize(rc)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(tmp, r); err != nil {
		return "", fmt.Errorf("extracting data track: %w", err)
	}
	return tmp.Name(), nil
}

func firstDataTrack(tracks []cue.ResolvedTrack) (cue.ResolvedTrack, error) {
	for _, t := range tracks {
		if !t.Track.IsAudio() {
			return t, nil
		}
	}
	return cue.ResolvedTrack{}, fmt.Errorf("no data track")
}

// extractFile adds e, a file in t, to fs at dest.
func extractFile(fs *fileSet, t *dataTrack, e iso9660.Entry, dest string) error {
	off, n, err := t.img.Extent(e)
	if err != nil {
		return err
	}
	if off+n > t.size {
		return fmt.Errorf("extends past the end of the image")
	}
	fs.set(dest, rangeEntry(t.path, t.off+off, n, sourceModified(e.Modified)))
	return nil
}

// extractDir copies dir's contents into fs, recursively, rooted at destDir.
// visited holds the extents of the directories being extracted, so a
// damaged image whose directory points back at an ancestor fails instead
// of recursing forever.
func extractDir(fs *fileSet, t *dataTrack, dir iso9660.Entry, destDir string, visited map[uint32]bool) error {
	if visited[dir.LBA] {
		return fmt.Errorf("%s: directory loop at sector %d", destDir, dir.LBA)
	}
	visited[dir.LBA] = true
	defer delete(visited, dir.LBA)
	entries, err := t.img.ReadDir(dir)
	if err != nil {
		return err
	}

	for _, e := range entries {
		dest := path.Join(destDir, e.Name)
		if e.IsDir {
			if err := extractDir(fs, t, e, dest, visited); err != nil {
				return err
			}
			continue
		}

		if err := extractFile(fs, t, e, dest); err != nil {
			return fmt.Errorf("%s: %w", dest, err)
		}
	}

	return nil
}

// sourceModified falls back to Epoch when a source has no modification
// time of its own (§8.2).
func sourceModified(t time.Time) time.Time {
	if t.IsZero() {
		return Epoch
	}
	return t
}
