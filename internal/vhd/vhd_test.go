package vhd

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"
)

// makeFooter builds a 512-byte footer for a disk of size bytes.
func makeFooter(diskType uint32, size, dataOffset uint64) []byte {
	b := make([]byte, SectorSize)
	be := binary.BigEndian
	copy(b, "conectix")
	be.PutUint32(b[8:], 2)
	be.PutUint32(b[12:], 0x00010000)
	be.PutUint64(b[16:], dataOffset)
	be.PutUint64(b[40:], size)
	be.PutUint64(b[48:], size)
	be.PutUint32(b[60:], diskType)
	be.PutUint32(b[64:], Checksum(b, 64))
	return b
}

// pattern returns n bytes of deterministic, non-zero test content.
func pattern(n int, seed byte) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*7) ^ seed | 1
	}
	return b
}

func makeFixed(data []byte) []byte {
	return append(append([]byte{}, data...), makeFooter(TypeFixed, uint64(len(data)), ^uint64(0))...)
}

// dynBlock is one block of a dynamic test image: its content, and which
// of its sectors the bitmap marks used (nil: all).
type dynBlock struct {
	data   []byte
	unused []int // sector indexes left clear in the bitmap, filled with garbage
}

// makeDynamic builds a dynamic VHD with blockSize-byte blocks; a nil entry
// in blocks is unallocated.
func makeDynamic(size int, blockSize int, blocks []*dynBlock) []byte {
	be := binary.BigEndian
	entries := (size + blockSize - 1) / blockSize
	batBytes := (entries*4 + SectorSize - 1) / SectorSize * SectorSize
	sectorsPerBlock := blockSize / SectorSize
	bitmapBytes := (sectorsPerBlock/8 + SectorSize - 1) / SectorSize * SectorSize

	ftr := makeFooter(TypeDynamic, uint64(size), SectorSize)
	hdr := make([]byte, 1024)
	copy(hdr, "cxsparse")
	be.PutUint64(hdr[8:], ^uint64(0))
	be.PutUint64(hdr[16:], 1536)
	be.PutUint32(hdr[24:], 0x00010000)
	be.PutUint32(hdr[28:], uint32(entries))
	be.PutUint32(hdr[32:], uint32(blockSize))
	be.PutUint32(hdr[36:], Checksum(hdr, 36))

	bat := bytes.Repeat([]byte{0xFF}, batBytes)
	var body []byte
	next := (1536 + batBytes) / SectorSize
	for i, blk := range blocks {
		if blk == nil {
			continue
		}
		be.PutUint32(bat[i*4:], uint32(next))
		bitmap := bytes.Repeat([]byte{0xFF}, bitmapBytes)
		data := append([]byte{}, blk.data...)
		data = append(data, make([]byte, blockSize-len(data))...)
		for _, s := range blk.unused {
			bitmap[s/8] &^= 0x80 >> (s % 8)
			copy(data[s*SectorSize:], bytes.Repeat([]byte{0xEE}, SectorSize))
		}
		body = append(append(body, bitmap...), data...)
		next += (bitmapBytes + blockSize) / SectorSize
	}

	out := append(append(append([]byte{}, ftr...), hdr...), bat...)
	return append(append(out, body...), ftr...)
}

