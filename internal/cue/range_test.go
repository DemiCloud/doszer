package cue

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// frame builds one raw frame of size bytes, filled with filler outside
// [offset:offset+length) and with data inside it.
func frame(size, offset, length int, filler byte, data []byte) []byte {
	f := make([]byte, size)
	for i := range f {
		f[i] = filler
	}
	copy(f[offset:offset+length], data)
	return f
}

func sectorData(n, seed int) []byte {
	d := make([]byte, 2048)
	for i := range d {
		d[i] = byte(seed + i)
	}
	return d
}

func writeFixture(t *testing.T, dir, name string, content []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), content, 0o644); err != nil {
		t.Fatal(err)
	}
}

func sha1Hex(b []byte) string {
	sum := sha1.Sum(b)
	return hex.EncodeToString(sum[:])
}

func TestResolveAndCopyCookedTrack(t *testing.T) {
	dir := t.TempDir()
	sector := sectorData(0, 1)
	writeFixture(t, dir, "game.bin", sector)

	sheet, err := Parse([]byte("FILE \"game.bin\" BINARY\nTRACK 01 MODE1/2048\nINDEX 01 00:00:00\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	tracks, err := Resolve(sheet, dir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(tracks) != 1 {
		t.Fatalf("got %d tracks, want 1", len(tracks))
	}
	rt := tracks[0]
	if rt.Start != 0 || rt.End != int64(len(sector)) {
		t.Errorf("range = [%d,%d), want [0,%d)", rt.Start, rt.End, len(sector))
	}

	var buf bytes.Buffer
	if err := rt.Copy(&buf); err != nil {
		t.Fatalf("Copy: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), sector) {
		t.Error("Copy did not reproduce the cooked sector unchanged")
	}

	hash, err := rt.Hash()
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if want := sha1Hex(sector); hash != want {
		t.Errorf("Hash = %s, want %s", hash, want)
	}
}

func TestResolveAndCopyMode1RawTrack(t *testing.T) {
	dir := t.TempDir()
	d1, d2 := sectorData(0, 1), sectorData(1, 2)
	content := append(frame(2352, 16, 2048, 0xAA, d1), frame(2352, 16, 2048, 0xBB, d2)...)
	writeFixture(t, dir, "game.bin", content)

	sheet, err := Parse([]byte("FILE \"game.bin\" BINARY\nTRACK 01 MODE1/2352\nINDEX 01 00:00:00\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	tracks, err := Resolve(sheet, dir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	var buf bytes.Buffer
	if err := tracks[0].Copy(&buf); err != nil {
		t.Fatalf("Copy: %v", err)
	}
	want := append(append([]byte{}, d1...), d2...)
	if !bytes.Equal(buf.Bytes(), want) {
		t.Error("Copy did not strip Mode 1 sync/header/ECC correctly")
	}
}

func TestResolveAndCopyMode2RawTrack(t *testing.T) {
	dir := t.TempDir()
	d1 := sectorData(0, 3)
	content := frame(2352, 24, 2048, 0xCC, d1)
	writeFixture(t, dir, "game.bin", content)

	sheet, err := Parse([]byte("FILE \"game.bin\" BINARY\nTRACK 01 MODE2/2352\nINDEX 01 00:00:00\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	tracks, err := Resolve(sheet, dir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	var buf bytes.Buffer
	if err := tracks[0].Copy(&buf); err != nil {
		t.Fatalf("Copy: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), d1) {
		t.Error("Copy did not strip Mode 2 Form 1 sync/header/ECC correctly")
	}
}

func TestResolveMultiTrackSingleBin(t *testing.T) {
	dir := t.TempDir()

	// Track 1: one MODE1/2352 data frame. Track 2: a 1-frame pregap, then
	// one AUDIO frame (raw, no stripping).
	dataFrame := frame(2352, 16, 2048, 0xAA, sectorData(0, 1))
	pregapFrame := make([]byte, 2352)
	for i := range pregapFrame {
		pregapFrame[i] = 0xEE // silence/junk, must never appear in output
	}
	audioFrame := bytes.Repeat([]byte{0x11, 0x22}, 2352/2)

	content := append(append(dataFrame, pregapFrame...), audioFrame...)
	writeFixture(t, dir, "game.bin", content)

	sheet, err := Parse([]byte(`FILE "game.bin" BINARY
TRACK 01 MODE1/2352
INDEX 01 00:00:00
TRACK 02 AUDIO
INDEX 00 00:00:01
INDEX 01 00:00:02
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	tracks, err := Resolve(sheet, dir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(tracks) != 2 {
		t.Fatalf("got %d tracks, want 2", len(tracks))
	}

	// Track 1 must end at track 2's pregap (INDEX 00), not its INDEX 01.
	if tracks[0].Start != 0 || tracks[0].End != 2352 {
		t.Errorf("track 1 range = [%d,%d), want [0,2352)", tracks[0].Start, tracks[0].End)
	}
	// Track 2 must start at INDEX 01, excluding the pregap frame.
	if tracks[1].Start != 2*2352 || tracks[1].End != int64(len(content)) {
		t.Errorf("track 2 range = [%d,%d), want [%d,%d)", tracks[1].Start, tracks[1].End, 2*2352, len(content))
	}

	var buf bytes.Buffer
	if err := tracks[1].Copy(&buf); err != nil {
		t.Fatalf("Copy: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), audioFrame) {
		t.Error("audio track Copy included pregap or stripped audio content")
	}
}

func TestResolveMultiFile(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "track1.bin", sectorData(0, 1))
	writeFixture(t, dir, "track2.bin", append(sectorData(0, 5), sectorData(1, 9)...))

	sheet, err := Parse([]byte(`FILE "track1.bin" BINARY
TRACK 01 MODE1/2048
INDEX 01 00:00:00
FILE "track2.bin" BINARY
TRACK 02 MODE1/2048
INDEX 01 00:00:00
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	tracks, err := Resolve(sheet, dir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(tracks) != 2 {
		t.Fatalf("got %d tracks, want 2", len(tracks))
	}
	if tracks[0].FilePath == tracks[1].FilePath {
		t.Error("expected tracks to resolve to different files")
	}
	if tracks[1].End != 2*2048 {
		t.Errorf("track 2 end = %d, want %d (its own file's size)", tracks[1].End, 2*2048)
	}
}

func TestResolveRejectsMixedFrameSizes(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "game.bin", make([]byte, 2352+2048))

	sheet, err := Parse([]byte(`FILE "game.bin" BINARY
TRACK 01 MODE1/2352
INDEX 01 00:00:00
TRACK 02 MODE1/2048
INDEX 01 00:00:01
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if _, err := Resolve(sheet, dir); err == nil {
		t.Fatal("Resolve should reject tracks with differing frame sizes in one FILE")
	}
}

func TestCopyRejectsUnsupportedMode(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "game.bin", make([]byte, 2336))

	sheet, err := Parse([]byte("FILE \"game.bin\" BINARY\nTRACK 01 MODE2/2336\nINDEX 01 00:00:00\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	tracks, err := Resolve(sheet, dir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if err := tracks[0].Copy(&bytes.Buffer{}); err == nil {
		t.Fatal("Copy should reject an unsupported data track mode")
	}
}

func TestResolveWaveHashesPCMOnly(t *testing.T) {
	dir := t.TempDir()
	pcm := bytes.Repeat([]byte{1, 2, 3, 4}, 2352/4*3) // 3 frames
	// The same audio as a raw BIN track and as a WAV with a RIFF header
	// (plus an extra chunk before "data"): both must hash alike.
	os.WriteFile(filepath.Join(dir, "a.bin"), pcm, 0o644)
	var wav bytes.Buffer
	le := binary.LittleEndian
	wav.WriteString("RIFF")
	binary.Write(&wav, le, uint32(4+8+16+8+4+8+len(pcm)))
	wav.WriteString("WAVEfmt ")
	binary.Write(&wav, le, uint32(16))
	binary.Write(&wav, le, []uint16{1, 2})
	binary.Write(&wav, le, []uint32{44100, 176400})
	binary.Write(&wav, le, []uint16{4, 16})
	wav.WriteString("LIST")
	binary.Write(&wav, le, uint32(4))
	wav.WriteString("INFO")
	wav.WriteString("data")
	binary.Write(&wav, le, uint32(len(pcm)))
	wav.Write(pcm)
	os.WriteFile(filepath.Join(dir, "a.wav"), wav.Bytes(), 0o644)

	hash := func(cueText string) string {
		sheet, err := Parse([]byte(cueText))
		if err != nil {
			t.Fatal(err)
		}
		tracks, err := Resolve(sheet, dir)
		if err != nil {
			t.Fatal(err)
		}
		h, err := tracks[0].Hash()
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	bin := hash("FILE \"a.bin\" BINARY\n  TRACK 01 AUDIO\n    INDEX 01 00:00:01\n")
	wavHash := hash("FILE \"a.wav\" WAVE\n  TRACK 01 AUDIO\n    INDEX 01 00:00:01\n")
	if bin != wavHash {
		t.Fatalf("WAV track hash %s differs from BIN %s", wavHash, bin)
	}

	os.WriteFile(filepath.Join(dir, "mono.wav"), []byte("RIFF\x24\x00\x00\x00WAVEfmt \x10\x00\x00\x00\x01\x00\x01\x00"+strings.Repeat("\x00", 12)), 0o644)
	if _, _, err := WAVData(filepath.Join(dir, "mono.wav")); err == nil {
		t.Fatal("mono WAV accepted")
	}
}

func TestWAVDataRejects(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{"not RIFF", "RIFX\x00\x00\x00\x00WAVE"},
		{"no data chunk", "RIFF\x04\x00\x00\x00WAVE"},
		// A 4-byte fmt chunk whose following bytes happen to read as
		// valid 16-bit stereo PCM fields.
		{"short fmt chunk", "RIFF\x30\x00\x00\x00WAVEfmt \x04\x00\x00\x00\x01\x00\x02\x00data\x04\x00\x00\x00\x00\x00\x10\x00data\x04\x00\x00\x00\x00\x00\x00\x00"},
		{"mono", "RIFF\x24\x00\x00\x00WAVEfmt \x10\x00\x00\x00\x01\x00\x01\x00" + strings.Repeat("\x00", 12)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := WAVDataFrom(strings.NewReader(tt.data)); err == nil {
				t.Error("accepted")
			}
		})
	}
}

func TestResolveRejectsTracksOutsideFile(t *testing.T) {
	// A track starting after its end, or ending past its file, would
	// otherwise be read as a negative length: a garbage WAV size.
	for _, tc := range []struct{ name, sheet string }{
		{"last track starts past the end", "FILE \"game.bin\" BINARY\nTRACK 01 MODE1/2352\nINDEX 01 00:00:00\nTRACK 02 AUDIO\nINDEX 01 00:00:05\n"},
		{"middle track ends past the end", "FILE \"game.bin\" BINARY\nTRACK 01 MODE1/2352\nINDEX 01 00:00:00\nTRACK 02 AUDIO\nINDEX 01 00:00:05\nTRACK 03 AUDIO\nINDEX 01 00:00:06\n"},
		{"next track before this one", "FILE \"game.bin\" BINARY\nTRACK 01 MODE1/2352\nINDEX 01 00:00:01\nTRACK 02 AUDIO\nINDEX 01 00:00:00\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFixture(t, dir, "game.bin", make([]byte, 2*2352))
			sheet, err := Parse([]byte(tc.sheet))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Resolve(sheet, dir); err == nil || !strings.Contains(err.Error(), "outside the file") {
				t.Fatalf("Resolve = %v, want an out-of-range error", err)
			}
		})
	}
}
