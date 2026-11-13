package build

import (
	"crypto/sha1"
	"encoding/hex"
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

func TestLocateMediaMatchesAndVerifies(t *testing.T) {
	dir := t.TempDir()
	content := sectorContent("sector content")
	writeMediaFixture(t, dir, "game.cue", "game.bin", content)

	declared := []manifest.Media{{
		ID:     "disc1",
		Tracks: []manifest.Track{{Type: manifest.TrackData, SHA1: sha1Hex(content)}},
	}}

	resolved, err := locateMedia(dir, declared)
	if err != nil {
		t.Fatalf("locateMedia: %v", err)
	}
	m, ok := resolved["disc1"]
	if !ok {
		t.Fatal("disc1 not resolved")
	}
	if len(m.Tracks) != 1 {
		t.Fatalf("got %d tracks, want 1", len(m.Tracks))
	}
	if m.Dir != dir {
		t.Errorf("Dir = %q, want %q", m.Dir, dir)
	}
}

func TestLocateMediaHashMismatch(t *testing.T) {
	dir := t.TempDir()
	content := sectorContent("actual disc content")
	writeMediaFixture(t, dir, "game.cue", "game.bin", content)

	declared := []manifest.Media{{
		ID:     "disc1",
		Tracks: []manifest.Track{{Type: manifest.TrackData, SHA1: sha1Hex(sectorContent("wrong content"))}},
	}}

	_, err := locateMedia(dir, declared)
	if err == nil {
		t.Fatal("locateMedia should fail on a hash mismatch")
	}
}

func TestLocateMediaNotFound(t *testing.T) {
	dir := t.TempDir()

	declared := []manifest.Media{{
		ID:     "disc1",
		Tracks: []manifest.Track{{Type: manifest.TrackData, SHA1: sha1Hex([]byte("x"))}},
	}}

	_, err := locateMedia(dir, declared)
	if err == nil {
		t.Fatal("locateMedia should fail when no cue sheet exists")
	}
}

func TestLocateMediaWrongShape(t *testing.T) {
	dir := t.TempDir()
	content := []byte("single track disc")
	writeMediaFixture(t, dir, "game.cue", "game.bin", content)

	// Declares two tracks; the candidate only has one: a shape mismatch,
	// not a hash mismatch.
	declared := []manifest.Media{{
		ID: "disc1",
		Tracks: []manifest.Track{
			{Type: manifest.TrackData, SHA1: sha1Hex(content)},
			{Type: manifest.TrackAudio, SHA1: sha1Hex([]byte("y"))},
		},
	}}

	_, err := locateMedia(dir, declared)
	if err == nil {
		t.Fatal("locateMedia should fail on a track-count mismatch")
	}
}

func TestLocateMediaMultipleCandidates(t *testing.T) {
	dir := t.TempDir()
	disc1 := sectorContent("disc one content")
	disc2 := sectorContent("disc two content, longer")
	writeMediaFixture(t, mkdir(t, dir, "d1"), "a.cue", "a.bin", disc1)
	writeMediaFixture(t, mkdir(t, dir, "d2"), "b.cue", "b.bin", disc2)

	declared := []manifest.Media{
		{ID: "disc1", Tracks: []manifest.Track{{Type: manifest.TrackData, SHA1: sha1Hex(disc1)}}},
		{ID: "disc2", Tracks: []manifest.Track{{Type: manifest.TrackData, SHA1: sha1Hex(disc2)}}},
	}

	resolved, err := locateMedia(dir, declared)
	if err != nil {
		t.Fatalf("locateMedia: %v", err)
	}
	if len(resolved) != 2 {
		t.Fatalf("got %d resolved, want 2", len(resolved))
	}
	if resolved["disc1"].Dir == resolved["disc2"].Dir {
		t.Error("disc1 and disc2 resolved to the same directory")
	}
}

func mkdir(t *testing.T, base, name string) string {
	t.Helper()
	dir := filepath.Join(base, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}
