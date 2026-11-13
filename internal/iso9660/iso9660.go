// Package iso9660 reads ISO 9660 (ECMA-119) filesystems, enough to resolve
// and extract a path from a disc image's data track (spec §5.7). It parses
// leniently: some real-world images have directory records strict parsers
// reject.
package iso9660

import (
	"encoding/binary"
	"fmt"
	"io"
	"strings"
	"time"
)

// sectorSize is the logical block size assumed throughout: 2048 bytes, the
// size of the data track's user data (spec §6.4), which is what doszer
// always reads this package from.
const sectorSize = 2048

// Entry is one file or directory in the filesystem.
type Entry struct {
	Name     string // without any ";N" version suffix
	IsDir    bool
	LBA      uint32
	Size     uint32
	Modified time.Time // zero if the directory record's date is unset or invalid
}

// Image is an opened ISO 9660 filesystem.
type Image struct {
	r    io.ReaderAt
	root Entry
}

// Open reads the volume descriptors from r and locates the root directory.
func Open(r io.ReaderAt) (*Image, error) {
	for lba := 16; ; lba++ {
		sector := make([]byte, sectorSize)
		if _, err := r.ReadAt(sector, int64(lba)*sectorSize); err != nil {
			return nil, fmt.Errorf("volume descriptor at sector %d: %w", lba, err)
		}
		if string(sector[1:6]) != "CD001" {
			return nil, fmt.Errorf("sector %d: not a CD001 volume descriptor", lba)
		}

		switch sector[0] {
		case 1: // Primary Volume Descriptor
			root, err := parseDirRecord(sector[156:190])
			if err != nil {
				return nil, fmt.Errorf("root directory record: %w", err)
			}
			return &Image{r: r, root: root}, nil
		case 255: // Volume Descriptor Set Terminator
			return nil, fmt.Errorf("no Primary Volume Descriptor found")
		default:
			continue // Boot Record, Supplementary (Joliet), etc.: skip
		}
	}
}

// Root returns the filesystem's root directory entry.
func (img *Image) Root() Entry { return img.root }

// ReadDir lists a directory's entries, excluding "." and "..".
func (img *Image) ReadDir(dir Entry) ([]Entry, error) {
	if !dir.IsDir {
		return nil, fmt.Errorf("%s: not a directory", dir.Name)
	}

	data := make([]byte, dir.Size)
	if _, err := img.r.ReadAt(data, int64(dir.LBA)*sectorSize); err != nil {
		return nil, fmt.Errorf("directory extent at LBA %d: %w", dir.LBA, err)
	}

	var entries []Entry
	pos := 0
	for pos < len(data) {
		length := int(data[pos])
		if length == 0 {
			// Padding to the next sector boundary (common and valid).
			pos = ((pos / sectorSize) + 1) * sectorSize
			continue
		}
		if pos+length > len(data) {
			break // truncated record at the end of the extent: stop leniently
		}

		e, err := parseDirRecord(data[pos : pos+length])
		if err != nil {
			pos += length
			continue // skip a malformed record rather than fail the whole read
		}
		if e.Name != "\x00" && e.Name != "\x01" { // not "." or ".."
			entries = append(entries, e)
		}
		pos += length
	}

	return entries, nil
}

// Lookup resolves a "/"-separated, case-insensitive path from the root.
func (img *Image) Lookup(path string) (Entry, error) {
	cur := img.root
	path = strings.Trim(path, "/")
	if path == "" {
		return cur, nil
	}

	for _, part := range strings.Split(path, "/") {
		entries, err := img.ReadDir(cur)
		if err != nil {
			return Entry{}, err
		}
		found := false
		for _, e := range entries {
			if strings.EqualFold(e.Name, part) {
				cur, found = e, true
				break
			}
		}
		if !found {
			return Entry{}, fmt.Errorf("%s: not found", path)
		}
	}
	return cur, nil
}

// OpenFile returns a reader over a file entry's content.
func (img *Image) OpenFile(e Entry) (io.Reader, error) {
	if e.IsDir {
		return nil, fmt.Errorf("%s: is a directory", e.Name)
	}
	return io.NewSectionReader(img.r, int64(e.LBA)*sectorSize, int64(e.Size)), nil
}

// dirFlag is the File Flags bit marking a directory record (ECMA-119 §9.1.6).
const dirFlag = 0x02

// parseDirRecord parses one Directory Record (ECMA-119 §9.1).
func parseDirRecord(b []byte) (Entry, error) {
	if len(b) < 34 {
		return Entry{}, fmt.Errorf("directory record too short (%d bytes)", len(b))
	}

	idLen := int(b[32])
	if 33+idLen > len(b) {
		return Entry{}, fmt.Errorf("directory record identifier overruns its record")
	}

	lba := binary.LittleEndian.Uint32(b[2:6])
	size := binary.LittleEndian.Uint32(b[10:14])
	flags := b[25]
	id := string(b[33 : 33+idLen])

	return Entry{
		Name:     stripVersion(id),
		IsDir:    flags&dirFlag != 0,
		LBA:      lba,
		Size:     size,
		Modified: parseRecordingTime(b[18:25]),
	}, nil
}

// stripVersion removes a file identifier's ";N" version suffix.
func stripVersion(id string) string {
	if i := strings.LastIndexByte(id, ';'); i != -1 {
		return id[:i]
	}
	return id
}

// parseRecordingTime parses a Directory Record's 7-byte recording
// date/time field (ECMA-119 §9.1.5): years since 1900, month, day, hour,
// minute, second, then a GMT offset in signed 15-minute intervals. Returns
// the zero time.Time if the field is unset or not a valid date - parsed
// leniently, same as the rest of this package.
func parseRecordingTime(b []byte) time.Time {
	if len(b) < 7 || (b[0] == 0 && b[1] == 0 && b[2] == 0) {
		return time.Time{}
	}

	year := 1900 + int(b[0])
	month := time.Month(b[1])
	day, hour, min, sec := int(b[2]), int(b[3]), int(b[4]), int(b[5])
	offset := int(int8(b[6])) * 15 * 60
	loc := time.FixedZone("", offset)

	t := time.Date(year, month, day, hour, min, sec, 0, loc)
	if t.Month() != month || t.Day() != day {
		return time.Time{} // out-of-range fields normalized away: not a real date
	}
	return t.UTC()
}
