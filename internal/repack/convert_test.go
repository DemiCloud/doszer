package repack_test

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DemiCloud/doszer/internal/build"
	"github.com/DemiCloud/doszer/internal/manifest"
	"github.com/DemiCloud/doszer/internal/repack"
)

// writeZip writes a plain ZIP (an archive not built by doszer).
func writeZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for name, content := range files {
		fw, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		fw.Write([]byte(content))
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
}

// TestConvert: Convert renames the parent and re-points its children -
// one with a manifest, one without (given a minimal one) - leaving
// exactly one marker naming the new file; a dry run changes nothing.
func TestConvert(t *testing.T) {
	tests := []struct {
		name   string
		ext    string
		dryRun bool
		want   string // the parent's file name afterwards
	}{
		{name: "to parent", ext: repack.ExtParent, want: "Base.dosp"},
		{name: "dry run", ext: repack.ExtParent, dryRun: true, want: "Base.dosz"},
		{name: "already a game", ext: repack.ExtGame, want: "Base.dosz"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeZip(t, filepath.Join(dir, "Base.dosz"), map[string]string{"WINDOWS/WIN.COM": "x", "AUTOBOOT.DBP": "S*Win31\n", "WINDOWS.BAT": "@ECHO OFF\r\nGAME.EXE\r\n"})
			writeZip(t, filepath.Join(dir, "Legacy.dosz"), map[string]string{"Base.dosz.parent": "", "GAME.EXE": "MZ"})
			src := t.TempDir()
			writeFile(t, src, "DOSZ.TOML", plainManifest+"parent = 'Base.dosz'\n")
			writeFile(t, src, "GAME.EXE", "MZ")
			if _, err := build.Run(build.Options{SourceDir: src, OutputPath: filepath.Join(dir, "Built.dosz")}); err != nil {
				t.Fatal(err)
			}

			res, err := repack.Convert(filepath.Join(dir, "Base.dosz"), tt.ext, tt.dryRun, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(dir, tt.want)); err != nil {
				t.Fatalf("parent: %v", err)
			}
			wantMarker := tt.want + ".parent"
			// A parent leaves starting to its children: no WINDOWS.BAT, and
			// the boot its AUTOBOOT.DBP had is kept.
			base := readZip(t, filepath.Join(dir, tt.want))
			_, bat := base["WINDOWS.BAT"]
			if want := tt.want != "Base.dosp"; bat != want {
				t.Errorf("%s has WINDOWS.BAT: %v, want %v", tt.want, bat, want)
			}
			if f, ok := base["AUTOBOOT.DBP"]; !ok || !strings.HasPrefix(readZipFile(t, f), "S*Win31") {
				t.Errorf("%s lost its AUTOBOOT.DBP", tt.want)
			}
			if tt.ext == repack.ExtParent && len(res.Children) != 2 {
				t.Errorf("children = %v, want 2", res.Children)
			}
			for _, child := range []string{"Legacy.dosz", "Built.dosz"} {
				entries := readZip(t, filepath.Join(dir, child))
				var markers []string
				for name := range entries {
					if filepath.Ext(name) == ".parent" {
						markers = append(markers, name)
					}
				}
				if len(markers) != 1 || markers[0] != wantMarker {
					t.Errorf("%s markers = %v, want [%s]", child, markers, wantMarker)
				}
				if _, ok := entries[manifest.FileName]; !ok && !tt.dryRun && tt.ext == repack.ExtParent {
					t.Errorf("%s has no manifest", child)
				}
			}
		})
	}
}
