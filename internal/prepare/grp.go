package prepare

import (
	"bytes"
	"encoding/binary"
)

// grpItem is one Program Manager item.
type grpItem struct {
	name, command, iconPath string
}

// Icon placeholder: 32x32, one bit per pixel, fully transparent. Program
// Manager re-extracts an item's icon from its icon path when the group's
// icon format doesn't match the display's, as one bit never does.
const (
	grpIconSize = 32
	grpRowBytes = grpIconSize / 8
	grpPlane    = grpRowBytes * grpIconSize // AND and XOR planes, 128 bytes each
)

// buildGRP writes a Program Manager group file (PMCC, the Windows 3.1
// format Windows 9x's Program Manager reads): a header, each item with its
// icon and strings, then the tag section. Every 16-bit word of the file
// sums to zero, which the checksum field makes so. Layout as in a group
// Windows 98's Program Manager wrote: header fields at fixed offsets, 24
// bytes per item, a 12-byte icon header (hot spot, size, row bytes,
// planes, bits per pixel), the AND plane, then the XOR plane.
func buildGRP(name string, items []grpItem) []byte {
	le := binary.LittleEndian
	var b bytes.Buffer
	w16 := func(v int) { binary.Write(&b, le, uint16(v)) }

	header := 34 + 2*len(items)
	b.Write(make([]byte, header))
	str := func(s string) int {
		off := b.Len()
		b.WriteString(s)
		b.WriteByte(0)
		return off
	}
	pName := str(name)

	offsets := make([]int, len(items))
	for i, it := range items {
		offsets[i] = b.Len()
		at := b.Len()
		b.Write(make([]byte, 24))
		pHeader := b.Len()
		w16(grpIconSize / 2) // hot spot
		w16(grpIconSize / 2)
		w16(grpIconSize) // size
		w16(grpIconSize)
		w16(grpRowBytes)
		b.WriteByte(1) // planes
		b.WriteByte(1) // bits per pixel
		pAND := b.Len()
		b.Write(bytes.Repeat([]byte{0xFF}, grpPlane))
		pXOR := b.Len()
		b.Write(make([]byte, grpPlane))
		pItem := str(it.name)
		pCommand := str(it.command)
		pIcon := str(it.iconPath)

		f := b.Bytes()[at:]
		le.PutUint16(f[0:], uint16(21+75*i)) // position in the group window
		le.PutUint16(f[2:], 0)
		le.PutUint16(f[4:], 0) // icon index
		le.PutUint16(f[6:], 12)
		le.PutUint16(f[8:], grpPlane)
		le.PutUint16(f[10:], grpPlane)
		for j, v := range []int{pHeader, pAND, pXOR, pItem, pCommand, pIcon} {
			le.PutUint16(f[12+2*j:], uint16(v))
		}
	}
	if b.Len()%2 == 1 {
		b.WriteByte(0)
	}
	cbGroup := b.Len()

	// Tags: the PMCC tag, then the end tag.
	w16(0x8000)
	w16(0xFFFF)
	w16(10)
	b.WriteString("PMCC")
	w16(0xFFFF)
	w16(0xFFFF)
	w16(0)

	out := b.Bytes()
	copy(out[0:], "PMCC")
	le.PutUint16(out[6:], uint16(cbGroup))
	le.PutUint16(out[8:], 1) // nCmdShow: normal
	width := 21 + 75*len(items) + 40
	for i, v := range []int{0, 0, width, 386} { // normal window rectangle
		le.PutUint16(out[10+2*i:], uint16(v))
	}
	le.PutUint16(out[18:], 0xFFFF) // minimized position: none
	le.PutUint16(out[20:], 0xFFFF)
	le.PutUint16(out[22:], uint16(pName))
	le.PutUint16(out[24:], grpIconSize)
	le.PutUint16(out[26:], grpIconSize)
	out[28], out[29] = 1, 1 // icon format: bits per pixel, planes
	le.PutUint16(out[32:], uint16(len(items)))
	for i, off := range offsets {
		le.PutUint16(out[34+2*i:], uint16(off))
	}
	var sum uint16
	for i := 0; i < len(out); i += 2 {
		sum += le.Uint16(out[i:])
	}
	le.PutUint16(out[4:], -sum)
	return out
}
