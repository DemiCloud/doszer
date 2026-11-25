package icon

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/png"
	"sort"
	"testing"
)

var le = binary.LittleEndian

// dib returns a w x w 32-bit icon bitmap filled with c, its left half
// masked out (transparent) when c has no alpha.
func dib(w int, bpp int, c color.NRGBA) []byte {
	stride := (w*bpp + 31) / 32 * 4
	maskStride := (w + 31) / 32 * 4
	b := make([]byte, 40)
	le.PutUint32(b, 40)
	le.PutUint32(b[4:], uint32(w))
	le.PutUint32(b[8:], uint32(2*w))
	le.PutUint16(b[12:], 1)
	le.PutUint16(b[14:], uint16(bpp))
	if bpp == 8 {
		le.PutUint32(b[32:], 2)
		b = append(b, 0, 0, 0, 0, c.B, c.G, c.R, 0) // palette: black, c
	}
	for range w {
		row := make([]byte, stride)
		for x := range w {
			switch bpp {
			case 32:
				copy(row[4*x:], []byte{c.B, c.G, c.R, c.A})
			case 8:
				row[x] = 1
			}
		}
		b = append(b, row...)
	}
	for range w {
		row := make([]byte, maskStride)
		for x := range w / 2 {
			row[x/8] |= 0x80 >> (x % 8)
		}
		b = append(b, row...)
	}
	return b
}

// group returns an RT_GROUP_ICON resource for images of the given widths,
// bit depths and RT_ICON IDs. The size fields are wrong on purpose:
// §6.8.4 takes the RT_ICON resource's length instead.
func group(ws, bpps []int, ids []uint16) []byte {
	b := make([]byte, 6)
	le.PutUint16(b[2:], 1)
	le.PutUint16(b[4:], uint16(len(ws)))
	for i, w := range ws {
		e := make([]byte, 14)
		e[0], e[1] = byte(w), byte(w)
		le.PutUint16(e[4:], 1)
		le.PutUint16(e[6:], uint16(bpps[i]))
		le.PutUint32(e[8:], 12345)
		le.PutUint16(e[12:], ids[i])
		b = append(b, e...)
	}
	return b
}

type resource struct {
	id   uint16
	data []byte
}

// pe32 returns a PE program whose only section holds a resource
// directory with the given resources by type.
func pe32(types map[uint32][]resource) []byte {
	const rva, raw = 0x1000, 0x200
	var tids []uint32
	for t := range types {
		tids = append(tids, t)
	}
	sort.Slice(tids, func(i, j int) bool { return tids[i] < tids[j] })

	// Layout: root, type directories, language directories, data
	// entries, data.
	dirSize := func(n int) int { return 16 + 8*n }
	off := dirSize(len(tids))
	typeOff := map[uint32]int{}
	n := 0
	for _, t := range tids {
		typeOff[t] = off
		off += dirSize(len(types[t]))
		n += len(types[t])
	}
	langOff, entryOff := off, off+n*dirSize(1)
	dataOff := entryOff + 16*n
	rs := make([]byte, dataOff)
	dir := func(at, count int) {
		le.PutUint16(rs[at+14:], uint16(count))
	}
	ent := func(at, i int, name, to uint32) {
		le.PutUint32(rs[at+16+8*i:], name)
		le.PutUint32(rs[at+16+8*i+4:], to)
	}
	dir(0, len(tids))
	k := 0
	for i, t := range tids {
		ent(0, i, t, 0x80000000|uint32(typeOff[t]))
		dir(typeOff[t], len(types[t]))
		for j, r := range types[t] {
			lo := langOff + k*dirSize(1)
			eo := entryOff + 16*k
			ent(typeOff[t], j, uint32(r.id), 0x80000000|uint32(lo))
			dir(lo, 1)
			ent(lo, 0, 1033, uint32(eo))
			le.PutUint32(rs[eo:], uint32(rva+len(rs)))
			le.PutUint32(rs[eo+4:], uint32(len(r.data)))
			rs = append(rs, r.data...)
			k++
		}
	}

	b := make([]byte, raw)
	b[0], b[1] = 'M', 'Z'
	le.PutUint32(b[0x3c:], 0x40)
	copy(b[0x40:], "PE\x00\x00")
	le.PutUint16(b[0x44:], 0x14c)
	le.PutUint16(b[0x46:], 1)   // sections
	le.PutUint16(b[0x54:], 224) // optional header size
	opt := 0x58
	le.PutUint16(b[opt:], 0x10b)
	le.PutUint32(b[opt+96+16:], rva)
	le.PutUint32(b[opt+96+20:], uint32(len(rs)))
	sec := opt + 224
	copy(b[sec:], ".rsrc")
	le.PutUint32(b[sec+8:], uint32(len(rs)))
	le.PutUint32(b[sec+12:], rva)
	le.PutUint32(b[sec+16:], uint32(len(rs)))
	le.PutUint32(b[sec+20:], raw)
	return append(b, rs...)
}

