package build

import (
	"fmt"
	"io"
	"os"
	"path"
	"time"

	"github.com/DemiCloud/doszer/internal/cue"
	"github.com/DemiCloud/doszer/internal/iso9660"
	"github.com/DemiCloud/doszer/internal/manifest"
)

// applyExtracts performs every [[extract]] entry, in order (§5.7, §6.1):
// each copies a file or directory out of a media's data track filesystem
// into the build.
func applyExtracts(fs *fileSet, media map[string]resolvedMedia, extracts []manifest.Extract) error {
	images := map[string]*iso9660.Image{}
	var tempFiles []*os.File
	defer func() {
		for _, f := range tempFiles {
			f.Close()
			os.Remove(f.Name())
		}
	}()

	for _, ex := range extracts {
		img, ok := images[ex.Media]
		if !ok {
			var err error
			img, err = openDataTrack(media[ex.Media], &tempFiles)
			if err != nil {
				return fmt.Errorf("extract: media %q: %w", ex.Media, err)
			}
			images[ex.Media] = img
		}

		entry, err := img.Lookup(ex.From)
		if err != nil {
			return fmt.Errorf("extract: media %q: %s: %w", ex.Media, ex.From, err)
		}

		if entry.IsDir {
			if err := extractDir(fs, img, entry, ex.To); err != nil {
				return fmt.Errorf("extract: media %q: %w", ex.Media, err)
			}
			continue
		}

		data, err := extractFile(img, entry)
		if err != nil {
			return fmt.Errorf("extract: media %q: %s: %w", ex.Media, ex.From, err)
		}
		fs.set(ex.To, dataEntry(data, sourceModified(entry.Modified)))
	}

	return nil
}

// openDataTrack materializes a media's first data track as a temporary ISO
// image and opens it. The temp file is appended to *tempFiles for the
// caller to clean up once every extract using it is done.
func openDataTrack(m resolvedMedia, tempFiles *[]*os.File) (*iso9660.Image, error) {
	track, err := firstDataTrack(m.Tracks)
	if err != nil {
		return nil, err
	}

	tmp, err := os.CreateTemp("", "doszer-media-*.iso")
	if err != nil {
		return nil, err
	}
	*tempFiles = append(*tempFiles, tmp)

	rc, err := m.openTrack(track)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	r, err := track.Normalize(rc)
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(tmp, r); err != nil {
		return nil, fmt.Errorf("extracting data track: %w", err)
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}

	return iso9660.Open(tmp)
}

func firstDataTrack(tracks []cue.ResolvedTrack) (cue.ResolvedTrack, error) {
	for _, t := range tracks {
		if !t.Track.IsAudio() {
			return t, nil
		}
	}
	return cue.ResolvedTrack{}, fmt.Errorf("no data track")
}

func extractFile(img *iso9660.Image, e iso9660.Entry) ([]byte, error) {
	r, err := img.OpenFile(e)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(r)
}

// extractDir copies dir's contents into fs, recursively, rooted at destDir.
func extractDir(fs *fileSet, img *iso9660.Image, dir iso9660.Entry, destDir string) error {
	entries, err := img.ReadDir(dir)
	if err != nil {
		return err
	}

	for _, e := range entries {
		dest := path.Join(destDir, e.Name)
		if e.IsDir {
			if err := extractDir(fs, img, e, dest); err != nil {
				return err
			}
			continue
		}

		data, err := extractFile(img, e)
		if err != nil {
			return fmt.Errorf("%s: %w", dest, err)
		}
		fs.set(dest, dataEntry(data, sourceModified(e.Modified)))
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
