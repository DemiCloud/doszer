package dump

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// Importing a single-BIN image writes the same files dump writes for the
// disc: disc<N>.cue and one file per track in Redump's layout, N past the
// discs already in the directory.
func TestImport(t *testing.T) {
	data := bytes.Repeat([]byte{'D'}, 3*2352)
	audio := bytes.Repeat([]byte{'A'}, 4*2352)
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, "game.bin"), append(append([]byte(nil), data...), audio...), 0o644)
	os.WriteFile(filepath.Join(src, "game.cue"), []byte("FILE \"game.bin\" BINARY\n  TRACK 01 MODE1/2352\n    INDEX 01 00:00:00\n  TRACK 02 AUDIO\n    PREGAP 00:00:02\n    INDEX 01 00:00:03\n"), 0o644)

	dir := t.TempDir()
	for _, name := range []string{"disc1", "disc2"} {
		res, err := Import(filepath.Join(src, "game.cue"), dir, 0)
		if err != nil {
			t.Fatal(err)
		}
		if want := filepath.Join(dir, name+".cue"); res.Cue != want {
			t.Errorf("Cue = %s, want %s", res.Cue, want)
		}
	}

	files := map[string][]byte{
		"disc1.cue":           []byte("FILE \"disc1 (Track 1).bin\" BINARY\r\n  TRACK 01 MODE1/2352\r\n    INDEX 01 00:00:00\r\nFILE \"disc1 (Track 2).bin\" BINARY\r\n  TRACK 02 AUDIO\r\n    INDEX 00 00:00:00\r\n    INDEX 01 00:00:02\r\n"),
		"disc1 (Track 1).bin": data,
		"disc1 (Track 2).bin": append(make([]byte, 2*2352), audio...),
	}
	for name, want := range files {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s: %d bytes (%v), want %d", name, len(got), err, len(want))
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "disc2 (Track 2).bin")); err != nil {
		t.Error(err)
	}
}
