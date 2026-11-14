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

// spoolLimit is how much compressed data Prepare holds in memory before
// spilling it to a temporary file. It is kept small because a caller may
// hold several prepared entries at once.
const spoolLimit = 8 << 20

// Writer writes a deterministic ZIP archive.
//
// Every entry is written with its CRC-32 and sizes in its local header and
// no data descriptor, whether Add compressed it or AddRaw copied it
// through: an entry's bytes don't depend on how it got there, so repacking
// an archive reproduces it exactly.
type Writer struct {
	zw *zip.Writer

	// TempDir holds temporary files for compressed data larger than
	// spoolLimit; empty means os.TempDir.
	TempDir string
}

// New returns a Writer that writes to w.
func New(w io.Writer) *Writer {
	return &Writer{zw: zip.NewWriter(w)}
}

// Add writes one entry with the given modification time (§8.2): Prepare,
// then WritePrepared.
func (w *Writer) Add(name string, r io.Reader, store bool, modified time.Time) error {
	p, err := w.Prepare(r, store)
	if err != nil {
		return err
	}
	return w.WritePrepared(name, p, modified)
}

// Prepared is an entry's content, compressed and checksummed, ready for
// WritePrepared. Preparing is safe to run concurrently, so a caller can
// compress upcoming entries while writing earlier ones in order.
type Prepared struct {
	method       uint16
	crc          uint32
	csize, usize int64
	body         io.Reader // the data to write: a spool, or the original io.ReadSeeker
	sp           *spool
}

// Prepare reads r, using Store if store is set, else Deflate at
// flate.BestCompression in parallel blocks (archive/zip's own Deflate
// compressor uses level 5). The CRC-32 and sizes must precede the data, so
// a stored entry from an io.ReadSeeker is read twice, and anything else is
// first written to a spool. r must stay open until WritePrepared (or
// Close) returns.
func (w *Writer) Prepare(r io.Reader, store bool) (*Prepared, error) {
	if rs, ok := r.(io.ReadSeeker); ok && store {
		start, err := rs.Seek(0, io.SeekCurrent)
		if err != nil {
			return nil, err
		}
		h := crc32.NewIEEE()
		n, err := io.Copy(h, rs)
		if err != nil {
			return nil, err
		}
		if _, err := rs.Seek(start, io.SeekStart); err != nil {
			return nil, err
		}
		return &Prepared{method: zip.Store, crc: h.Sum32(), csize: n, usize: n, body: io.LimitReader(rs, n)}, nil
	}

	sp := &spool{dir: w.TempDir}
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
	if err == nil {
		var body io.Reader
		if body, err = sp.reader(); err == nil {
			return &Prepared{method: method, crc: h.Sum32(), csize: sp.size, usize: n, body: body, sp: sp}, nil
		}
	}
	sp.close()
	return nil, err
}

// WritePrepared writes p as entry name, then releases it.
func (w *Writer) WritePrepared(name string, p *Prepared, modified time.Time) error {
	defer p.Close()
	return w.AddRaw(name, p.method, p.crc, uint64(p.csize), uint64(p.usize), p.body, modified)
}

// Close releases p's temporary storage without writing it.
func (p *Prepared) Close() {
	if p.sp != nil {
		p.sp.close()
	}
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
