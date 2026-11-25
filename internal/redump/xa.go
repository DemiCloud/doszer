package redump

import (
	"encoding/binary"
	"fmt"
	"io"
)

// Submodes of the sectors of a Mode 2 Form 1 PC disc, as mastering tools
// write them and as its ISO 9660 file system predicts them.
const (
	subNone  byte = 0x00 // a sector no structure uses (system area, gaps, padding)
	subData  byte = 0x08 // data
	subEOR   byte = 0x09 // data, end of record: a volume descriptor
	subEOREF byte = 0x89 // data, end of record and of file: the last sector of a file, a directory or a path table, and the descriptor set terminator
)

// xaSubmodes predicts each sector's submode for a data track of frames
// 2048-byte sectors, the first at lba, from its ISO 9660 file system: the
// volume descriptors (primary and Joliet), their path tables, and every
// directory and file extent (Mode2Form1). It reads the track with open,
// from its start, as far as its directories go, again if a directory
// lies before one already read.
//
// It matched every subheader of a Mode 2 PC disc (Recoil: 168,065
// sectors, two directory trees) when written. A sector no structure uses
// but holding data is guessed as data.
func xaSubmodes(open func() (io.ReadCloser, error), frames, lba int64) ([]byte, error) {
	sub := make([]byte, frames)
	mark := func(ext, size int64) {
		n := (size + 2047) / 2048
		for i := ext; i < ext+n; i++ {
			if i >= 0 && i < frames {
				sub[i] = subData
			}
		}
		if last := ext + n - 1; n > 0 && last >= 0 && last < frames {
			sub[last] = subEOREF
		}
	}
	idx := func(abs uint32) int64 { return int64(abs) - lba }

	wanted := map[int64]bool{} // directory sectors still to read
	dirs := map[int64]bool{}   // directory extents seen
	addDir := func(ext, size int64) {
		if dirs[ext] {
			return
		}
		dirs[ext] = true
		mark(ext, size)
		for i := ext; i < ext+(size+2047)/2048 && i < frames; i++ {
			wanted[i] = true
		}
	}
	vd, vdDone := int64(16), false
	buf := make([]byte, 2048)
	for pass := 0; pass < 8; pass++ {
		rc, err := open()
		if err != nil {
			return nil, err
		}
		passed := false // a wanted sector lay behind this pass
		for i := int64(0); i < frames; i++ {
			if vdDone && len(wanted) == 0 {
				break
			}
			if _, err := io.ReadFull(rc, buf); err != nil {
				rc.Close()
				return nil, fmt.Errorf("sector %d: %w", i, err)
			}
			if !vdDone && i == vd {
				switch {
				case buf[0] == 255 && string(buf[1:6]) == "CD001":
					sub[i], vdDone = subEOREF, true
				case string(buf[1:6]) == "CD001":
					sub[i] = subEOR
					if buf[0] == 1 || buf[0] == 2 {
						ptSize := int64(binary.LittleEndian.Uint32(buf[132:]))
						for _, loc := range []uint32{binary.LittleEndian.Uint32(buf[140:]), binary.LittleEndian.Uint32(buf[144:]), binary.BigEndian.Uint32(buf[148:]), binary.BigEndian.Uint32(buf[152:])} {
							if loc != 0 {
								mark(idx(loc), ptSize)
							}
						}
						root := buf[156:]
						addDir(idx(binary.LittleEndian.Uint32(root[2:])), int64(binary.LittleEndian.Uint32(root[10:])))
					}
					vd++
				default:
					vdDone = true // no descriptor set: not ISO 9660
				}
			}
			if !wanted[i] {
				continue
			}
			delete(wanted, i)
			for p := 0; p < 2048 && buf[p] != 0; p += int(buf[p]) {
				r := buf[p:]
				if len(r) < 34 || int(r[0]) < 34 || p+int(r[0]) > 2048 {
					break
				}
				if r[32] == 1 && (r[33] == 0 || r[33] == 1) {
					continue // . and ..
				}
				ext, size := idx(binary.LittleEndian.Uint32(r[2:])), int64(binary.LittleEndian.Uint32(r[10:]))
				if r[25]&2 != 0 {
					before := len(wanted)
					addDir(ext, size)
					if len(wanted) > before && ext <= i {
						passed = true
					}
				} else if size > 0 {
					mark(ext, size)
				}
			}
		}
		rc.Close()
		if !passed || len(wanted) == 0 {
			break
		}
	}
	return sub, nil
}
