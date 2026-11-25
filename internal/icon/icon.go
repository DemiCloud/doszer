// Package icon makes an archive's DOSZ.ICO (spec §6.8.4) from an ICO file
// or a program's icon group (NE or PE resources), and decodes an ICO file
// to an image for doszer's window.
package icon

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Resource types (winuser.h).
const (
	rtIcon      = 3
	rtGroupIcon = 14
)

// maxResource bounds a resource's size: an icon image is at most a 256x256
// 32-bit bitmap with its mask, about 270 KB; a PNG one is smaller.
const maxResource = 4 << 20

// IsICO reports whether data begins as an ICO file does: reserved 0, type
// 1 and at least one image.
func IsICO(data []byte) bool {
	return len(data) >= 6 && binary.LittleEndian.Uint16(data) == 0 && binary.LittleEndian.Uint16(data[2:]) == 1 && binary.LittleEndian.Uint16(data[4:]) > 0
}

// Make returns DOSZ.ICO for a file (spec §6.8.4): an ICO file unchanged,
// or a program's icon group number index as an ICO file.
func Make(r io.ReaderAt, size int64, index int) ([]byte, error) {
	head := make([]byte, min(size, 6))
	if _, err := r.ReadAt(head, 0); err != nil && err != io.EOF {
		return nil, err
	}
	if IsICO(head) {
		if size > maxResource {
			return nil, fmt.Errorf("ICO file of %d bytes is too large", size)
		}
		data := make([]byte, size)
		if _, err := r.ReadAt(data, 0); err != nil && err != io.EOF {
			return nil, err
		}
		return data, nil
	}
	return FromProgram(r, size, index)
}

// ErrNoIcon means a program has no icon group with the index asked for.
var ErrNoIcon = errors.New("no icon")

// resources finds a program's resources.
type resources interface {
	// groups lists the RT_GROUP_ICON resources in resource table order.
	groups() ([]res, error)
	// icon returns the RT_ICON resource with the given ID.
	icon(id uint16) (res, bool, error)
}

// res is a resource's place in the file.
type res struct{ off, size int64 }

// FromProgram returns a program's icon group number index (counted from
// 0, in resource table order) as an ICO file (spec §6.8.4). The program
// is an MZ executable with an NE or PE header.
func FromProgram(r io.ReaderAt, size int64, index int) ([]byte, error) {
	f := &file{r, size}
	if index < 0 {
		return nil, fmt.Errorf("icon index %d", index)
	}
	rs, err := f.open()
	if err != nil {
		return nil, err
	}
	groups, err := rs.groups()
	if err != nil {
		return nil, err
	}
	if index >= len(groups) {
		if len(groups) == 0 {
			return nil, fmt.Errorf("%w: the program has no icon resources", ErrNoIcon)
		}
		return nil, fmt.Errorf("%w: icon %d asked for, the program has %d", ErrNoIcon, index, len(groups))
	}
	dir, err := f.bytes(groups[index])
	if err != nil {
		return nil, err
	}
	if len(dir) < 6 {
		return nil, errors.New("icon group too short")
	}
	n := int(binary.LittleEndian.Uint16(dir[4:]))
	if len(dir) < 6+14*n {
		return nil, errors.New("icon group truncated")
	}

	type image struct {
		entry []byte // the group entry's first 12 bytes
		data  []byte
	}
	var images []image
	for i := range n {
		e := dir[6+14*i : 6+14*(i+1)]
		r, ok, err := rs.icon(binary.LittleEndian.Uint16(e[12:]))
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		data, err := f.bytes(r)
		if err != nil {
			return nil, err
		}
		images = append(images, image{e[:12], data})
	}
	if len(images) == 0 {
		return nil, fmt.Errorf("%w: icon %d has no images", ErrNoIcon, index)
	}

	out := make([]byte, 6, 6+16*len(images))
	binary.LittleEndian.PutUint16(out[2:], 1)
	binary.LittleEndian.PutUint16(out[4:], uint16(len(images)))
	off := 6 + 16*len(images)
	for _, im := range images {
		e := make([]byte, 16)
		copy(e, im.entry)
		binary.LittleEndian.PutUint32(e[8:], uint32(len(im.data)))
		binary.LittleEndian.PutUint32(e[12:], uint32(off))
		out = append(out, e...)
		off += len(im.data)
	}
	for _, im := range images {
		out = append(out, im.data...)
	}
	return out, nil
}

