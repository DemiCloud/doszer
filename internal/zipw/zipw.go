// Package zipw writes deterministic ZIP archives per spec §8.
package zipw

import (
	"archive/zip"
	"compress/flate"
	"io"
	"time"
)

// dosEpoch and dosMaxYear bound what MS-DOS date/time encoding can
// represent; a modification time outside this range is clamped to the
// nearer bound rather than producing a garbage encoding.
var dosEpoch = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)

const dosMaxYear = 2107

// Writer writes a deterministic ZIP archive.
type Writer struct {
	zw *zip.Writer
}

// New returns a Writer that writes to w.
func New(w io.Writer) *Writer {
	zw := zip.NewWriter(w)
	// archive/zip's own Deflate compressor uses flate level 5; use
	// BestCompression instead, scoped to this Writer only.
	zw.RegisterCompressor(zip.Deflate, func(w io.Writer) (io.WriteCloser, error) {
		return flate.NewWriter(w, flate.BestCompression)
	})
	return &Writer{zw: zw}
}

// Add writes one entry with the given modification time (§8.2), using
// Store if store is set, else Deflate.
func (w *Writer) Add(name string, r io.Reader, store bool, modified time.Time) error {
	method := zip.Deflate
	if store {
		method = zip.Store
	}

	fh := &zip.FileHeader{Name: name, Method: method}
	fh.ExternalAttrs = 0
	// Set the legacy DOS fields directly, not Modified: archive/zip's
	// CreateHeader always adds an extended-timestamp extra field whenever
	// Modified is non-zero, which §8.2 forbids ("omit extra fields").
	fh.ModifiedDate, fh.ModifiedTime = toDOSTime(modified)

	fw, err := w.zw.CreateHeader(fh)
	if err != nil {
		return err
	}
	_, err = io.Copy(fw, r)
	return err
}

// Close finishes writing the archive.
func (w *Writer) Close() error {
	return w.zw.Close()
}

// toDOSTime converts t to MS-DOS date/time encoding (2-second resolution,
// years 1980-2107), clamping t to that range first if needed.
func toDOSTime(t time.Time) (date, timeField uint16) {
	t = t.UTC()
	switch {
	case t.Before(dosEpoch):
		t = dosEpoch
	case t.Year() > dosMaxYear:
		t = time.Date(dosMaxYear, 12, 31, 23, 59, 58, 0, time.UTC)
	}

	date = uint16(t.Day() + int(t.Month())<<5 + (t.Year()-1980)<<9)
	timeField = uint16(t.Second()/2 + t.Minute()<<5 + t.Hour()<<11)
	return date, timeField
}
