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
//
// Extracted files stay in the temporary ISO images they came from, as
// ranges of them (rangeEntry), rather than being read into memory: the
// images are appended to *tempFiles for the caller to remove once the
// archive is written.
func applyExtracts(fs *fileSet, media map[string]resolvedMedia, extracts []manifest.Extract, tempFiles *[]*os.File) error {
	images := map[string]*iso9660.Image{}
	paths := map[string]string{}

	for _, ex := range extracts {
		img, ok := images[ex.Media]
		if !ok {
			var err error
			img, err = openDataTrack(media[ex.Media], tempFiles)
			if err != nil {
				return fmt.Errorf("extract: media %q: %w", ex.Media, err)
			}
			images[ex.Media] = img
			paths[ex.Media] = (*tempFiles)[len(*tempFiles)-1].Name()
		}

		entry, err := img.Lookup(ex.From)
		if err != nil {
			return fmt.Errorf("extract: media %q: %s: %w", ex.Media, ex.From, err)
		}

		if entry.IsDir {
			if err := extractDir(fs, img, paths[ex.Media], entry, ex.To); err != nil {
				return fmt.Errorf("extract: media %q: %w", ex.Media, err)
			}
			continue
		}

		if err := extractFile(fs, img, paths[ex.Media], entry, ex.To); err != nil {
			return fmt.Errorf("extract: media %q: %s: %w", ex.Media, ex.From, err)
		}
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

// extractFile adds e, a file in img (whose image file is imgPath), to fs
// at dest.
func extractFile(fs *fileSet, img *iso9660.Image, imgPath string, e iso9660.Entry, dest string) error {
	off, n, err := img.Extent(e)
	if err != nil {
		return err
	}
	info, err := os.Stat(imgPath)
	if err != nil {
		return err
	}
	if off+n > info.Size() {
		return fmt.Errorf("extends past the end of the image")
	}
	fs.set(dest, rangeEntry(imgPath, off, n, sourceModified(e.Modified)))
	return nil
}

// extractDir copies dir's contents into fs, recursively, rooted at destDir.
func extractDir(fs *fileSet, img *iso9660.Image, imgPath string, dir iso9660.Entry, destDir string) error {
	entries, err := img.ReadDir(dir)
	if err != nil {
		return err
	}

	for _, e := range entries {
		dest := path.Join(destDir, e.Name)
		if e.IsDir {
			if err := extractDir(fs, img, imgPath, e, dest); err != nil {
				return err
			}
			continue
		}

		if err := extractFile(fs, img, imgPath, e, dest); err != nil {
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