// ne16 returns an NE program with the given resources by type, in the
// order given.
func ne16(types [][2]any) []byte {
	const shift = 4
	b := make([]byte, 0x80)
	b[0], b[1] = 'M', 'Z'
	le.PutUint32(b[0x3c:], 0x40)
	b[0x40], b[0x41] = 'N', 'E'
	le.PutUint16(b[0x40+0x24:], 0x40) // resource table at 0x80
	tab := []byte{shift, 0}
	type fix struct {
		at   int
		data []byte
	}
	var fixes []fix
	for _, t := range types {
		rs := t[1].([]resource)
		h := make([]byte, 8)
		le.PutUint16(h, 0x8000|uint16(t[0].(int)))
		le.PutUint16(h[2:], uint16(len(rs)))
		tab = append(tab, h...)
		for _, r := range rs {
			e := make([]byte, 12)
			le.PutUint16(e[6:], 0x8000|r.id)
			fixes = append(fixes, fix{len(tab), r.data})
			tab = append(tab, e...)
		}
	}
	tab = append(tab, 0, 0)
	b = append(b, tab...)
	for _, f := range fixes {
		for len(b)%(1<<shift) != 0 {
			b = append(b, 0)
		}
		e := 0x80 + f.at
		le.PutUint16(b[e:], uint16(len(b)>>shift))
		le.PutUint16(b[e+2:], uint16((len(f.data)+(1<<shift)-1)>>shift))
		b = append(b, f.data...)
	}
	for len(b)%(1<<shift) != 0 {
		b = append(b, 0)
	}
	return b
}

// ico is the ICO file §6.8.4 says a group of these images becomes.
func ico(ws, bpps []int, images ...[]byte) []byte {
	b := make([]byte, 6)
	le.PutUint16(b[2:], 1)
	le.PutUint16(b[4:], uint16(len(images)))
	off := 6 + 16*len(images)
	for i, im := range images {
		e := make([]byte, 16)
		e[0], e[1] = byte(ws[i]), byte(ws[i])
		le.PutUint16(e[4:], 1)
		le.PutUint16(e[6:], uint16(bpps[i]))
		le.PutUint32(e[8:], uint32(len(im)))
		le.PutUint32(e[12:], uint32(off))
		b = append(b, e...)
		off += len(im)
	}
	for _, im := range images {
		b = append(b, im...)
	}
	return b
}

