package redump

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A three-track disc: a data track, an audio track with a 2-frame pregap
// (silence), and an audio track without one, whose last frame is silent
// (so a shifted read's zero fill matches it).
var (
	discData   = frames(20, 'D')
	discPregap = make([]byte, 2*2352)
	discAudio2 = frames(5, 'A')
	discAudio3 = append(frames(3, 'B'), make([]byte, 2352)...)
)

func frames(n int, c byte) []byte {
	b := make([]byte, n*2352)
	for i := range b {
		b[i] = c + byte(i%7)
	}
	return b
}

// wantTracks is the disc in Redump's layout.
func wantTracks() [][]byte {
	return [][]byte{discData, append(append([]byte(nil), discPregap...), discAudio2...), discAudio3}
}

func write(t *testing.T, dir, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

const splitCue = `FILE "d (Track 1).bin" BINARY
  TRACK 01 MODE1/2352
    INDEX 01 00:00:00
FILE "d (Track 2).bin" BINARY
  TRACK 02 AUDIO
    INDEX 00 00:00:00
    INDEX 01 00:00:02
FILE "d (Track 3).bin" BINARY
  TRACK 03 AUDIO
    INDEX 01 00:00:00
`

func writeSplit(t *testing.T, dir string) string {
	w := wantTracks()
	for i, b := range w {
		write(t, dir, "d (Track "+string(rune('1'+i))+").bin", b)
	}
	write(t, dir, "d.cue", []byte(splitCue))
	return filepath.Join(dir, "d.cue")
}

func cat(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

func TestOpenImageLayouts(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, dir string) string
	}{
		{"one file per track", writeSplit},
		{"one file, INDEX 00 stored", func(t *testing.T, dir string) string {
			write(t, dir, "d.bin", cat(discData, discPregap, discAudio2, discAudio3))
			write(t, dir, "d.cue", []byte(`FILE "d.bin" BINARY
  TRACK 01 MODE1/2352
    INDEX 01 00:00:00
  TRACK 02 AUDIO
    INDEX 00 00:00:20
    INDEX 01 00:00:22
  TRACK 03 AUDIO
    INDEX 01 00:00:27
`))
			return filepath.Join(dir, "d.cue")
		}},
		{"one file, PREGAP not stored", func(t *testing.T, dir string) string {
			write(t, dir, "d.bin", cat(discData, discAudio2, discAudio3))
			write(t, dir, "d.cue", []byte(`FILE "d.bin" BINARY
  TRACK 01 MODE1/2352
    INDEX 01 00:00:00
  TRACK 02 AUDIO
    PREGAP 00:00:02
    INDEX 01 00:00:20
  TRACK 03 AUDIO
    INDEX 01 00:00:25
`))
			return filepath.Join(dir, "d.cue")
		}},
		{"zip", func(t *testing.T, dir string) string {
			src := t.TempDir()
			writeSplit(t, src)
			zpath := filepath.Join(dir, "d.zip")
			f, _ := os.Create(zpath)
			zw := zip.NewWriter(f)
			for _, n := range []string{"d.cue", "d (Track 1).bin", "d (Track 2).bin", "d (Track 3).bin"} {
				b, _ := os.ReadFile(filepath.Join(src, n))
				w, _ := zw.Create(n)
				w.Write(b)
			}
			zw.Close()
			f.Close()
			return zpath
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			img, err := OpenImage(tt.setup(t, t.TempDir()), 0)
			if err != nil {
				t.Fatal(err)
			}
			defer img.Close()
			checkTracks(t, img, wantTracks())
			if got := string(FormatImage("d", img.Tracks)); got != strings.ReplaceAll(splitCue, "\n", "\r\n") {
				t.Errorf("FormatImage =\n%s\nwant\n%s", got, splitCue)
			}
		})
	}
}

func checkTracks(t *testing.T, img *Image, want [][]byte) {
	t.Helper()
	if len(img.Tracks) != len(want) {
		t.Fatalf("%d tracks, want %d", len(img.Tracks), len(want))
	}
	for i, tr := range img.Tracks {
		rc, err := tr.Open()
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		if int64(len(got)) != tr.Size || !bytes.Equal(got, want[i]) {
			t.Errorf("track %d: %d bytes (Size %d), want %d bytes, equal %v", tr.Number, len(got), tr.Size, len(want[i]), bytes.Equal(got, want[i]))
		}
	}
}

// An image whose audio wasn't corrected for a +3 sample read offset holds
// every audio sample 3 samples later than the disc; reading it with
// offset 3 gives the disc's tracks.
func TestOpenImageOffset(t *testing.T) {
	const k = 3
	audio := cat(discPregap, discAudio2, discAudio3)
	shifted := cat(make([]byte, k*4), audio)[:len(audio)]
	dir := t.TempDir()
	write(t, dir, "d.bin", cat(discData, shifted))
	write(t, dir, "d.cue", []byte(`FILE "d.bin" BINARY
  TRACK 01 MODE1/2352
    INDEX 01 00:00:00
  TRACK 02 AUDIO
    INDEX 00 00:00:20
    INDEX 01 00:00:22
  TRACK 03 AUDIO
    INDEX 01 00:00:27
`))
	img, err := OpenImage(filepath.Join(dir, "d.cue"), k)
	if err != nil {
		t.Fatal(err)
	}
	checkTracks(t, img, wantTracks())

	// Uncorrected, the audio tracks differ.
	img, _ = OpenImage(filepath.Join(dir, "d.cue"), 0)
	rc, _ := img.Tracks[2].Open()
	got, _ := io.ReadAll(rc)
	if bytes.Equal(got, discAudio3) {
		t.Error("uncorrected track 3 matches the disc")
	}
}

func TestOpenImageRejects(t *testing.T) {
	tests := []struct{ name, cue, want string }{
		{"lossy audio", "FILE \"a.ogg\" OGG\n  TRACK 01 AUDIO\n    INDEX 01 00:00:00\n", "can't reproduce"},
		{"cooked data", "FILE \"a.ogg\" BINARY\n  TRACK 01 MODE1/2048\n    INDEX 01 00:00:00\n", "2048-byte"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, dir, "a.ogg", make([]byte, 4096))
			write(t, dir, "d.cue", []byte(tt.cue))
			if _, err := OpenImage(filepath.Join(dir, "d.cue"), 0); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestTrackFileName(t *testing.T) {
	tests := []struct {
		n, count int
		want     string
	}{
		{1, 1, "disc1.bin"},
		{3, 5, "disc1 (Track 3).bin"},
		{3, 12, "disc1 (Track 03).bin"},
	}
	for _, tt := range tests {
		if got := TrackFileName("disc1", tt.n, tt.count); got != tt.want {
			t.Errorf("TrackFileName(%d, %d) = %q, want %q", tt.n, tt.count, got, tt.want)
		}
	}
}
