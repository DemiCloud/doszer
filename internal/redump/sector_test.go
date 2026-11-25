package redump

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"io"
	"testing"
)

// TestMode1: rebuilt sectors match real discs' raw sectors. The vectors
// are zero-data sectors of a Redump dump (no game data), at the disc's
// first address and at 12:15:25, in a data track's postgap; the encoder
// also matched every sector of two whole Mode 1 data tracks (241,575
// sectors) when written.
func TestMode1(t *testing.T) {
	for _, tt := range []struct {
		lba  int64
		want string
	}{
		{0, "d9ee3c3ce5f425e5adb786706f65f363fd785695"},
		{55000, "aa0cbd49dc97a2c8a48a57e7c5948bfbc5dbed22"},
		{55001, "3b6b1d6b08aaf43baf044cfc3a0d1f2e614a2949"},
	} {
		sec := make([]byte, 2352)
		Mode1(sec, tt.lba, make([]byte, 2048))
		if got := sha1.Sum(sec); hex.EncodeToString(got[:]) != tt.want {
			t.Errorf("lba %d: %x, want %s", tt.lba, got, tt.want)
		}
	}

	// mode1Reader streams the same sectors, and ignores a partial one.
	data := bytes.Repeat([]byte{0xA5}, 2048*3+100)
	got, err := io.ReadAll(&mode1Reader{r: bytes.NewReader(data), lba: 7})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3*2352 {
		t.Fatalf("%d bytes, want 3 sectors", len(got))
	}
	want := make([]byte, 2352)
	for i := range 3 {
		Mode1(want, int64(7+i), data[i*2048:(i+1)*2048])
		if !bytes.Equal(got[i*2352:(i+1)*2352], want) {
			t.Errorf("sector %d differs", i)
		}
	}
}
