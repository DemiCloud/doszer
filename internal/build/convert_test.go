package build

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"os/exec"
	"strings"
	"testing"

	"github.com/DemiCloud/doszer/internal/manifest"
)

// mode1Sector returns a raw 2352-byte Mode 1 sector whose user data is
// 2048 copies of b, with non-zero sync/header/ECC bytes that conversion
// must strip.
func mode1Sector(b byte) []byte {
	s := bytes.Repeat([]byte{0xEE}, 2352)
	copy(s[16:2064], bytes.Repeat([]byte{b}, 2048))
	return s
}

// audioFrames returns n CD-DA frames (2352 bytes each) filled with b.
func audioFrames(n int, b byte) []byte {
	return bytes.Repeat([]byte{b}, n*2352)
}

func cat(parts ...[]byte) []byte {
	return bytes.Join(parts, nil)
}

func content(t *testing.T, fs *fileSet, p string) []byte {
	t.Helper()
	e, ok := fs.get(p)
	if !ok {
		t.Fatalf("missing %s; have %v", p, fs.paths())
	}
	data, err := e.bytes()
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func tempDirFunc(t *testing.T) func() (string, error) {
	dir := t.TempDir()
	return func() (string, error) { return dir, nil }
}

func TestConvertMedia(t *testing.T) {
	// Single BIN, mixed mode: two data sectors, then an audio track with a
	// one-second (75-frame) INDEX 00 pregap and 3 frames of audio.
	singleBin := cat(mode1Sector(1), mode1Sector(2), audioFrames(75, 0), audioFrames(3, 7))
	singleCue := "FILE \"GAME.BIN\" BINARY\r\n" +
		"  TRACK 01 MODE1/2352\r\n    INDEX 01 00:00:00\r\n" +
		"  TRACK 02 AUDIO\r\n    INDEX 00 00:00:02\r\n    INDEX 01 00:01:02\r\n"

	tests := []struct {
		name      string
		files     map[string][]byte
		want      map[string][]byte // path -> exact content
		gone      []string
		warnings  int
		converted int
	}{
		{
			name:  "single BIN to ISO and WAV, pregap kept as PREGAP",
			files: map[string][]byte{"CD/Game.cue": []byte(singleCue), "CD/GAME.BIN": singleBin},
			want: map[string][]byte{
				"CD/Game.iso":            cat(bytes.Repeat([]byte{1}, 2048), bytes.Repeat([]byte{2}, 2048)),
				"CD/Game (Track 02).wav": cat(wavHeader(3*2352), audioFrames(3, 7)),
				"CD/Game.cue": []byte("FILE \"Game.iso\" BINARY\r\n  TRACK 01 MODE1/2048\r\n    INDEX 01 00:00:00\r\n" +
					"FILE \"Game (Track 02).wav\" WAVE\r\n  TRACK 02 AUDIO\r\n    PREGAP 00:01:00\r\n    INDEX 01 00:00:00\r\n"),
			},
			gone:      []string{"CD/GAME.BIN"},
			converted: 1,
		},
		{
			name: "already converted: unchanged",
			files: map[string][]byte{
				"Game.cue": []byte("FILE \"Game.iso\" BINARY\r\n  TRACK 01 MODE1/2048\r\n    INDEX 01 00:00:00\r\n" +
					"FILE \"Game (Track 02).wav\" WAVE\r\n  TRACK 02 AUDIO\r\n    PREGAP 00:02:00\r\n    INDEX 01 00:00:00\r\n"),
				"Game.iso":            bytes.Repeat([]byte{1}, 4096),
				"Game (Track 02).wav": cat(wavHeader(2352), audioFrames(1, 3)),
			},
			want: map[string][]byte{"Game.iso": bytes.Repeat([]byte{1}, 4096)},
		},
		{
			name: "2048-byte track and encoded audio kept, renamed",
			files: map[string][]byte{
				"Game.cue": []byte("FILE \"track01.bin\" BINARY\n  TRACK 01 MODE1/2048\n    INDEX 01 00:00:00\n" +
					"FILE \"track02.ogg\" OGG\n  TRACK 02 AUDIO\n    INDEX 00 00:00:00\n    INDEX 01 00:02:00\n"),
				"track01.bin": bytes.Repeat([]byte{9}, 2048),
				"track02.ogg": []byte("OggS-not-really"),
			},
			want: map[string][]byte{
				"Game.iso":            bytes.Repeat([]byte{9}, 2048),
				"Game (Track 02).ogg": []byte("OggS-not-really"),
				"Game.cue": []byte("FILE \"Game.iso\" BINARY\r\n  TRACK 01 MODE1/2048\r\n    INDEX 01 00:00:00\r\n" +
					"FILE \"Game (Track 02).ogg\" OGG\r\n  TRACK 02 AUDIO\r\n    INDEX 00 00:00:00\r\n    INDEX 01 00:02:00\r\n"),
			},
			gone:      []string{"track01.bin", "track02.ogg"},
			converted: 1,
		},
		{
			name:     "missing file: warned, unchanged",
			files:    map[string][]byte{"Game.cue": []byte(singleCue)},
			want:     map[string][]byte{"Game.cue": []byte(singleCue)},
			warnings: 1,
		},
		{
			name:  "not a CUE sheet: ignored",
			files: map[string][]byte{"NOTES.CUE": []byte("just some notes")},
			want:  map[string][]byte{"NOTES.CUE": []byte("just some notes")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := newFileSet()
			for p, data := range tt.files {
				fs.set(p, dataEntry(data, Epoch))
			}

			conv, warnings, err := convertMedia(fs, manifest.AudioWAV, 6, false, tempDirFunc(t))
			if err != nil {
				t.Fatal(err)
			}
			if len(warnings) != tt.warnings {
				t.Errorf("warnings = %v, want %d", warnings, tt.warnings)
			}
			if len(conv) != tt.converted {
				t.Errorf("conversions = %v, want %d", conv, tt.converted)
			}
			for p, want := range tt.want {
				if got := content(t, fs, p); !bytes.Equal(got, want) {
					if len(got) < 300 && len(want) < 300 {
						t.Errorf("%s = %q, want %q", p, got, want)
					} else {
						t.Errorf("%s: %d bytes differ from wanted %d", p, len(got), len(want))
					}
				}
			}
			for _, p := range tt.gone {
				if _, ok := fs.get(p); ok {
					t.Errorf("%s still present", p)
				}
			}
		})
	}
}

func wavHeader(n int) []byte {
	var b bytes.Buffer
	writeWAVHeader(&b, uint32(n))
	return b.Bytes()
}

func TestWAVHeader(t *testing.T) {
	h := wavHeader(2352)
	le := binary.LittleEndian
	if len(h) != 44 || string(h[0:4]) != "RIFF" || string(h[8:16]) != "WAVEfmt " || string(h[36:40]) != "data" {
		t.Fatalf("bad header %q", h)
	}
	checks := []struct {
		name      string
		got, want uint32
	}{
		{"riff size", le.Uint32(h[4:]), 36 + 2352},
		{"format", uint32(le.Uint16(h[20:])), 1},
		{"channels", uint32(le.Uint16(h[22:])), 2},
		{"rate", le.Uint32(h[24:]), 44100},
		{"block align", uint32(le.Uint16(h[32:])), 4},
		{"bits", uint32(le.Uint16(h[34:])), 16},
		{"data size", le.Uint32(h[40:]), 2352},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", c.name, c.got, c.want)
		}
	}
}

