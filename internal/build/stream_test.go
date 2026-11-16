package build

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
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
		resolved, warnings, err := locateMedia(fs, media, nil, nil)
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

func TestArchiveMediaReportsCRC(t *testing.T) {
	disc := bytes.Repeat([]byte("ISO user data. "), 2048)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range []struct {
		name string
		data []byte
	}{{"GOOD.ISO", disc}, {"BAD.ISO", disc}, {"OTHER.DAT", []byte("not media")}} {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: e.name, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		w.Write(e.data)
	}
	zw.Close()
	data := buf.Bytes()
	// Damage BAD.ISO's stored bytes (its copy is the second one).
	first := bytes.Index(data, disc[:64])
	second := first + len(disc) + bytes.Index(data[first+len(disc):], disc[:64])
	copy(data[second+1000:], "DAMAGE")

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	media := []manifest.Media{{ID: "disc1", Tracks: []manifest.Track{{Type: manifest.TrackData, SHA1: sha1Hex(disc)}}}}
	_, crc, err := ArchiveMedia(zr.File, media)
	if err != nil {
		t.Fatal(err)
	}
	if err, ok := crc["BAD.ISO"]; !ok || !errors.Is(err, zip.ErrChecksum) {
		t.Errorf("BAD.ISO: %v, %v; want zip.ErrChecksum", err, ok)
	}
	// BAD.ISO sorts first and no longer matches, so GOOD.ISO is hashed too.
	if err, ok := crc["GOOD.ISO"]; !ok || err != nil {
		t.Errorf("GOOD.ISO: %v, %v; want a passed check", err, ok)
	}
	if _, ok := crc["OTHER.DAT"]; ok {
		t.Error("OTHER.DAT was never read but has a result")
	}
}

func TestLocateMediaTeesDataTrack(t *testing.T) {
	userData := cat(bytes.Repeat([]byte{1}, 2048), bytes.Repeat([]byte{2}, 2048))
	raw := cat(mode1Sector(1), mode1Sector(2))
	audio := audioFrames(3, 5)
	mixedBin := cat(raw, audio)
	mixedCue := "FILE \"GAME.BIN\" BINARY\n  TRACK 01 MODE1/2352\n    INDEX 01 00:00:00\n  TRACK 02 AUDIO\n    INDEX 01 00:00:02\n"
	dataMedia := []manifest.Media{{ID: "disc1", Tracks: []manifest.Track{
		{Type: manifest.TrackData, SHA1: sha1Hex(userData)},
		{Type: manifest.TrackAudio, SHA1: sha1Hex(audio)},
	}}}
	extract := []manifest.Extract{{Media: "disc1", From: "X", To: "X"}}

	tests := []struct {
		name     string
		files    map[string][]byte
		media    []manifest.Media
		extracts []manifest.Extract
		wantTee  bool // the data track is kept as a temporary file
	}{
		{"raw sectors, extracted", map[string][]byte{"GAME.CUE": []byte(mixedCue), "GAME.BIN": mixedBin}, dataMedia, extract, true},
		{"raw sectors, not extracted", map[string][]byte{"GAME.CUE": []byte(mixedCue), "GAME.BIN": mixedBin}, dataMedia, nil, false},
		{"2048-byte sectors: read in place", map[string][]byte{"GAME.ISO": userData},
			[]manifest.Media{{ID: "disc1", Tracks: []manifest.Track{{Type: manifest.TrackData, SHA1: sha1Hex(userData)}}}}, extract, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, data := range tt.files {
				if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			var temps []*os.File
			defer func() {
				for _, f := range temps {
					f.Close()
				}
			}()
			resolved, warnings, err := locateMedia(dirSet(t, dir), tt.media, tt.extracts, &teeTarget{dir: t.TempDir(), files: &temps})
			if err != nil || len(warnings) > 0 {
				t.Fatalf("locateMedia: %v, %v", warnings, err)
			}
			iso := resolved["disc1"].isoFile
			if !tt.wantTee {
				if iso != "" || len(temps) != 0 {
					t.Errorf("isoFile %q, %d temporary files; want none", iso, len(temps))
				}
				return
			}
			if len(temps) != 1 || iso != temps[0].Name() {
				t.Fatalf("isoFile %q, temporary files %d; want the one", iso, len(temps))
			}
			if got, _ := os.ReadFile(iso); !bytes.Equal(got, userData) {
				t.Errorf("isoFile holds %d bytes, want the %d bytes of user data", len(got), len(userData))
			}
		})
	}
}

