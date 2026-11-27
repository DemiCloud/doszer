package runlog

import (
	"archive/zip"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"time"
)

// ReadArchive describes the archive at path as written: its size and
// hashes, and every entry's sizes, method and CRC-32 - what a ratio is
// worked out from.
func ReadArchive(path string) (*Archive, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	s, c := sha1.New(), crc32.NewIEEE()
	n, err := io.Copy(io.MultiWriter(s, c), f)
	if err != nil {
		return nil, err
	}
	zr, err := zip.NewReader(f, n)
	if err != nil {
		return nil, err
	}
	a := &Archive{Path: abs, Size: n, SHA1: hex.EncodeToString(s.Sum(nil)), CRC32: fmt.Sprintf("%08x", c.Sum32())}
	for _, e := range zr.File {
		method := "deflate"
		switch e.Method {
		case zip.Store:
			method = "store"
		case zip.Deflate:
		default:
			method = fmt.Sprintf("method %d", e.Method)
		}
		a.Entries = append(a.Entries, Entry{
			Path: e.Name, Size: int64(e.UncompressedSize64), Compressed: int64(e.CompressedSize64),
			Method: method, CRC32: fmt.Sprintf("%08x", e.CRC32), Modified: dosTime(e.ModifiedDate, e.ModifiedTime),
		})
		a.Content += int64(e.UncompressedSize64)
		a.Compressed += int64(e.CompressedSize64)
	}
	return a, nil
}

// dosTime is an entry's MS-DOS date and time, the only timestamp DOSBox
// Pure reads, as a time without a zone.
func dosTime(d, t uint16) time.Time {
	return time.Date(int(d>>9)+1980, time.Month(d>>5&0xf), int(d&0x1f), int(t>>11), int(t>>5&0x3f), int(t&0x1f)*2, 0, time.UTC)
}
