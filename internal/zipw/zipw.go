// Package zipw writes deterministic ZIP archives per spec §8.
package zipw

import (
	"archive/zip"
	"bytes"
	"hash/crc32"
	"io"
	"os"
	"time"
)

// dosEpoch and dosMaxYear bound what MS-DOS date/time encoding can
// represent; a modification time outside this range is clamped to the
// nearer bound rather than producing a garbage encoding.
var dosEpoch = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)

const dosMaxYear = 2107

// spoolLimit is how much compressed data Add holds in memory before
// spilling it to a temporary file.
const spoolLimit = 32 << 20

// Writer writes a deterministic ZIP archive.
//
// Every entry is written with its CRC-32 and sizes in its local header and
// no data descriptor, whether Add compressed it or AddRaw copied it
// through: an entry's bytes don't depend on how it got there, so repacking
// an archive reproduces it exactly.
type Writer struct {
	zw *zip.Writer

	// TempDir holds Add's temporary files for compressed data larger than
	// spoolLimit; empty means os.TempDir.
	TempDir string
}

// New returns a Writer that writes to w.
func New(w io.Writer) *Writer {
	return &Writer{zw: zip.NewWriter(w)}
}

// Add writes one entry with the given modification time (§8.2), using
// Store if store is set, else Deflate at flate.BestCompression, in parallel
// (archive/zip's own Deflate compressor uses level 5). The CRC-32 and sizes
// must precede the data, so a stored entry from an io.ReadSeeker is read
// twice, and anything else is first written to a spool.
func (w *Writer) Add(name string, r io.Reader, store bool, modified time.Time) error {
	if rs, ok := r.(io.ReadSeeker); ok && store {
		start, err := rs.Seek(0, io.SeekCurrent)
		if err != nil {
			return err
		}
		h := crc32.NewIEEE()
		n, err := io.Copy(h, rs)
		if err != nil {
			return err
		}
		if _, err := rs.Seek(start, io.SeekStart); err != nil {
			return err
		}
		return w.AddRaw(name, zip.Store, h.Sum32(), uint64(n), uint64(n), io.LimitReader(rs, n), modified)
	}

	sp := &spool{dir: w.TempDir}
	defer sp.close()
	h := crc32.NewIEEE()
	method := uint16(zip.Store)
	var n int64
	var err error
	if store {
		n, err = io.Copy(io.MultiWriter(sp, h), r)
	} else {
		method = zip.Deflate
		n, err = deflate(sp, r, h)
	}
	if err != nil {
		return err
	}
	body, err := sp.reader()
	if err != nil {
		return err
	}
	return w.AddRaw(name, method, h.Sum32(), uint64(sp.size), uint64(n), body, modified)
}

// spool collects bytes in memory, moving them to a temporary file once
// they exceed spoolLimit.
type spool struct {
	dir  string
	buf  bytes.Buffer
	f    *os.File
	size int64
}

func (s *spool) Write(p []byte) (int, error) {
	s.size += int64(len(p))
	if s.f == nil && s.buf.Len()+len(p) > spoolLimit {
		f, err := os.CreateTemp(s.dir, ".doszer-spool-*")
		if err != nil {
			return 0, err
		}
		s.f = f
		if _, err := s.buf.WriteTo(f); err != nil {
			return 0, err
		}
	}
	if s.f != nil {
		return s.f.Write(p)
	}
	return s.buf.Write(p)
}

// reader returns everything written, from the start.
func (s *spool) reader() (io.Reader, error) {
	if s.f == nil {
		return &s.buf, nil
	}
	if _, err := s.f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	return s.f, nil
}

func (s *spool) close() {
	if s.f != nil {
		s.f.Close()
		os.Remove(s.f.Name())
	}
}

// AddRaw writes one entry whose content is already compressed, copying it
// through unchanged rather than compressing it: method, crc32 and both
// sizes must already describe body exactly, as they would for the zip.File
// an already-compressed entry was read from.
func (w *Writer) AddRaw(name string, method uint16, crc32 uint32, compressedSize, uncompressedSize uint64, body io.Reader, modified time.Time) error {
	fh := &zip.FileHeader{
		Name:               name,
		Method:             method,
		CRC32:              crc32,
		CompressedSize64:   compressedSize,
		UncompressedSize64: uncompressedSize,
	}
	fh.ExternalAttrs = 0
	// Set the legacy DOS fields directly, not Modified: archive/zip adds an
	// extended-timestamp extra field whenever Modified is non-zero, which
	// §8.2 forbids ("omit extra fields").
	fh.ModifiedDate, fh.ModifiedTime = toDOSTime(modified)

	fw, err := w.zw.CreateRaw(fh)
	if err != nil {
		return err
	}
	_, err = io.Copy(fw, body)
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
