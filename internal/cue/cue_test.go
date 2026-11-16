package cue

import (
	"fmt"
	"reflect"
	"testing"
)

func TestParseBasic(t *testing.T) {
	data := []byte(`FILE "game.bin" BINARY
  TRACK 01 MODE1/2352
    INDEX 01 00:00:00
`)
	sheet, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	want := &Sheet{Files: []File{{
		Name: "game.bin", Type: "BINARY",
		Tracks: []Track{{Number: 1, Mode: "MODE1/2352", Indexes: []Index{{Number: 1, Frames: 0}}}},
	}}}
	if !reflect.DeepEqual(sheet, want) {
		t.Errorf("Parse() = %+v, want %+v", sheet, want)
	}
}

func TestParseMultiTrackWithPregap(t *testing.T) {
	// Classic single-BIN layout: a data track, then an audio track with a
	// 2-second pregap (INDEX 00) before its real start (INDEX 01).
	data := []byte(`FILE "game.bin" BINARY
  TRACK 01 MODE2/2352
    INDEX 01 00:00:00
  TRACK 02 AUDIO
    INDEX 00 00:10:00
    INDEX 01 00:12:00
`)
	sheet, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(sheet.Files) != 1 || len(sheet.Files[0].Tracks) != 2 {
		t.Fatalf("unexpected structure: %+v", sheet)
	}

	t2 := sheet.Files[0].Tracks[1]
	if t2.Indexes[0].Frames != 10*FramesPerSecond || t2.Indexes[1].Frames != 12*FramesPerSecond {
		t.Errorf("track 2 indexes = %+v", t2.Indexes)
	}
}

func TestParseIgnoresUnknownCommands(t *testing.T) {
	data := []byte(`REM GENRE "Platform"
CATALOG 1234567890123
FILE "game.bin" BINARY
  TRACK 01 MODE1/2352
    PREGAP 00:02:00
    INDEX 01 00:00:00
    FLAGS DCP
`)
	sheet, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(sheet.Files) != 1 || len(sheet.Files[0].Tracks) != 1 {
		t.Fatalf("unexpected structure: %+v", sheet)
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{"track before file", "TRACK 01 MODE1/2352\n"},
		{"index before track", "FILE \"a.bin\" BINARY\nINDEX 01 00:00:00\n"},
		{"file missing type", "FILE \"a.bin\"\n"},
		{"track missing mode", "FILE \"a.bin\" BINARY\nTRACK 01\n"},
		{"bad track number", "FILE \"a.bin\" BINARY\nTRACK XX MODE1/2352\n"},
		{"bad timecode", "FILE \"a.bin\" BINARY\nTRACK 01 MODE1/2352\nINDEX 01 bad\n"},
		{"unterminated quote", "FILE \"a.bin BINARY\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Parse([]byte(tt.data)); err == nil {
				t.Fatal("Parse succeeded, want error")
			}
		})
	}
}

func TestTrackFrameSize(t *testing.T) {
	tests := []struct {
		mode string
		want int
	}{
		{"AUDIO", 2352},
		{"MODE1/2352", 2352},
		{"MODE2/2352", 2352},
		{"MODE1/2048", 2048},
	}
	for _, tt := range tests {
		track := Track{Mode: tt.mode}
		got, err := track.FrameSize()
		if err != nil {
			t.Fatalf("FrameSize(%q): %v", tt.mode, err)
		}
		if got != tt.want {
			t.Errorf("FrameSize(%q) = %d, want %d", tt.mode, got, tt.want)
		}
	}
}

func TestPregapAndFormat(t *testing.T) {
	tests := []struct {
		name       string
		in         string
		wantPregap []int  // TotalPregap per track
		wantText   string // Format output
	}{
		{
			name: "INDEX 00 span",
			in: "FILE \"A.BIN\" BINARY\n  TRACK 01 MODE1/2352\n    INDEX 01 00:00:00\n" +
				"  TRACK 02 AUDIO\n    INDEX 00 01:00:00\n    INDEX 01 01:02:00\n",
			wantPregap: []int{0, 150},
			wantText: "FILE \"A.BIN\" BINARY\r\n  TRACK 01 MODE1/2352\r\n    INDEX 01 00:00:00\r\n" +
				"  TRACK 02 AUDIO\r\n    INDEX 00 01:00:00\r\n    INDEX 01 01:02:00\r\n",
		},
		{
			name: "PREGAP plus INDEX 00, other commands dropped",
			in: "REM COMMENT x\nFILE \"B.WAV\" WAVE\n  TRACK 02 AUDIO\n    FLAGS DCP\n    PREGAP 00:00:10\n" +
				"    INDEX 00 00:00:00\n    INDEX 01 00:01:00\n",
			wantPregap: []int{85},
			wantText:   "FILE \"B.WAV\" WAVE\r\n  TRACK 02 AUDIO\r\n    PREGAP 00:00:10\r\n    INDEX 00 00:00:00\r\n    INDEX 01 00:01:00\r\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := Parse([]byte(tt.in))
			if err != nil {
				t.Fatal(err)
			}
			var got []int
			for _, f := range s.Files {
				for _, tr := range f.Tracks {
					n, err := tr.TotalPregap()
					if err != nil {
						t.Fatal(err)
					}
					got = append(got, n)
				}
			}
			if fmt.Sprint(got) != fmt.Sprint(tt.wantPregap) {
				t.Errorf("TotalPregap = %v, want %v", got, tt.wantPregap)
			}
			if text := string(s.Format()); text != tt.wantText {
				t.Errorf("Format = %q, want %q", text, tt.wantText)
			}
		})
	}
}

func TestResolveWith(t *testing.T) {
	s, err := Parse([]byte("FILE \"X.BIN\" BINARY\n  TRACK 01 MODE1/2352\n    INDEX 01 00:00:00\n" +
		"  TRACK 02 AUDIO\n    INDEX 00 00:00:02\n    INDEX 01 00:00:04\n"))
	if err != nil {
		t.Fatal(err)
	}
	tracks, err := ResolveWith(s, func(name string) (string, int64, error) {
		return "key:" + name, 6 * 2352, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []ResolvedTrack{
		{FilePath: "key:X.BIN", FrameSize: 2352, Start: 0, End: 2 * 2352},
		{FilePath: "key:X.BIN", FrameSize: 2352, Start: 4 * 2352, End: 6 * 2352},
	}
	for i, w := range want {
		g := tracks[i]
		if g.FilePath != w.FilePath || g.FrameSize != w.FrameSize || g.Start != w.Start || g.End != w.End {
			t.Errorf("track %d = %+v, want %+v", i+1, g, w)
		}
	}
}

func TestParseSkipsBOM(t *testing.T) {
	sheet, err := Parse([]byte("\xEF\xBB\xBFFILE \"game.bin\" BINARY\r\n  TRACK 01 MODE1/2352\r\n    INDEX 01 00:00:00\r\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(sheet.Files) != 1 || sheet.Files[0].Name != "game.bin" {
		t.Errorf("Parse() = %+v, want one FILE game.bin", sheet)
	}
}
