// Package zipw writes deterministic ZIP archives per spec §8.
package zipw

import (
	"archive/zip"
	"io"
)

// 1980-01-01 00:00:00 in MS-DOS date/time encoding (§8.2). Set directly on
// ModifiedDate/ModifiedTime, not Modified: archive/zip's CreateHeader always
// adds an extended-timestamp extra field when Modified is set, which §8.2
// forbids.
const (
	epochDate uint16 = 0x21 // day=1, month=1<<5, (year-1980)<<9
	epochTime uint16 = 0x00
)

// Writer writes a deterministic ZIP archive.
type Writer struct {
	zw *zip.Writer
}

// New returns a Writer that writes to w.
func New(w io.Writer) *Writer {
	return &Writer{zw: zip.NewWriter(w)}
}

// Add writes one entry, using Store if store is set, else Deflate.
func (w *Writer) Add(name string, r io.Reader, store bool) error {
	method := zip.Deflate
	if store {
		method = zip.Store
	}

	fh := &zip.FileHeader{
		Name:         name,
		Method:       method,
		ModifiedDate: epochDate,
		ModifiedTime: epochTime,
	}
	fh.ExternalAttrs = 0

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