func TestLocateMediaRemovesUnmatchedTee(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "GAME.CUE"), []byte("FILE \"GAME.BIN\" BINARY\n  TRACK 01 MODE1/2352\n    INDEX 01 00:00:00\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "GAME.BIN"), mode1Sector(1), 0o644)
	var temps []*os.File
	media := []manifest.Media{{ID: "disc1", Tracks: []manifest.Track{{Type: manifest.TrackData, SHA1: strings.Repeat("0", 40)}}}}
	// Not extracted, so a missing media is only a warning.
	if _, _, err := locateMedia(dirSet(t, dir), media, []manifest.Extract{{Media: "disc2"}}, &teeTarget{dir: t.TempDir(), files: &temps}); err != nil {
		t.Fatal(err)
	}
	for _, f := range temps {
		f.Close()
		t.Errorf("%s: kept for media nothing extracts", f.Name())
	}
	temps = nil
	media[0].ID = "disc2"
	if _, _, err := locateMedia(dirSet(t, dir), media, []manifest.Extract{{Media: "disc2"}}, &teeTarget{dir: t.TempDir(), files: &temps}); err == nil {
		t.Fatal("unmatched extracted media accepted")
	}
	for _, f := range temps {
		f.Close()
		if _, err := os.Stat(f.Name()); err == nil {
			t.Errorf("%s: not removed after its image failed to match", f.Name())
		}
	}
}

func TestInspectedEntriesStayRaw(t *testing.T) {
	// Reading an entry only to decide nothing changes must leave it
	// eligible for writeArchive's raw pass-through.
	var read atomic.Int64
	src := func(s string) *fileEntry { return sourceEntry(countingSource([]byte(s), &read), Epoch) }
	fs := newFileSet()
	fs.set("GAME.BAT", src("@ECHO OFF\r\nGAME\r\n")) // already §6.6-normalized
	fs.set("SETUP.CFG", src("sound=sb16\r\n"))       // matched by a rule, not changed
	fs.set("EDIT.CFG", src("path=C:\\GAME\\\r\n"))   // changed by it
	fs.set("DOS.YML", src("cycles: max\r\n"))        // no [[input.pad]]: left alone
	fs.set("GAME.CUE", src("FILE \"GAME.iso\" BINARY\r\n  TRACK 01 MODE1/2048\r\n    INDEX 01 00:00:00\r\n"))
	fs.set("GAME.iso", dataEntry(bytes.Repeat([]byte{1}, 2048), Epoch))

	if err := normalizeText(fs); err != nil {
		t.Fatal(err)
	}
	if _, _, err := applyRewrite(fs, []manifest.Rewrite{{From: `C:\GAME\`, To: `C:\`, Files: []string{"*.CFG"}}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := convertMedia(fs, manifest.AudioOgg, 6, false, tempDirFunc(t)); err != nil {
		t.Fatal(err)
	}
	m := &manifest.Manifest{Format: 1, Name: "Game"}
	if err := addRuntimeFiles(fs, m, "Game"); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"GAME.BAT", "SETUP.CFG", "DOS.YML", "GAME.CUE"} {
		if e, _ := fs.get(p); e.rawPassthrough() == nil {
			t.Errorf("%s: no longer passed through raw", p)
		}
	}
	if e, _ := fs.get("EDIT.CFG"); e.rawPassthrough() != nil || string(e.data) != "path=C:\\\r\n" {
		t.Errorf("EDIT.CFG: not rewritten: %q", e.data)
	}
}