func TestConvertMediaKeepsUnconvertedEntryIdentity(t *testing.T) {
	// A data track already stored as exactly its 2048-byte user data is
	// reused as the same entry, so repack can still pass it through raw.
	fs := newFileSet()
	iso := dataEntry(bytes.Repeat([]byte{1}, 2048), Epoch)
	fs.set("Disc.cue", dataEntry([]byte("FILE \"DISC.ISO\" BINARY\n  TRACK 01 MODE1/2048\n    INDEX 01 00:00:00\n"), Epoch))
	fs.set("DISC.ISO", iso)

	if _, _, err := convertMedia(fs, manifest.AudioOgg, 6, false, func() (string, error) {
		t.Fatal("no track should need writing")
		return "", nil
	}); err != nil {
		t.Fatal(err)
	}
	if e, ok := fs.get("Disc.iso"); !ok || e != iso {
		t.Fatalf("Disc.iso is not the original entry; have %v", fs.paths())
	}
}

func TestConvertMediaDryRun(t *testing.T) {
	fs := newFileSet()
	fs.set("Game.cue", dataEntry([]byte("FILE \"G.BIN\" BINARY\n  TRACK 01 MODE1/2352\n    INDEX 01 00:00:00\n"), Epoch))
	fs.set("G.BIN", dataEntry(mode1Sector(1), Epoch))

	conv, _, err := convertMedia(fs, manifest.AudioOgg, 6, true, func() (string, error) {
		t.Fatal("dry run must not write tracks")
		return "", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(conv) != 1 || strings.Join(conv[0].Files, ",") != "Game.iso" {
		t.Fatalf("conversions = %v", conv)
	}
	if got := strings.Join(fs.paths(), ","); got != "Game.cue,Game.iso" {
		t.Fatalf("paths = %s", got)
	}
}

func TestConvertMediaRejectsCollision(t *testing.T) {
	fs := newFileSet()
	fs.set("Game.cue", dataEntry([]byte("FILE \"G.BIN\" BINARY\n  TRACK 01 MODE1/2352\n    INDEX 01 00:00:00\n"), Epoch))
	fs.set("G.BIN", dataEntry(mode1Sector(1), Epoch))
	fs.set("GAME.ISO", dataEntry([]byte("unrelated"), Epoch))

	if _, _, err := convertMedia(fs, manifest.AudioOgg, 6, false, tempDirFunc(t)); err == nil {
		t.Fatal("expected a collision error")
	}
}

func TestConvertMediaOgg(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	cue := "FILE \"T1.BIN\" BINARY\n  TRACK 01 MODE1/2352\n    INDEX 01 00:00:00\n" +
		"FILE \"T2.BIN\" BINARY\n  TRACK 02 AUDIO\n    INDEX 00 00:00:00\n    INDEX 01 00:02:00\n"

	encode := func() []byte {
		fs := newFileSet()
		fs.set("Game.cue", dataEntry([]byte(cue), Epoch))
		fs.set("T1.BIN", dataEntry(mode1Sector(1), Epoch))
		fs.set("T2.BIN", dataEntry(audioFrames(150+75, 0x10), Epoch))
		if _, _, err := convertMedia(fs, manifest.AudioOgg, 6, false, tempDirFunc(t)); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(content(t, fs, "Game.cue")), "FILE \"Game (Track 02).ogg\" OGG\r\n  TRACK 02 AUDIO\r\n    PREGAP 00:02:00\r\n") {
			t.Fatalf("cue = %q", content(t, fs, "Game.cue"))
		}
		return content(t, fs, "Game (Track 02).ogg")
	}

	a, b := encode(), encode()
	if !bytes.HasPrefix(a, []byte("OggS")) {
		t.Fatalf("not an Ogg stream: %q", a[:min(len(a), 16)])
	}
	if !bytes.Equal(a, b) {
		t.Fatal("Ogg output differs between identical runs")
	}
}

func TestUnconvertedMedia(t *testing.T) {
	write := func(files map[string]string) []*zip.File {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		for name, data := range files {
			w, _ := zw.Create(name)
			w.Write([]byte(data))
		}
		zw.Close()
		zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
		if err != nil {
			t.Fatal(err)
		}
		return zr.File
	}

	tests := []struct {
		name  string
		files map[string]string
		want  int
	}{
		{"converted", map[string]string{
			"Game.cue": "FILE \"Game.iso\" BINARY\r\n  TRACK 01 MODE1/2048\r\n    INDEX 01 00:00:00\r\n",
			"Game.iso": strings.Repeat("x", 2048),
		}, 0},
		{"raw sectors", map[string]string{
			"Game.cue": "FILE \"Game.bin\" BINARY\r\n  TRACK 01 MODE1/2352\r\n    INDEX 01 00:00:00\r\n",
			"Game.bin": strings.Repeat("x", 2352),
		}, 1},
		{"misnamed", map[string]string{
			"Game.cue":  "FILE \"Other.iso\" BINARY\r\n  TRACK 01 MODE1/2048\r\n    INDEX 01 00:00:00\r\n",
			"Other.iso": strings.Repeat("x", 2048),
		}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := UnconvertedMedia(write(tt.files), manifest.AudioOgg); len(got) != tt.want {
				t.Fatalf("UnconvertedMedia = %v, want %d", got, tt.want)
			}
		})
	}
}