func TestMake(t *testing.T) {
	red := color.NRGBA{255, 0, 0, 255}
	blue := color.NRGBA{0, 0, 255, 0}
	small, big, other := dib(16, 8, red), dib(32, 32, blue), dib(48, 32, red)
	// NE resources are stored padded to 16 bytes, and §6.8.4 takes the
	// resource's stored length.
	pad := func(b []byte) []byte {
		for len(b)%16 != 0 {
			b = append(b, 0)
		}
		return b
	}
	peFile := pe32(map[uint32][]resource{
		rtIcon:      {{1, small}, {2, big}, {7, other}},
		rtGroupIcon: {{100, group([]int{16, 32}, []int{8, 32}, []uint16{1, 2})}, {200, group([]int{48, 64}, []int{32, 32}, []uint16{7, 9})}},
	})
	neFile := ne16([][2]any{
		{rtGroupIcon, []resource{{5, group([]int{32}, []int{32}, []uint16{2})}, {3, group([]int{16}, []int{8}, []uint16{1})}}},
		{rtIcon, []resource{{1, small}, {2, big}}},
	})
	dos := append([]byte("MZ"), make([]byte, 200)...)
	given := ico([]int{16}, []int{8}, small)

	tests := []struct {
		name    string
		file    []byte
		index   int
		want    []byte
		err     error // errors.Is, or any error when wantErr
		wantErr bool
	}{
		{"ICO unchanged", given, 5, given, nil, false},
		{"PE first group", peFile, 0, ico([]int{16, 32}, []int{8, 32}, small, big), nil, false},
		{"PE second group, missing image left out", peFile, 1, ico([]int{48}, []int{32}, other), nil, false},
		{"PE index past the groups", peFile, 2, nil, ErrNoIcon, true},
		{"NE groups in table order, not by ID", neFile, 0, ico([]int{32}, []int{32}, pad(big)), nil, false},
		{"NE second group", neFile, 1, ico([]int{16}, []int{8}, pad(small)), nil, false},
		{"DOS program", dos, 0, nil, ErrNoIcon, true},
		{"PE without resources", pe32(nil), 0, nil, ErrNoIcon, true},
		{"not a program", []byte("hello, world"), 0, nil, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Make(bytes.NewReader(tt.file), int64(len(tt.file)), tt.index)
			if tt.wantErr {
				if err == nil || tt.err != nil && !errors.Is(err, tt.err) {
					t.Fatalf("err = %v, want %v", err, tt.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, tt.want) {
				t.Errorf("ICO differs:\n got %x\nwant %x", got[:min(64, len(got))], tt.want[:min(64, len(tt.want))])
			}
		})
	}
}

func TestDecode(t *testing.T) {
	red := color.NRGBA{255, 0, 0, 255}
	green := color.NRGBA{0, 255, 0, 128}
	var pngBuf bytes.Buffer
	pimg := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	for i := range pimg.Pix {
		pimg.Pix[i] = 0xff
	}
	png.Encode(&pngBuf, pimg)
	f := ico([]int{16, 32, 64}, []int{8, 32, 32}, dib(16, 8, red), dib(32, 32, green), pngBuf.Bytes())

	tests := []struct {
		size int
		w    int
		x, y int
		want color.NRGBA
	}{
		{16, 16, 0, 0, color.NRGBA{}},                    // masked out: transparent
		{16, 16, 15, 0, red},                             // palette colour, opaque
		{24, 32, 0, 0, green},                            // alpha channel used, mask ignored
		{48, 64, 3, 3, color.NRGBA{255, 255, 255, 255}},  // PNG
		{200, 64, 0, 0, color.NRGBA{255, 255, 255, 255}}, // none big enough: the largest
	}
	for _, tt := range tests {
		img, err := Decode(f, tt.size)
		if err != nil {
			t.Fatal(err)
		}
		if w := img.Bounds().Dx(); w != tt.w {
			t.Errorf("size %d: picked %d px, want %d", tt.size, w, tt.w)
			continue
		}
		got := color.NRGBAModel.Convert(img.At(tt.x, tt.y)).(color.NRGBA)
		if tt.want.A == 0 {
			if got.A != 0 {
				t.Errorf("size %d: (%d,%d) = %v, want transparent", tt.size, tt.x, tt.y, got)
			}
		} else if got != tt.want {
			t.Errorf("size %d: (%d,%d) = %v, want %v", tt.size, tt.x, tt.y, got, tt.want)
		}
	}
}