func readAll(t *testing.T, d *Disk) []byte {
	t.Helper()
	b, err := io.ReadAll(io.NewSectionReader(d, 0, d.Size()))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRead(t *testing.T) {
	const bs = 4096 // 8 sectors per block
	blk0 := pattern(bs, 1)
	blk2 := pattern(bs, 2)
	wantSparse := append(append(append([]byte{}, blk0...), make([]byte, bs)...), blk2...)
	// Sectors 1 and 5 of block 2 are unused: zeros despite garbage on disk.
	copy(wantSparse[2*bs+1*SectorSize:], make([]byte, SectorSize))
	copy(wantSparse[2*bs+5*SectorSize:], make([]byte, SectorSize))

	tests := []struct {
		name     string
		image    []byte
		wantType uint32
		want     []byte
	}{
		{"fixed", makeFixed(pattern(3*SectorSize, 9)), TypeFixed, pattern(3*SectorSize, 9)},
		{
			"dynamic, unallocated block and unused sectors",
			makeDynamic(3*bs, bs, []*dynBlock{{data: blk0}, nil, {data: blk2, unused: []int{1, 5}}}),
			TypeDynamic, wantSparse,
		},
		{
			"dynamic, size not a whole number of blocks",
			makeDynamic(bs+SectorSize, bs, []*dynBlock{nil, {data: pattern(SectorSize, 3)}}),
			TypeDynamic, append(make([]byte, bs), pattern(SectorSize, 3)...),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := New(bytes.NewReader(tt.image), int64(len(tt.image)))
			if err != nil {
				t.Fatal(err)
			}
			if d.Type() != tt.wantType || d.Size() != int64(len(tt.want)) {
				t.Fatalf("type %d size %d, want %d %d", d.Type(), d.Size(), tt.wantType, len(tt.want))
			}
			if got := readAll(t, d); !bytes.Equal(got, tt.want) {
				t.Fatal("content differs")
			}
			// Unaligned reads spanning sector and block boundaries.
			for _, r := range [][2]int{{100, 1000}, {bs - 3, 10}, {len(tt.want) - 7, 7}} {
				if r[0] < 0 || r[0]+r[1] > len(tt.want) {
					continue
				}
				got := make([]byte, r[1])
				if _, err := d.ReadAt(got, int64(r[0])); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got, tt.want[r[0]:r[0]+r[1]]) {
					t.Errorf("ReadAt(%d, %d) differs", r[0], r[1])
				}
			}
			if _, err := d.ReadAt(make([]byte, 8), d.Size()-4); err != io.EOF {
				t.Errorf("read past end: err = %v, want io.EOF", err)
			}
		})
	}
}

func TestReject(t *testing.T) {
	badChecksum := makeFixed(make([]byte, SectorSize))
	badChecksum[len(badChecksum)-SectorSize+20]++

	diff := makeDynamic(4096, 4096, nil)
	for _, off := range []int{0, len(diff) - SectorSize} {
		binary.BigEndian.PutUint32(diff[off+60:], TypeDifferencing)
		binary.BigEndian.PutUint32(diff[off+64:], 0)
		binary.BigEndian.PutUint32(diff[off+64:], Checksum(diff[off:off+SectorSize], 64))
	}

	tests := []struct {
		name  string
		image []byte
		want  string
	}{
		{"raw image", make([]byte, 4*SectorSize), "not a VHD"},
		{"bad checksum", badChecksum, "checksum"},
		{"differencing", diff, "differencing"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(bytes.NewReader(tt.image), int64(len(tt.image)))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
	if _, err := New(bytes.NewReader(make([]byte, 1024)), 1024); !errors.Is(err, ErrNotVHD) {
		t.Errorf("raw image: err = %v, want ErrNotVHD", err)
	}
}

func TestDamagedTrailingFooterUsesCopy(t *testing.T) {
	img := makeDynamic(4096, 4096, []*dynBlock{{data: pattern(4096, 4)}})
	copy(img[len(img)-SectorSize:], make([]byte, SectorSize))
	d, err := New(bytes.NewReader(img), int64(len(img)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(readAll(t, d), pattern(4096, 4)) {
		t.Fatal("content differs")
	}
}

// TestFixedFooter: a dynamic disk's contents followed by FixedFooter
// open as the same disk, fixed.
func TestFixedFooter(t *testing.T) {
	data := bytes.Repeat([]byte{0xAB}, 512)
	img := makeDynamic(2<<20, 1<<20, []*dynBlock{{data: data}, nil})
	d, err := New(bytes.NewReader(img), int64(len(img)))
	if err != nil {
		t.Fatal(err)
	}
	raw := make([]byte, d.Size())
	if _, err := d.ReadAt(raw, 0); err != nil {
		t.Fatal(err)
	}
	fixed := append(raw, d.FixedFooter()...)
	f, err := New(bytes.NewReader(fixed), int64(len(fixed)))
	if err != nil {
		t.Fatal(err)
	}
	if f.Type() != TypeFixed || f.Size() != d.Size() {
		t.Fatalf("type %d size %d, want fixed, %d", f.Type(), f.Size(), d.Size())
	}
	got := make([]byte, f.Size())
	f.ReadAt(got, 0)
	if !bytes.Equal(got, raw) {
		t.Error("fixed copy reads differently")
	}
}
