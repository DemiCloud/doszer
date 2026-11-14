// Package vhd reads Microsoft Virtual Hard Disk (VHD) images, fixed and
// dynamic, as a flat io.ReaderAt over the virtual disk - the OS images
// DOSBox Pure boots (README "Install"). Differencing disks are rejected,
// as DOSBox Pure itself does (src/ints/bios_disk.cpp, sparseVhd::Init).
//
// Format: Microsoft "Virtual Hard Disk Image Format Specification" 1.0.
// All fields are big-endian.
package vhd

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
)

// SectorSize is the VHD sector size; offsets and sizes in the format are in
// bytes, but blocks, bitmaps and BAT entries are counted in sectors.
const SectorSize = 512

// Disk types (footer "Disk Type" field).
const (
	TypeFixed        = 2
	TypeDynamic      = 3
	TypeDifferencing = 4
)

// ErrNotVHD reports a file without a VHD footer: possibly a raw disk image.
var ErrNotVHD = errors.New("not a VHD image (no conectix footer)")

const unallocated = 0xFFFFFFFF

// Disk is an open VHD image. It is safe for concurrent use.
type Disk struct {
	r        io.ReaderAt
	closer   io.Closer
	diskType uint32
	size     int64 // virtual disk size in bytes

	// Dynamic disks only.
	blockSize     int64
	bitmapSectors int64
	bat           []uint32 // sector offset of each block, or unallocated

	mu      sync.Mutex
	bitmaps map[int][]byte // block index -> sector bitmap
}

// Open opens a VHD file.
func Open(path string) (*Disk, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	d, err := New(f, info.Size())
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	d.closer = f
	return d, nil
}

// New reads a VHD image of fileSize bytes from r.
func New(r io.ReaderAt, fileSize int64) (*Disk, error) {
	if fileSize < SectorSize {
		return nil, ErrNotVHD
	}
	ft, err := readFooter(r, fileSize-SectorSize)
	if err != nil && fileSize >= 2*SectorSize {
		// The format keeps a copy of a dynamic disk's footer at offset 0,
		// so a truncated or damaged trailing footer is recoverable.
		if copy, copyErr := readFooter(r, 0); copyErr == nil && copy.diskType != TypeFixed {
			ft, err = copy, nil
		}
	}
	if err != nil {
		return nil, err
	}

	d := &Disk{r: r, diskType: ft.diskType, size: int64(ft.currentSize)}
	switch ft.diskType {
	case TypeFixed:
		if d.size > fileSize-SectorSize {
			return nil, fmt.Errorf("fixed VHD: disk size %d exceeds file data %d", d.size, fileSize-SectorSize)
		}
		return d, nil
	case TypeDynamic:
		if err := d.readDynamicHeader(int64(ft.dataOffset)); err != nil {
			return nil, err
		}
		return d, nil
	case TypeDifferencing:
		return nil, errors.New("differencing VHDs are not supported")
	default:
		return nil, fmt.Errorf("unknown VHD disk type %d", ft.diskType)
	}
}

// Close closes the underlying file, if Open opened it.
func (d *Disk) Close() error {
	if d.closer != nil {
		return d.closer.Close()
	}
	return nil
}

// Size returns the virtual disk's size in bytes.
func (d *Disk) Size() int64 { return d.size }

// Type returns the disk type: TypeFixed or TypeDynamic.
func (d *Disk) Type() uint32 { return d.diskType }

// ReadAt reads the virtual disk. Unallocated blocks, and sectors a dynamic
// block's bitmap marks unused, read as zeros (as DOSBox Pure reads them:
// src/ints/bios_disk.cpp, sparseVhd::SeeBlock).
func (d *Disk) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("vhd: negative offset")
	}
	if off >= d.size {
		return 0, io.EOF
	}
	want := len(p)
	if int64(want) > d.size-off {
		p = p[:d.size-off]
	}

	var n int
	var err error
	if d.diskType == TypeFixed {
		n, err = d.r.ReadAt(p, off)
	} else {
		n, err = d.readDynamic(p, off)
	}
	if err == nil && n < want {
		err = io.EOF
	}
	return n, err
}