func TestConvertMediaUnsupportedModeWarns(t *testing.T) {
	for _, dryRun := range []bool{false, true} {
		fs := newFileSet()
		fs.set("Game.cue", dataEntry([]byte("FILE \"G.BIN\" BINARY\n  TRACK 01 MODE2/2336\n    INDEX 01 00:00:00\n"), Epoch))
		fs.set("G.BIN", dataEntry(make([]byte, 2*2336), Epoch))

		conv, warnings, err := convertMedia(fs, manifest.AudioOgg, 6, dryRun, tempDirFunc(t))
		if err != nil {
			t.Fatalf("dryRun=%v: %v", dryRun, err)
		}
		if len(conv) != 0 || len(warnings) != 1 || !strings.Contains(warnings[0], "MODE2/2336") {
			t.Errorf("dryRun=%v: conversions %v, warnings %v; want none and one naming MODE2/2336", dryRun, conv, warnings)
		}
		if got := strings.Join(fs.paths(), ","); got != "G.BIN,Game.cue" {
			t.Errorf("dryRun=%v: paths = %s, want the sheet left as it was", dryRun, got)
		}
	}
}

func TestConvertMediaDropsPartialSector(t *testing.T) {
	tests := []struct {
		name string
		mode string
		bin  []byte
	}{
		{"raw", "MODE1/2352", cat(mode1Sector(1), mode1Sector(2), []byte("trailing"))},
		// Not reused as is: the ISO must hold whole sectors only.
		{"cooked", "MODE1/2048", cat(bytes.Repeat([]byte{1}, 2048), bytes.Repeat([]byte{2}, 2048), []byte("trailing"))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := newFileSet()
			fs.set("Game.cue", dataEntry([]byte("FILE \"G.BIN\" BINARY\n  TRACK 01 "+tt.mode+"\n    INDEX 01 00:00:00\n"), Epoch))
			fs.set("G.BIN", dataEntry(tt.bin, Epoch))

			if _, _, err := convertMedia(fs, manifest.AudioOgg, 6, false, tempDirFunc(t)); err != nil {
				t.Fatal(err)
			}
			want := cat(bytes.Repeat([]byte{1}, 2048), bytes.Repeat([]byte{2}, 2048))
			if got := content(t, fs, "Game.iso"); !bytes.Equal(got, want) {
				t.Errorf("Game.iso is %d bytes, want the %d bytes of two whole sectors", len(got), len(want))
			}
		})
	}
}
