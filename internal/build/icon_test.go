package build_test

import (
	"crypto/sha1"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DemiCloud/doszer/internal/build"
	"github.com/DemiCloud/doszer/internal/repack"
)

// testICO is a one-image ICO file: header, entry, then 8 bytes of image.
var testICO = "\x00\x00\x01\x00\x01\x00" +
	"\x10\x10\x00\x00\x01\x00\x20\x00\x08\x00\x00\x00\x16\x00\x00\x00" +
	"IMAGEDAT"

func TestRunIcon(t *testing.T) {
	iso := buildMinimalISO(t, "AUTORUN.ICO", []byte(testICO))
	media := fmt.Sprintf(`
[[source.media]]
id = "disc1"
tracks = [{ type = "data", sha1 = "%x" }]
`, sha1.Sum(iso))

	tests := []struct {
		name    string
		files   map[string]string
		icon    string
		wantErr string
		absent  []string // entries that must not be in the archive
	}{
		{"ICO in the build", map[string]string{"GAME/GAME.ICO": testICO}, `path = "GAME/GAME.ICO"`, "", nil},
		{"replaces a DOSZ.ICO", map[string]string{"GAME/GAME.ICO": testICO, "DOSZ.ICO": "old"}, `path = "GAME/GAME.ICO"`, "", nil},
		{"from media, not added to the build", map[string]string{"DISC/game.bin": string(iso), "DISC/game.cue": "FILE \"game.bin\" BINARY\nTRACK 01 MODE1/2048\nINDEX 01 00:00:00\n"},
			`media = "disc1"` + "\n" + `path = "AUTORUN.ICO"`, "", []string{"AUTORUN.ICO"}},
		{"missing", map[string]string{}, `path = "GAME/GAME.ICO"`, "icon: GAME/GAME.ICO is not in the build", nil},
		{"neither ICO nor program", map[string]string{"README.TXT": "hello"}, `path = "README.TXT"`, "neither an ICO file nor a program", nil},
		{"DOS program", map[string]string{"GAME.EXE": "MZ" + strings.Repeat("\x00", 100)}, `path = "GAME.EXE"`, "a DOS program has no icons", nil},
		{"media missing", map[string]string{}, `media = "disc1"` + "\n" + `path = "AUTORUN.ICO"`, "no matching disc image", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for p, c := range tt.files {
				writeFile(t, dir, p, c)
			}
			writeFile(t, dir, "DOSZ.TOML", "format = 1\nname = \"Icon Game\"\n"+media+"\n[icon]\n"+tt.icon+"\n")
			out := filepath.Join(t.TempDir(), "out.dosz")
			_, err := build.Run(build.Options{SourceDir: dir, OutputPath: out})
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			entries := readZip(t, out)
			f, ok := entries["DOSZ.ICO"]
			if !ok {
				t.Fatal("no DOSZ.ICO")
			}
			if got := readZipFile(t, f); got != testICO {
				t.Errorf("DOSZ.ICO = %q", got)
			}
			if f.Modified.Year() != 1980 {
				t.Errorf("DOSZ.ICO dated %v, want the epoch (§8.2)", f.Modified)
			}
			for _, p := range tt.absent {
				if _, ok := entries[p]; ok {
					t.Errorf("%s is in the archive", p)
				}
			}

			// Repack reproduces it, from the archive's media too.
			again := filepath.Join(t.TempDir(), "again.dosz")
			if _, err := repack.Run(repack.Options{InputPath: out, OutputPath: again, OggQuality: 6}); err != nil {
				t.Fatalf("repack: %v", err)
			}
			if got := readZipFile(t, readZip(t, again)["DOSZ.ICO"]); got != testICO {
				t.Errorf("repacked DOSZ.ICO = %q", got)
			}
		})
	}
}
