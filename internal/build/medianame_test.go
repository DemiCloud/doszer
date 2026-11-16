package build

import (
	"strings"
	"testing"

	"github.com/DemiCloud/doszer/internal/manifest"
)

func TestRenameMediaCueRenamesSingleMatch(t *testing.T) {
	fs := newFileSet()
	cueBody := "FILE \"GAME.BIN\" BINARY\n" +
		"  TRACK 01 MODE1/2048\n    INDEX 01 00:00:00\n" +
		"FILE \"GAME.WAV\" WAVE\n  TRACK 02 AUDIO\n    INDEX 00 00:00:00\n    INDEX 01 00:02:00\n"
	fs.set("Game (USA) (Disc 1).cue", dataEntry([]byte(cueBody), Epoch))
	fs.set("GAME.BIN", dataEntry([]byte("data"), Epoch))
	fs.set("GAME.WAV", dataEntry([]byte("audio"), Epoch))

	media := []manifest.Media{{
		ID: "disc1",
		Tracks: []manifest.Track{
			{Type: manifest.TrackData, SHA1: strings.Repeat("a", 40)},
			{Type: manifest.TrackAudio, SHA1: strings.Repeat("b", 40)},
		},
	}}
	boot := &manifest.Boot{Disc: "Game (USA) (Disc 1)"}

	if err := renameMediaCue(fs, media, boot, "Example Game"); err != nil {
		t.Fatal(err)
	}

	if _, ok := fs.get("Example Game.cue"); !ok {
		t.Fatalf("expected renamed cue, got paths %v", fs.paths())
	}
	if _, ok := fs.get("Game (USA) (Disc 1).cue"); ok {
		t.Fatalf("old cue name still present: %v", fs.paths())
	}
	if boot.Disc != "Example Game" {
		t.Fatalf("boot.Disc = %q, want %q", boot.Disc, "Example Game")
	}
}

func TestRenameMediaCueDisambiguatesMultiDisc(t *testing.T) {
	fs := newFileSet()
	singleTrackCue := "FILE \"T.BIN\" BINARY\n  TRACK 01 MODE1/2048\n    INDEX 01 00:00:00\n"
	fs.set("Disc1.cue", dataEntry([]byte(singleTrackCue), Epoch))
	fs.set("Disc2.cue", dataEntry([]byte(singleTrackCue), Epoch))

	media := []manifest.Media{
		{ID: "disc1", Tracks: []manifest.Track{{Type: manifest.TrackData, SHA1: strings.Repeat("a", 40)}}},
		{ID: "disc2", Tracks: []manifest.Track{{Type: manifest.TrackData, SHA1: strings.Repeat("b", 40)}}},
	}
	boot := &manifest.Boot{}

	if err := renameMediaCue(fs, media, boot, "Example Game"); err != nil {
		t.Fatal(err)
	}

	if _, ok := fs.get("Example Game (disc1).cue"); !ok {
		t.Fatalf("missing disc1 rename, got %v", fs.paths())
	}
	if _, ok := fs.get("Example Game (disc2).cue"); !ok {
		t.Fatalf("missing disc2 rename, got %v", fs.paths())
	}
}

func TestRenameMediaCueIgnoresShapeMismatch(t *testing.T) {
	fs := newFileSet()
	fs.set("Other.cue", dataEntry([]byte("FILE \"T.BIN\" BINARY\n  TRACK 01 MODE1/2048\n    INDEX 01 00:00:00\n"), Epoch))

	media := []manifest.Media{{
		ID: "disc1",
		Tracks: []manifest.Track{
			{Type: manifest.TrackData, SHA1: strings.Repeat("a", 40)},
			{Type: manifest.TrackAudio, SHA1: strings.Repeat("b", 40)},
		},
	}}
	boot := &manifest.Boot{}

	if err := renameMediaCue(fs, media, boot, "Example Game"); err != nil {
		t.Fatal(err)
	}

	if _, ok := fs.get("Other.cue"); !ok {
		t.Fatalf("shape mismatch should not rename: %v", fs.paths())
	}
}

func TestRenameMediaCueNoMediaDeclaredIsNoop(t *testing.T) {
	fs := newFileSet()
	fs.set("Whatever.cue", dataEntry([]byte("FILE \"T.BIN\" BINARY\n  TRACK 01 MODE1/2048\n    INDEX 01 00:00:00\n"), Epoch))
	boot := &manifest.Boot{Disc: "Whatever"}

	if err := renameMediaCue(fs, nil, boot, "Example Game"); err != nil {
		t.Fatal(err)
	}

	if _, ok := fs.get("Whatever.cue"); !ok {
		t.Fatalf("no declared media should not rename anything: %v", fs.paths())
	}
	if boot.Disc != "Whatever" {
		t.Fatalf("boot.Disc changed with no declared media: %q", boot.Disc)
	}
}

func TestRenameMediaCueKeepsDirectory(t *testing.T) {
	fs := newFileSet()
	fs.set("CD/Dump Name.cue", dataEntry([]byte("FILE \"T.BIN\" BINARY\n  TRACK 01 MODE1/2048\n    INDEX 01 00:00:00\n"), Epoch))
	media := []manifest.Media{{ID: "cd", Tracks: []manifest.Track{{Type: manifest.TrackData, SHA1: strings.Repeat("a", 40)}}}}

	if err := renameMediaCue(fs, media, &manifest.Boot{}, "Example Game"); err != nil {
		t.Fatal(err)
	}
	if _, ok := fs.get("CD/Example Game.cue"); !ok {
		t.Fatalf("want CD/Example Game.cue, got %v", fs.paths())
	}
}

func TestRenameMediaCueCollisions(t *testing.T) {
	sheet := []byte("FILE \"T.BIN\" BINARY\n  TRACK 01 MODE1/2048\n    INDEX 01 00:00:00\n")
	media := []manifest.Media{{ID: "disc1", Tracks: []manifest.Track{{Type: manifest.TrackData, SHA1: strings.Repeat("a", 40)}}}}

	t.Run("matching sheet already named", func(t *testing.T) {
		// AAA.cue sorts first but must not be renamed onto Example Game.cue.
		fs := newFileSet()
		fs.set("AAA.cue", dataEntry(sheet, Epoch))
		fs.set("Example Game.cue", dataEntry(sheet, Epoch))
		if err := renameMediaCue(fs, media, &manifest.Boot{}, "Example Game"); err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(fs.paths(), ","); got != "AAA.cue,Example Game.cue" {
			t.Errorf("paths = %s", got)
		}
	})

	t.Run("unrelated file in the way", func(t *testing.T) {
		fs := newFileSet()
		fs.set("Game (USA).cue", dataEntry(sheet, Epoch))
		fs.set("EXAMPLE GAME.CUE", dataEntry([]byte("not a cue sheet"), Epoch))
		if err := renameMediaCue(fs, media, &manifest.Boot{}, "Example Game"); err == nil {
			t.Fatalf("renamed over an existing file; paths %v", fs.paths())
		}
	})
}