// readDynamic reads p at off, one run of same-state sectors at a time.
func (d *Disk) readDynamic(p []byte, off int64) (int, error) {
	n := 0
	for n < len(p) {
		pos := off + int64(n)
		block := int(pos / d.blockSize)
		inBlock := pos % d.blockSize
		chunk := min(int64(len(p)-n), d.blockSize-inBlock)

		if block >= len(d.bat) || d.bat[block] == unallocated {
			clear(p[n : n+int(chunk)])
			n += int(chunk)
			continue
		}

		bitmap, err := d.bitmap(block)
		if err != nil {
			return n, err
		}
		// Within the block, take the longest run of sectors sharing the
		// first sector's bitmap bit, then read or zero it as one piece.
		sector := inBlock / SectorSize
		used := bitSet(bitmap, sector)
		end := inBlock + chunk
		runEnd := (sector + 1) * SectorSize
		for runEnd < end && bitSet(bitmap, runEnd/SectorSize) == used {
			runEnd += SectorSize
		}
		chunk = min(runEnd, end) - inBlock

		dst := p[n : n+int(chunk)]
		if used {
			dataStart := (int64(d.bat[block]) + d.bitmapSectors) * SectorSize
			if _, err := d.r.ReadAt(dst, dataStart+inBlock); err != nil {
				return n, fmt.Errorf("vhd: block %d: %w", block, err)
			}
		} else {
			clear(dst)
		}
		n += int(chunk)
	}
	return n, nil
}

func bitSet(bitmap []byte, sector int64) bool {
	return bitmap[sector/8]&(0x80>>(sector%8)) != 0
}

// bitmap returns (and caches) a block's sector bitmap.
func (d *Disk) bitmap(block int) ([]byte, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if b, ok := d.bitmaps[block]; ok {
		return b, nil
	}
	b := make([]byte, d.bitmapSectors*SectorSize)
	if _, err := d.r.ReadAt(b, int64(d.bat[block])*SectorSize); err != nil {
		return nil, fmt.Errorf("vhd: block %d bitmap: %w", block, err)
	}
	d.bitmaps[block] = b
	return b, nil
}

type footer struct {
	dataOffset  uint64
	currentSize uint64
	diskType    uint32
}

// readFooter reads and checks the 512-byte footer at off.
func readFooter(r io.ReaderAt, off int64) (footer, error) {
	b := make([]byte, SectorSize)
	if _, err := r.ReadAt(b, off); err != nil {
		return footer{}, err
	}
	if string(b[0:8]) != "conectix" {
		return footer{}, ErrNotVHD
	}
	if err := checkChecksum(b, 64); err != nil {
		return footer{}, fmt.Errorf("footer: %w", err)
	}
	be := binary.BigEndian
	return footer{
		dataOffset:  be.Uint64(b[16:]),
		currentSize: be.Uint64(b[48:]),
		diskType:    be.Uint32(b[60:]),
	}, nil
}

// readDynamicHeader reads the 1024-byte dynamic disk header at off and the
// block allocation table it points to.
func (d *Disk) readDynamicHeader(off int64) error {
	b := make([]byte, 1024)
	if _, err := d.r.ReadAt(b, off); err != nil {
		return fmt.Errorf("dynamic header: %w", err)
	}
	if string(b[0:8]) != "cxsparse" {
		return errors.New("dynamic header: missing cxsparse cookie")
	}
	if err := checkChecksum(b, 36); err != nil {
		return fmt.Errorf("dynamic header: %w", err)
	}
	be := binary.BigEndian
	tableOffset := int64(be.Uint64(b[16:]))
	entries := int64(be.Uint32(b[28:]))
	blockSize := int64(be.Uint32(b[32:]))
	if blockSize == 0 || blockSize%SectorSize != 0 {
		return fmt.Errorf("dynamic header: bad block size %d", blockSize)
	}
	if need := (d.size + blockSize - 1) / blockSize; entries < need {
		return fmt.Errorf("dynamic header: %d BAT entries cover less than the %d-byte disk", entries, d.size)
	}

	raw := make([]byte, entries*4)
	if _, err := d.r.ReadAt(raw, tableOffset); err != nil {
		return fmt.Errorf("block allocation table: %w", err)
	}
	d.bat = make([]uint32, entries)
	for i := range d.bat {
		d.bat[i] = be.Uint32(raw[i*4:])
	}

	sectorsPerBlock := blockSize / SectorSize
	d.blockSize = blockSize
	d.bitmapSectors = (sectorsPerBlock/8 + SectorSize - 1) / SectorSize
	d.bitmaps = map[int][]byte{}
	return nil
}

// checkChecksum verifies a footer's or dynamic header's checksum: the
// one's complement of the sum of every byte, with the 4-byte checksum
// field at at treated as zero.
func checkChecksum(b []byte, at int) error {
	want := binary.BigEndian.Uint32(b[at:])
	if got := Checksum(b, at); got != want {
		return fmt.Errorf("checksum %08x, want %08x", got, want)
	}
	return nil
}

// Checksum computes a footer's or dynamic header's checksum, skipping the
// checksum field at offset at.
func Checksum(b []byte, at int) uint32 {
	var sum uint32
	for i, c := range b {
		if i >= at && i < at+4 {
			continue
		}
		sum += uint32(c)
	}
	return ^sum
}
