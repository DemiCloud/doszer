package redump

import (
	"encoding/binary"
	"io"
)

// A Mode 1 sector (ECMA-130 §14): 12 sync bytes, a 4-byte header (the
// sector's address as minutes, seconds and frames in BCD, then mode 1),
// 2048 bytes of user data, a 4-byte EDC over everything before it, 8 zero
// bytes, then 172 bytes of P parity and 104 of Q parity (a Reed-Solomon
// product code over the header, data, EDC and zeros). All of it follows
// from the address and the user data, which is what a 2048-byte track (an
// ISO) keeps: Mode1 rebuilds the raw sector Redump hashes.

var (
	edcTable [256]uint32
	eccF     [256]byte
	eccB     [256]byte
)

func init() {
	for i := range 256 {
		edc := uint32(i)
		for range 8 {
			if edc&1 != 0 {
				edc = edc>>1 ^ 0xD8018001
			} else {
				edc >>= 1
			}
		}
		edcTable[i] = edc
		j := byte(i << 1)
		if i&0x80 != 0 {
			j ^= 0x1D
		}
		eccF[i] = j
		eccB[byte(i)^j] = byte(i)
	}
}

func edc(b []byte) uint32 {
	var e uint32
	for _, c := range b {
		e = e>>8 ^ edcTable[byte(e)^c]
	}
	return e
}

// eccBlock computes one parity set over s (the sector from its header):
// majors codewords of minors bytes each, stepping majorMult between
// codewords and minorInc within one, into dst (2*majors bytes).
func eccBlock(s []byte, majors, minors, majorMult, minorInc int, dst []byte) {
	size := majors * minors
	for major := range majors {
		index := (major>>1)*majorMult + major&1
		var a, b byte
		for range minors {
			t := s[index]
			index += minorInc
			if index >= size {
				index -= size
			}
			a ^= t
			b ^= t
			a = eccF[a]
		}
		a = eccB[eccF[a]^b]
		dst[major] = a
		dst[major+majors] = a ^ b
	}
}

// Mode1 writes into sec (2352 bytes) the raw Mode 1 sector holding data
// (2048 bytes) at logical block lba (0 is the disc's first data sector,
// address 00:02:00).
func Mode1(sec []byte, lba int64, data []byte) {
	copy(sec, "\x00\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\x00")
	a := lba + 150
	bcd := func(n int64) byte { return byte(n/10<<4 | n%10) }
	sec[12], sec[13], sec[14], sec[15] = bcd(a/4500), bcd(a/75%60), bcd(a%75), 1
	copy(sec[16:2064], data)
	binary.LittleEndian.PutUint32(sec[2064:], edc(sec[:2064]))
	clear(sec[2068:2076])
	eccBlock(sec[12:], 86, 24, 2, 86, sec[2076:])
	eccBlock(sec[12:], 52, 43, 86, 88, sec[2248:])
}

// mode1Reader reads 2048-byte sectors from r as raw Mode 1 sectors, the
// first at lba.
type mode1Reader struct {
	r    io.Reader
	lba  int64
	data [2048]byte
	sec  [2352]byte
	buf  []byte
}

func (m *mode1Reader) Read(p []byte) (int, error) {
	if len(m.buf) == 0 {
		if _, err := io.ReadFull(m.r, m.data[:]); err != nil {
			if err == io.ErrUnexpectedEOF {
				err = io.EOF // a partial last sector isn't a sector
			}
			return 0, err
		}
		Mode1(m.sec[:], m.lba, m.data[:])
		m.lba++
		m.buf = m.sec[:]
	}
	n := copy(p, m.buf)
	m.buf = m.buf[n:]
	return n, nil
}
