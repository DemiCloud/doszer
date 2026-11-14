package build

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	iofs "io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/DemiCloud/doszer/internal/manifest"
)

func writeMediaFixture(t *testing.T, dir, cueName, binName string, content []byte) string {
	t.Helper()
	cuePath := filepath.Join(dir, cueName)
	cueBody := "FILE \"" + binName + "\" BINARY\nTRACK 01 MODE1/2048\nINDEX 01 00:00:00\n"
	if err := os.WriteFile(cuePath, []byte(cueBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, binName), content, 0o644); err != nil {
		t.Fatal(err)
	}
	return cuePath
}

func sha1Hex(b []byte) string {
	sum := sha1.Sum(b)
	return hex.EncodeToString(sum[:])
}

// sectorContent returns one MODE1/2048 sector (2048 bytes), zero-padded
// after s, so fixtures stay sector-aligned.
func sectorContent(s string) []byte {
	b := make([]byte, 2048)
	copy(b, s)
	return b
}

// dirSet collects dir into a fileSet, as collect does.
func dirSet(t *testing.T, dir string) *fileSet {
	t.Helper()
	fs := newFileSet()
	err := filepath.WalkDir(dir, func(p string, d iofs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		info, _ := d.Info()
		fs.set(filepath.ToSlash(rel), diskEntry(p, info.ModTime()))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return fs
}

// wavFile wraps PCM samples in a 16-bit stereo WAV file, with a LIST
// chunk ahead of the data so the data offset isn't the usual 44.
func wavFile(pcm []byte) []byte {
	var b bytes.Buffer
	le := binary.LittleEndian
	list := []byte("INFOxxxx")
	b.WriteString("RIFF")
	binary.Write(&b, le, uint32(4+8+16+8+len(list)+8+len(pcm)))
	b.WriteString("WAVEfmt ")
	for _, v := range []any{uint32(16), uint16(1), uint16(2), uint32(44100), uint32(176400), uint16(4), uint16(16)} {
		binary.Write(&b, le, v)
	}
	b.WriteString("LIST")
	binary.Write(&b, le, uint32(len(list)))
	b.Write(list)
	b.WriteString("data")
	binary.Write(&b, le, uint32(len(pcm)))
	b.Write(pcm)
	return b.Bytes()
}

func TestLocateMedia(t *testing.T) {
	disc1 := sectorContent("disc one content")
	disc2 := sectorContent("disc two content")
	audio := bytes.Repeat([]byte{1, 2, 3, 4}, 2352/4*2) // two frames of PCM
	data := func(c []byte) manifest.Track { return manifest.Track{Type: manifest.TrackData, SHA1: sha1Hex(c)} }
	aud := manifest.Track{Type: manifest.TrackAudio, SHA1: sha1Hex(audio)}
	mixedCue := func(audioFile, audioType string) string {
		return "FILE \"game.iso\" BINARY\nTRACK 01 MODE1/2048\nINDEX 01 00:00:00\n" +
			"FILE \"" + audioFile + "\" " + audioType + "\nTRACK 02 AUDIO\nINDEX 01 00:00:00\n"
	}
	extract := []manifest.Extract{{Media: "disc1", From: "/", To: "x"}}

	tests := []struct {
		name     string
		files    map[string][]byte
		media    []manifest.Media
		extracts []manifest.Extract
		want     map[string]string // media id -> matched image
		warnings int
		wantErr  bool
	}{
		{
			name:  "match",
			files: map[string][]byte{"game.cue": cueFor("game.bin"), "game.bin": disc1},
			media: []manifest.Media{{ID: "disc1", Tracks: []manifest.Track{data(disc1)}}},
			want:  map[string]string{"disc1": "game.cue"},
		},
		{
			name:     "hash mismatch, not extracted: warning",
			files:    map[string][]byte{"game.cue": cueFor("game.bin"), "game.bin": disc2},
			media:    []manifest.Media{{ID: "disc1", Tracks: []manifest.Track{data(disc1)}}},
			warnings: 1,
		},
		{
			name:     "hash mismatch, extracted: error",
			files:    map[string][]byte{"game.cue": cueFor("game.bin"), "game.bin": disc2},
			media:    []manifest.Media{{ID: "disc1", Tracks: []manifest.Track{data(disc1)}}},
			extracts: extract,
			wantErr:  true,
		},
		{
			name:     "absent, extracted: error",
			media:    []manifest.Media{{ID: "disc1", Tracks: []manifest.Track{data(disc1)}}},
			extracts: extract,
			wantErr:  true,
		},
		{
			name:     "wrong track count: not found",
			files:    map[string][]byte{"game.cue": cueFor("game.bin"), "game.bin": disc1},
			media:    []manifest.Media{{ID: "disc1", Tracks: []manifest.Track{data(disc1), aud}}},
			warnings: 1,
		},
		{
			name: "same layout, two discs",
			files: map[string][]byte{
				"d1/a.cue": cueFor("a.bin"), "d1/a.bin": disc1,
				"d2/b.cue": cueFor("b.bin"), "d2/b.bin": disc2,
			},
			media: []manifest.Media{
				{ID: "disc2", Tracks: []manifest.Track{data(disc2)}},
				{ID: "disc1", Tracks: []manifest.Track{data(disc1)}},
			},
			want: map[string]string{"disc1": "d1/a.cue", "disc2": "d2/b.cue"},
		},
		{
			name:  "bare ISO",
			files: map[string][]byte{"Game.iso": disc1},
			media: []manifest.Media{{ID: "disc1", Tracks: []manifest.Track{data(disc1)}}},
			want:  map[string]string{"disc1": "Game.iso"},
		},
		{
			name: "WAVE audio verifies",
			files: map[string][]byte{
				"game.cue": []byte(mixedCue("game (Track 02).wav", "WAVE")),
				"game.iso": disc1, "game (Track 02).wav": wavFile(audio),
			},
			media: []manifest.Media{{ID: "disc1", Tracks: []manifest.Track{data(disc1), aud}}},
			want:  map[string]string{"disc1": "game.cue"},
		},
		{
			name: "WAVE audio mismatch",
			files: map[string][]byte{
				"game.cue": []byte(mixedCue("game (Track 02).wav", "WAVE")),
				"game.iso": disc1, "game (Track 02).wav": wavFile(audio[:2352]),
			},
			media:    []manifest.Media{{ID: "disc1", Tracks: []manifest.Track{data(disc1), aud}}},
			extracts: extract,
			wantErr:  true,
		},
		{
			name: "Ogg audio skipped with a warning",
			files: map[string][]byte{
				"game.cue": []byte(mixedCue("game (Track 02).ogg", "OGG")),
				"game.iso": disc1, "game (Track 02).ogg": []byte("OggS not really"),
			},
			media:    []manifest.Media{{ID: "disc1", Tracks: []manifest.Track{data(disc1), aud}}},
			want:     map[string]string{"disc1": "game.cue"},
			warnings: 1,
		},
		{
			name: "Ogg audio doesn't excuse a data mismatch",
			files: map[string][]byte{
				"game.cue": []byte(mixedCue("game (Track 02).ogg", "OGG")),
				"game.iso": disc2, "game (Track 02).ogg": []byte("OggS not really"),
			},
			media:    []manifest.Media{{ID: "disc1", Tracks: []manifest.Track{data(disc1), aud}}},
			extracts: extract,
			wantErr:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Each case runs over files on disk (build) and over in-memory
			// entries (repack).
			for _, src := range []string{"disk", "memory"} {
				fs := newFileSet()
				if src == "disk" {
					dir := t.TempDir()
					for p, b := range tt.files {
						full := filepath.Join(dir, filepath.FromSlash(p))
						os.MkdirAll(filepath.Dir(full), 0o755)
						os.WriteFile(full, b, 0o644)
					}
					fs = dirSet(t, dir)
				} else {
					for p, b := range tt.files {
						fs.set(p, dataEntry(b, Epoch))
					}
				}
				resolved, warnings, err := locateMedia(fs, tt.media, tt.extracts)
				if (err != nil) != tt.wantErr {
					t.Fatalf("%s: err = %v, wantErr %v", src, err, tt.wantErr)
				}
				if err != nil {
					continue
				}
				if len(warnings) != tt.warnings {
					t.Errorf("%s: warnings %q, want %d", src, warnings, tt.warnings)
				}
				if len(resolved) != len(tt.want) {
					t.Errorf("%s: resolved %d media, want %d", src, len(resolved), len(tt.want))
				}
				for id, img := range tt.want {
					if resolved[id].Image != img {
						t.Errorf("%s: %s matched %q, want %q", src, id, resolved[id].Image, img)
					}
				}
			}
		})
	}
}

func cueFor(bin string) []byte {
	return []byte("FILE \"" + bin + "\" BINARY\nTRACK 01 MODE1/2048\nINDEX 01 00:00:00\n")
}

func mkdir(t *testing.T, base, name string) string {
	t.Helper()
	dir := filepath.Join(base, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestIdentifyMedia(t *testing.T) {
	dir := t.TempDir()
	a := sectorContent("disc one")
	b := sectorContent("disc two")
	writeMediaFixture(t, mkdir(t, dir, "CD1"), "Game (Disc 1).cue", "d1.bin", a)
	writeMediaFixture(t, mkdir(t, dir, "CD2"), "Game (Disc 2).cue", "d2.bin", b)
	writeMediaFixture(t, mkdir(t, dir, "COPY"), "Same as disc 1.cue", "copy.bin", a)
	os.WriteFile(filepath.Join(dir, "broken.cue"), []byte("FILE \"missing.bin\" BINARY\nTRACK 01 MODE1/2048\nINDEX 01 00:00:00\n"), 0o644)
	// Ogg audio can't be hashed: identifying it would write a wrong hash.
	os.WriteFile(filepath.Join(dir, "d1.ogg"), []byte("OggS"), 0o644)
	os.WriteFile(filepath.Join(dir, "lossy.cue"), []byte("FILE \"CD1/d1.bin\" BINARY\nTRACK 01 MODE1/2048\nINDEX 01 00:00:00\nFILE \"d1.ogg\" OGG\nTRACK 02 AUDIO\nINDEX 01 00:00:00\n"), 0o644)

	media, skipped, err := IdentifyMedia(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 2 {
		t.Errorf("skipped %v, want the broken and lossy sheets", skipped)
	}
	if len(media) != 2 {
		t.Fatalf("media %+v, want 2 (the copy left out)", media)
	}
	if media[0].ID != "disc1" || media[0].Label != "Game (Disc 1)" || media[0].Tracks[0].SHA1 != sha1Hex(a) ||
		media[1].ID != "disc2" || media[1].Tracks[0].SHA1 != sha1Hex(b) {
		t.Fatalf("media %+v", media)
	}

	// What init writes must parse back to the same entries.
	text := manifest.Template(manifest.TemplateOptions{Name: "Game", Media: media})
	m, err := manifest.Parse([]byte(text), "Game")
	if err != nil {
		t.Fatalf("template with media: %v", err)
	}
	if len(m.Source.Media) != 2 || m.Source.Media[1].Tracks[0] != media[1].Tracks[0] {
		t.Fatalf("parsed media %+v", m.Source.Media)
	}
}

func TestLocateMediaAcceptsBareISO(t *testing.T) {
	// The same single-track disc as a BIN/CUE dump (raw Mode 1 sectors)
	// and as a bare ISO: media declared from one verifies against the other.
	dir := t.TempDir()
	user := sectorContent("ISO 9660 user data")
	raw := make([]byte, 2352)
	copy(raw[16:], user)
	writeRaw := func(name string, b []byte) { os.WriteFile(filepath.Join(dir, name), b, 0o644) }
	writeRaw("game.bin", raw)
	writeRaw("game.cue", []byte("FILE \"game.bin\" BINARY\nTRACK 01 MODE1/2352\nINDEX 01 00:00:00\n"))

	media, _, err := IdentifyMedia(dir)
	if err != nil || len(media) != 1 {
		t.Fatalf("IdentifyMedia = %+v, %v", media, err)
	}

	isoDir := t.TempDir()
	os.WriteFile(filepath.Join(isoDir, "Game.iso"), user, 0o644)
	if resolved, _, err := locateMedia(dirSet(t, isoDir), media, nil); err != nil || len(resolved) != 1 {
		t.Fatalf("bare ISO not recognized as the same media: %v", err)
	}
}