// file reads little-endian values from a program, bounds-checked.
type file struct {
	r    io.ReaderAt
	size int64
}

var errTruncated = errors.New("truncated program")

func (f *file) read(off int64, n int) ([]byte, error) {
	if off < 0 || n < 0 || off+int64(n) > f.size {
		return nil, errTruncated
	}
	b := make([]byte, n)
	if _, err := f.r.ReadAt(b, off); err != nil && !(err == io.EOF && off+int64(n) == f.size) {
		return nil, err
	}
	return b, nil
}

func (f *file) u16(off int64) (uint16, error) {
	b, err := f.read(off, 2)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint16(b), nil
}

func (f *file) u32(off int64) (uint32, error) {
	b, err := f.read(off, 4)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(b), nil
}

func (f *file) bytes(r res) ([]byte, error) {
	if r.size > maxResource {
		return nil, fmt.Errorf("resource of %d bytes is too large for an icon", r.size)
	}
	return f.read(r.off, int(r.size))
}

// open reads the MZ header and returns the NE or PE resources it leads to.
func (f *file) open() (resources, error) {
	mz, err := f.read(0, 64)
	if err != nil || mz[0] != 'M' || mz[1] != 'Z' {
		return nil, errors.New("neither an ICO file nor a program")
	}
	hdr := int64(binary.LittleEndian.Uint32(mz[0x3c:]))
	sig, err := f.read(hdr, 4)
	if err != nil {
		return nil, fmt.Errorf("%w: a DOS program has no icons", ErrNoIcon)
	}
	switch {
	case sig[0] == 'N' && sig[1] == 'E':
		return &ne{f: f, hdr: hdr}, nil
	case string(sig) == "PE\x00\x00":
		return newPE(f, hdr)
	}
	return nil, fmt.Errorf("%w: a DOS program has no icons", ErrNoIcon)
}

// ne is a 16-bit Windows (NE) program's resource table.
type ne struct {
	f   *file
	hdr int64
}

// each calls fn for every resource of type typ in table order, until fn
// returns false.
func (n *ne) each(typ uint16, fn func(id uint16, r res) bool) error {
	rel, err := n.f.u16(n.hdr + 0x24)
	if err != nil {
		return err
	}
	tab := n.hdr + int64(rel)
	shift, err := n.f.u16(tab)
	if err != nil {
		return err
	}
	if shift > 16 {
		return errors.New("NE resource table: bad alignment")
	}
	p := tab + 2
	for range 1 << 12 { // resource types
		t, err := n.f.u16(p)
		if err != nil {
			return err
		}
		if t == 0 {
			return nil
		}
		count, err := n.f.u16(p + 2)
		if err != nil {
			return err
		}
		p += 8
		for i := range int64(count) {
			e, err := n.f.read(p+12*i, 12)
			if err != nil {
				return err
			}
			if t == 0x8000|typ {
				r := res{int64(binary.LittleEndian.Uint16(e)) << shift, int64(binary.LittleEndian.Uint16(e[2:])) << shift}
				if !fn(binary.LittleEndian.Uint16(e[6:]), r) {
					return nil
				}
			}
		}
		p += 12 * int64(count)
	}
	return errors.New("NE resource table: too many types")
}

func (n *ne) groups() ([]res, error) {
	var out []res
	err := n.each(rtGroupIcon, func(_ uint16, r res) bool { out = append(out, r); return true })
	return out, err
}

func (n *ne) icon(id uint16) (res, bool, error) {
	var found res
	ok := false
	err := n.each(rtIcon, func(rid uint16, r res) bool {
		if rid == 0x8000|id {
			found, ok = r, true
			return false
		}
		return true
	})
	return found, ok, err
}

// pe is a 32- or 64-bit Windows (PE) program's resource directory.
type pe struct {
	f        *file
	root     int64 // file offset of the resource directory
	rootRVA  uint32
	sections []section
}

type section struct{ va, vsize, raw, rawSize uint32 }

