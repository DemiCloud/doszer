package icon

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/png"
)

// Decode returns the best image of an ICO file for showing at about size
// pixels: the smallest at least that large, else the largest, preferring
// more colours at equal size. PNG and BMP (DIB, 1, 4, 8, 24 or 32 bits per
// pixel, with its AND mask) images are supported.
func Decode(ico []byte, size int) (image.Image, error) {
	if !IsICO(ico) {
		return nil, errors.New("not an ICO file")
	}
	n := int(binary.LittleEndian.Uint16(ico[4:]))
	type entry struct {
		w, bpp   int
		off, len uint32
	}
	var best *entry
	better := func(a, b *entry) bool { // a better than b
		if b == nil {
			return true
		}
		aBig, bBig := a.w >= size, b.w >= size
		switch {
		case aBig != bBig:
			return aBig
		case a.w != b.w:
			if aBig {
				return a.w < b.w
			}
			return a.w > b.w
		}
		return a.bpp > b.bpp
	}
	for i := range n {
		o := 6 + 16*i
		if o+16 > len(ico) {
			break
		}
		e := &entry{w: int(ico[o]), bpp: int(binary.LittleEndian.Uint16(ico[o+6:])), len: binary.LittleEndian.Uint32(ico[o+8:]), off: binary.LittleEndian.Uint32(ico[o+12:])}
		if e.w == 0 {
			e.w = 256
		}
		if uint64(e.off)+uint64(e.len) > uint64(len(ico)) {
			continue
		}
		if better(e, best) {
			best = e
		}
	}
	if best == nil {
		return nil, errors.New("ICO file has no readable image")
	}
	data := ico[best.off : best.off+best.len]
	if bytes.HasPrefix(data, []byte("\x89PNG")) {
		return png.Decode(bytes.NewReader(data))
	}
	return decodeDIB(data)
}

// decodeDIB decodes an icon's BITMAPINFOHEADER image: its height counts
// the colour bitmap and the 1-bit AND mask below it, both bottom-up, rows
// padded to 4 bytes.
func decodeDIB(d []byte) (image.Image, error) {
	le := binary.LittleEndian
	if len(d) < 40 {
		return nil, errors.New("icon bitmap too short")
	}
	hs := int(le.Uint32(d))
	w := int(int32(le.Uint32(d[4:])))
	h := int(int32(le.Uint32(d[8:]))) / 2
	bpp := int(le.Uint16(d[14:]))
	colors := int(le.Uint32(d[32:]))
	if w <= 0 || h <= 0 || w > 1024 || h > 1024 || hs < 40 || hs > len(d) {
		return nil, errors.New("icon bitmap: bad header")
	}
	var palette []color.NRGBA
	if bpp <= 8 {
		if colors == 0 {
			colors = 1 << bpp
		}
		p := hs
		if p+4*colors > len(d) {
			return nil, errors.New("icon bitmap: palette truncated")
		}
		for i := range colors {
			c := d[p+4*i:]
			palette = append(palette, color.NRGBA{c[2], c[1], c[0], 255})
		}
	} else if bpp != 24 && bpp != 32 {
		return nil, errors.New("icon bitmap: unsupported depth")
	}
	stride := (w*bpp + 31) / 32 * 4
	maskStride := (w + 31) / 32 * 4
	pix := hs + 4*len(palette)
	mask := pix + stride*h
	if mask > len(d) {
		return nil, errors.New("icon bitmap truncated")
	}
	hasMask := mask+maskStride*h <= len(d)
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	alpha := false // a 32-bit image whose alpha channel is used
	for y := range h {
		row := d[pix+stride*(h-1-y):]
		for x := range w {
			var c color.NRGBA
			switch bpp {
			case 32:
				c = color.NRGBA{row[4*x+2], row[4*x+1], row[4*x], row[4*x+3]}
				alpha = alpha || c.A != 0
			case 24:
				c = color.NRGBA{row[3*x+2], row[3*x+1], row[3*x], 255}
			default:
				bit := x * bpp
				i := int(row[bit/8]>>(8-bpp-bit%8)) & (1<<bpp - 1)
				if i < len(palette) {
					c = palette[i]
				}
			}
			img.SetNRGBA(x, y, c)
		}
	}
	for y := range h {
		for x := range w {
			o := img.PixOffset(x, y)
			switch {
			case bpp == 32 && alpha:
				// the alpha channel is the transparency
			case hasMask && d[mask+maskStride*(h-1-y)+x/8]>>(7-x%8)&1 == 1:
				img.Pix[o+3] = 0
			default:
				img.Pix[o+3] = 255
			}
		}
	}
	return img, nil
}

// Sizes returns an ICO file's image widths in pixels, in its order.
func Sizes(ico []byte) []int {
	if !IsICO(ico) {
		return nil
	}
	var out []int
	for i := range int(binary.LittleEndian.Uint16(ico[4:])) {
		o := 6 + 16*i
		if o+16 > len(ico) {
			break
		}
		w := int(ico[o])
		if w == 0 {
			w = 256
		}
		out = append(out, w)
	}
	return out
}
