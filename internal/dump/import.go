package dump

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/DemiCloud/doszer/internal/interrupt"
	"github.com/DemiCloud/doszer/internal/redump"
)

// Import copies a disc image (a CUE sheet and its files, in any layout,
// or a zip holding them) into dir exactly as Run would have dumped the same disc: disc<N>.cue
// and one raw file per track in Redump's layout (redump.OpenImage), so
// everything after it - the Redump check, init, build - is the same, and
// a verified image builds the same archive as a verified dump. The image
// itself is left as it is. offset is the drive read offset, in samples,
// that the image's audio wasn't corrected for (redump.OpenImage).
func Import(image, dir string, offset int) (*Result, error) {
	img, err := redump.OpenImage(image, offset)
	if err != nil {
		return nil, err
	}
	defer img.Close()
	tracks := img.Tracks
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	name := nextName(dir)
	res := &Result{}
	var written []string
	fail := func(err error) (*Result, error) {
		for _, p := range written {
			os.Remove(p)
		}
		return nil, err
	}
	for _, t := range tracks {
		p := filepath.Join(dir, redump.TrackFileName(name, t.Number, len(tracks)))
		if err := copyTrack(t, p); err != nil {
			return fail(err)
		}
		written = append(written, p)
		res.Tracks = append(res.Tracks, p)
	}
	res.Cue = filepath.Join(dir, name+".cue")
	if err := writeNew(res.Cue, func(w io.Writer) error {
		_, err := w.Write(redump.FormatImage(name, tracks))
		return err
	}); err != nil {
		return fail(err)
	}
	return res, nil
}

func copyTrack(t redump.ImageTrack, dst string) error {
	rc, err := t.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	return writeNew(dst, func(w io.Writer) error {
		n, err := io.Copy(w, rc)
		if err == nil && n != t.Size {
			err = fmt.Errorf("track %d: read %d of %d bytes", t.Number, n, t.Size)
		}
		return err
	})
}

// writeNew writes a file that mustn't exist yet, through a temporary file
// beside it, so an interrupted import leaves nothing half-written.
func writeNew(dst string, write func(io.Writer) error) error {
	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("%s already exists", dst)
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".doszer-import-*")
	if err != nil {
		return err
	}
	defer interrupt.Remove(tmp.Name())()
	err = write(tmp)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), dst)
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}