func newPE(f *file, hdr int64) (*pe, error) {
	nsec, err := f.u16(hdr + 6)
	if err != nil {
		return nil, err
	}
	optSize, err := f.u16(hdr + 20)
	if err != nil {
		return nil, err
	}
	opt := hdr + 24
	magic, err := f.u16(opt)
	if err != nil {
		return nil, err
	}
	dirs := opt + 96 // PE32
	if magic == 0x20b {
		dirs = opt + 112 // PE32+
	}
	p := &pe{f: f}
	if dirs+8*3 > opt+int64(optSize) {
		return p, nil // no resource directory entry
	}
	if p.rootRVA, err = f.u32(dirs + 16); err != nil {
		return nil, err
	}
	secs := opt + int64(optSize)
	for i := range int64(nsec) {
		b, err := f.read(secs+40*i, 40)
		if err != nil {
			return nil, err
		}
		le := binary.LittleEndian
		p.sections = append(p.sections, section{le.Uint32(b[12:]), le.Uint32(b[8:]), le.Uint32(b[20:]), le.Uint32(b[16:])})
	}
	if p.rootRVA != 0 {
		off, ok := p.offset(p.rootRVA)
		if !ok {
			return nil, errors.New("PE resource directory outside every section")
		}
		p.root = off
	}
	return p, nil
}

// offset converts an RVA to a file offset.
func (p *pe) offset(rva uint32) (int64, bool) {
	for _, s := range p.sections {
		size := max(s.vsize, s.rawSize)
		if rva >= s.va && rva < s.va+size {
			return int64(s.raw) + int64(rva-s.va), true
		}
	}
	return 0, false
}

// entries returns a resource directory's entries: name or ID, and offset
// (high bit set for a subdirectory), named ones first as stored.
func (p *pe) entries(dir int64) ([][2]uint32, error) {
	named, err := p.f.u16(dir + 12)
	if err != nil {
		return nil, err
	}
	ids, err := p.f.u16(dir + 14)
	if err != nil {
		return nil, err
	}
	n := int(named) + int(ids)
	b, err := p.f.read(dir+16, 8*n)
	if err != nil {
		return nil, err
	}
	out := make([][2]uint32, n)
	for i := range out {
		out[i] = [2]uint32{binary.LittleEndian.Uint32(b[8*i:]), binary.LittleEndian.Uint32(b[8*i+4:])}
	}
	return out, nil
}

// typeDir returns the file offset of type typ's directory, or 0.
func (p *pe) typeDir(typ uint32) (int64, error) {
	if p.rootRVA == 0 {
		return 0, nil
	}
	es, err := p.entries(p.root)
	if err != nil {
		return 0, err
	}
	for _, e := range es {
		if e[0] == typ && e[1]&0x80000000 != 0 {
			return p.root + int64(e[1]&0x7fffffff), nil
		}
	}
	return 0, nil
}

// leaf follows a name directory entry to its first language's data.
func (p *pe) leaf(e [2]uint32) (res, error) {
	off := p.root + int64(e[1]&0x7fffffff)
	if e[1]&0x80000000 != 0 {
		langs, err := p.entries(off)
		if err != nil {
			return res{}, err
		}
		if len(langs) == 0 || langs[0][1]&0x80000000 != 0 {
			return res{}, errors.New("PE resource directory: no language entry")
		}
		off = p.root + int64(langs[0][1])
	}
	rva, err := p.f.u32(off)
	if err != nil {
		return res{}, err
	}
	size, err := p.f.u32(off + 4)
	if err != nil {
		return res{}, err
	}
	fo, ok := p.offset(rva)
	if !ok {
		return res{}, errors.New("PE resource outside every section")
	}
	return res{fo, int64(size)}, nil
}

func (p *pe) groups() ([]res, error) {
	dir, err := p.typeDir(rtGroupIcon)
	if err != nil || dir == 0 {
		return nil, err
	}
	es, err := p.entries(dir)
	if err != nil {
		return nil, err
	}
	var out []res
	for _, e := range es {
		r, err := p.leaf(e)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

func (p *pe) icon(id uint16) (res, bool, error) {
	dir, err := p.typeDir(rtIcon)
	if err != nil || dir == 0 {
		return res{}, false, err
	}
	es, err := p.entries(dir)
	if err != nil {
		return res{}, false, err
	}
	for _, e := range es {
		if e[0] == uint32(id) {
			r, err := p.leaf(e)
			return r, err == nil, err
		}
	}
	return res{}, false, nil
}
