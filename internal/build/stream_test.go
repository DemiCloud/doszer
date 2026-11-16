package build

import (
	"bytes"
	"io"
	"sync/atomic"
	"testing"

	"github.com/DemiCloud/doszer/internal/manifest"
)

// countingSource is a RawSource over data that, like a deflated archive
// entry, can only be streamed from its start, and counts the bytes read.
func countingSource(data []byte, read *atomic.Int64) *RawSource {
	return &RawSource{
		Method:           8,
		UncompressedSize: uint64(len(data)),
		Decompressed: func() ([]byte, error) {
			read.Add(int64(len(data)))
			return data, nil
		},
		Open: func() (io.ReadCloser, error) {
			return io.NopCloser(&countingReader{r: bytes.NewReader(data), n: read}), nil
		},
	}
}

type countingReader struct {
	r io.Reader
	n *atomic.Int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	k, err := c.r.Read(p)
	c.n.Add(int64(k))
	return k, err
}

// A three-track single-BIN disc: data, then two audio tracks with INDEX 00
// pregaps.
func multiTrackDisc() (bin []byte, sheet string, tracks [][]byte) {
	data := cat(mode1Sector(1), mode1Sector(2))
	a2, a3 := audioFrames(4, 7), audioFrames(5, 9)
	bin = cat(data, audioFrames(2, 0), a2, audioFrames(2, 0), a3)
	sheet = "FILE \"GAME.BIN\" BINARY\r\n" +
		"  TRACK 01 MODE1/2352\r\n    INDEX 01 00:00:00\r\n" +
		"  TRACK 02 AUDIO\r\n    INDEX 00 00:00:02\r\n    INDEX 01 00:00:04\r\n" +
		"  TRACK 03 AUDIO\r\n    INDEX 00 00:00:08\r\n    INDEX 01 00:00:10\r\n"
	userData := cat(bytes.Repeat([]byte{1}, 2048), bytes.Repeat([]byte{2}, 2048))
	return bin, sheet, [][]byte{userData, a2, a3}
}

func TestStreamedSourceDecompressedOnce(t *testing.T) {
	bin, sheet, tracks := multiTrackDisc()

	t.Run("hashing", func(t *testing.T) {
		var read atomic.Int64
		fs := newFileSet()
		fs.set("GAME.CUE", dataEntry([]byte(sheet), Epoch))
		fs.set("GAME.BIN", sourceEntry(countingSource(bin, &read), Epoch))
		media := []manifest.Media{{ID: "disc1", Tracks: []manifest.Track{
			{Type: manifest.TrackData, SHA1: sha1Hex(tracks[0])},
			{Type: manifest.TrackAudio, SHA1: sha1Hex(tracks[1])},
			{Type: manifest.TrackAudio, SHA1: sha1Hex(tracks[2])},
		}}}
		resolved, warnings, err := locateMedia(fs, media, nil)
		if err != nil || len(warnings) > 0 || resolved["disc1"].Image != "GAME.CUE" {
			t.Fatalf("locateMedia = %v, %v, %v", resolved, warnings, err)
		}
		if got := read.Load(); got != int64(len(bin)) {
			t.Errorf("decompressed %d bytes to hash a %d-byte image", got, len(bin))
		}
	})

	t.Run("converting", func(t *testing.T) {
		var read atomic.Int64
		fs := newFileSet()
		fs.set("GAME.CUE", dataEntry([]byte(sheet), Epoch))
		fs.set("GAME.BIN", sourceEntry(countingSource(bin, &read), Epoch))
		if _, _, err := convertMedia(fs, manifest.AudioWAV, 6, false, tempDirFunc(t)); err != nil {
			t.Fatal(err)
		}
		if got := read.Load(); got != int64(len(bin)) {
			t.Errorf("decompressed %d bytes to convert a %d-byte image", got, len(bin))
		}
		if got := content(t, fs, "GAME.iso"); !bytes.Equal(got, tracks[0]) {
			t.Error("GAME.iso differs from the data track's user data")
		}
		for i, name := range []string{"GAME (Track 02).wav", "GAME (Track 03).wav"} {
			if got := content(t, fs, name); !bytes.Equal(got[44:], tracks[i+1]) {
				t.Errorf("%s differs from the track's samples", name)
			}
		}
	})
}

func TestOpenRangeOutOfOrder(t *testing.T) {
	// Ranges read backwards, overlapping, or left part-read must still
	// yield the right bytes.
	data := make([]byte, 1000)
	for i := range data {
		data[i] = byte(i)
	}
	var read atomic.Int64
	e := sourceEntry(countingSource(data, &read), Epoch)
	for _, r := range []struct{ off, n, consume int64 }{
		{100, 50, 50}, {200, 100, 10}, {150, 100, 100}, {0, 10, 10}, {900, 200, 100}, {300, 10, 10},
	} {
		rc, err := e.openRange(r.off, r.n)
		if err != nil {
			t.Fatal(err)
		}
		got := make([]byte, r.consume)
		if _, err := io.ReadFull(rc, got); err != nil {
			t.Fatalf("range %d+%d: %v", r.off, r.n, err)
		}
		if !bytes.Equal(got, data[r.off:r.off+r.consume]) {
			t.Errorf("range %d+%d: wrong bytes", r.off, r.n)
		}
		rc.Close()
	}
}
