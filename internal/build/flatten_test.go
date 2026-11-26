package build_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/DemiCloud/doszer/internal/build"
	"github.com/DemiCloud/doszer/internal/manifest"
)

// TestFlattenOption: Options.Flatten collapses an installed game's
// single-folder chain and records it in the embedded manifest - flatten
// on, a rule removing the collapsed name from the text file naming it,
// boot.exec moved - so building that manifest again changes nothing.
func TestFlattenOption(t *testing.T) {
	tests := []struct {
		name     string
		manifest string
		wantExec string
	}{
		{name: "exec moved", manifest: "format = 1\nname = 'Game'\n[boot]\nexec = 'C:\\VENDOR\\GAME\\GAME.EXE -x'\n", wantExec: `C:\GAME\GAME.EXE -x`},
		{name: "already flat", manifest: "format = 1\nname = 'Game'\n[boot]\nexec = 'C:\\VENDOR\\GAME\\GAME.EXE'\n[layout]\nflatten = true\n", wantExec: `C:\VENDOR\GAME\GAME.EXE`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := t.TempDir()
			writeFile(t, src, "DOSZ.TOML", tt.manifest)
			writeFile(t, src, "VENDOR/GAME/GAME.EXE", "MZ")
			writeFile(t, src, "VENDOR/GAME/GAME.CFG", "dir=C:\\VENDOR\\GAME\r\n")
			out := filepath.Join(t.TempDir(), "Game.dosz")
			if _, err := build.Run(build.Options{SourceDir: src, OutputPath: out, Flatten: true}); err != nil {
				t.Fatal(err)
			}
			files := readZip(t, out)
			m, err := manifest.Parse([]byte(readZipFile(t, files[manifest.FileName])), "Game")
			if err != nil {
				t.Fatal(err)
			}
			if m.Boot.Exec != tt.wantExec {
				t.Errorf("exec = %q, want %q", m.Boot.Exec, tt.wantExec)
			}
			if !m.Layout.Flatten {
				t.Error("flatten not recorded")
			}
			if tt.name == "already flat" {
				return // the manifest's own flatten: nothing recorded
			}
			if len(m.Layout.Rewrite) != 1 || m.Layout.Rewrite[0].From != `VENDOR\` || len(m.Layout.Rewrite[0].Files) != 1 {
				t.Errorf("rewrite = %+v, want one rule for VENDOR\\ in GAME/GAME.CFG", m.Layout.Rewrite)
			}
			if got := readZipFile(t, files["GAME/GAME.CFG"]); got != "dir=C:\\GAME\r\n" {
				t.Errorf("GAME.CFG = %q", got)
			}
			// Built again from the embedded manifest: the same archive.
			again := t.TempDir()
			for name, f := range files {
				writeFile(t, again, name, readZipFile(t, f))
				os.Chtimes(filepath.Join(again, name), f.Modified, f.Modified)
			}
			out2 := filepath.Join(t.TempDir(), "Game.dosz")
			if _, err := build.Run(build.Options{SourceDir: again, OutputPath: out2}); err != nil {
				t.Fatal(err)
			}
			a, _ := os.ReadFile(out)
			b, _ := os.ReadFile(out2)
			if !bytes.Equal(a, b) {
				t.Error("rebuilding from the recorded manifest changed the archive")
			}
		})
	}
}
