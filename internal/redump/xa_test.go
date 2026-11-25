package redump

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"io"
	"testing"
	"testing/fstest"
)

// TestMode2Form1: rebuilt sectors match a Mode 2 disc's raw ones. The
// vectors are zero-data sectors of a Redump dump (no game data); the
// encoder and xaSubmodes also matched every sector of that disc's data
// track (168,065) when written.
func TestMode2Form1(t *testing.T) {
	for _, tt := range []struct {
		lba  int64
		want string
	}{
		{0, "851f1f1315a2b712030098587f4fe8ebcaf8833b"},
		{168060, "443160e541b3073c6fe9ccb044c5af422fcef034"},
	} {
		sec := make([]byte, 2352)
		Mode2Form1(sec, tt.lba, subNone, make([]byte, 2048))
		if got := sha1.Sum(sec); hex.EncodeToString(got[:]) != tt.want {
			t.Errorf("lba %d: %x, want %s", tt.lba, got, tt.want)
		}
	}
}

// xaISO builds an ISO 9660 image: a primary descriptor, a terminator, an
// L and an M path table, a root directory with a subdirectory placed
// before the root (so xaSubmodes needs a second pass) and two files.
func xaISO() []byte {
	const ss = 2048
	img := make([]byte, 40*ss)
	le, be := binary.LittleEndian, binary.BigEndian
	both32 := func(b []byte, v uint32) { le.PutUint32(b, v); be.PutUint32(b[4:], v) }
	record := func(lba, size uint32, flags byte, id string) []byte {
		r := make([]byte, 33+len(id)+(1-len(id)%2))
		r[0] = byte(len(r))
		both32(r[2:], lba)
		both32(r[10:], size)
		r[25] = flags
		r[32] = byte(len(id))
		copy(r[33:], id)
		return r
	}
	dir := func(at, self, parent uint32, entries ...[]byte) {
		d := img[at*ss:]
		p := copy(d, record(self, ss, 2, "\x00"))
		p += copy(d[p:], record(parent, ss, 2, "\x01"))
		for _, e := range entries {
			p += copy(d[p:], e)
		}
	}
	pvd := img[16*ss:]
	pvd[0] = 1
	copy(pvd[1:], "CD001")
	both32(pvd[80:], 40)
	le.PutUint32(pvd[132:], 10) // path table size
	le.PutUint32(pvd[140:], 19)
	be.PutUint32(pvd[148:], 20)
	copy(pvd[156:], record(22, ss, 2, "\x00"))
	img[17*ss] = 255
	copy(img[17*ss+1:], "CD001")
	dir(22, 22, 22, record(21, ss, 2, "SUB"), record(25, 3*ss-100, 0, "A.DAT;1"))
	dir(21, 21, 22, record(30, 10, 0, "B.DAT;1"))
	for i := 25; i < 31; i++ {
		img[i*ss] = 0xAA // file data
	}
	img[35*ss] = 0xBB // data no structure uses
	return img
}

func TestXASubmodes(t *testing.T) {
	iso := xaISO()
	open := func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(iso)), nil }
	got, err := xaSubmodes(open, int64(len(iso)/2048), 0)
	if err != nil {
		t.Fatal(err)
	}
	want := make([]byte, 40)
	want[16], want[17] = subEOR, subEOREF   // descriptor, terminator
	want[19], want[20] = subEOREF, subEOREF // path tables
	want[21], want[22] = subEOREF, subEOREF // directories
	want[25], want[26], want[27] = subData, subData, subEOREF
	want[30] = subEOREF
	if !bytes.Equal(got, want) {
		t.Errorf("submodes\n%x\nwant\n%x", got, want)
	}

	// Through an image: the Mode 2 hash follows the predicted submodes,
	// guessing data for a used-looking sector no structure claims.
	tr := ImageTrack{Number: 1, Mode: "MODE1/2048", Size: 40 * 2352, fsys: fstest.MapFS{"d.iso": &fstest.MapFile{Data: iso}}, parts: []part{{name: "d.iso", size: int64(len(iso)), mode1: true}}}
	_, alt, size, err := tr.hash()
	if err != nil {
		t.Fatal(err)
	}
	h := sha1.New()
	sec := make([]byte, 2352)
	for i := 0; i < 40; i++ {
		sub := want[i]
		if sub == subNone && !allZero(iso[i*2048:(i+1)*2048]) {
			sub = subData // 28, 29 and 35
		}
		Mode2Form1(sec, int64(i), sub, iso[i*2048:(i+1)*2048])
		h.Write(sec)
	}
	if alt != hex.EncodeToString(h.Sum(nil)) || size != 40*2352 {
		t.Errorf("Mode 2 hash %s, size %d", alt, size)
	}
}
