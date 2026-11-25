package repack_test

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/DemiCloud/doszer/internal/manifest"
	"github.com/DemiCloud/doszer/internal/repack"
	"github.com/DemiCloud/doszer/internal/verify"
)

const osManifest = `
format = 1
name   = "Test Game"

[boot]
os = 'dosbox_pure/Win98'
`

func change(s string) *repack.Change { c := repack.ParseChange(s); return &c }

// TestSet: Set changes the boot settings it applies to, rewrites the
// runtime files that follow from them, never leaves an old parent marker
// behind (DOSBox Pure refuses an archive with two, §6.8) and produces
// exactly the archive repack would from the edited manifest.
func TestSet(t *testing.T) {
	tests := []struct {
		name        string
		manifest    string
		opts        repack.SetOptions
		wantBoot    manifest.Boot
		wantSkipped int
		wantMarkers []string
		wantAuto    string
	}{
		{
			name:        "parent replaced",
			manifest:    plainManifest + "parent = 'Base Game.dosz'\n",
			opts:        repack.SetOptions{Parent: change("Base Game (v2).dosz")},
			wantBoot:    manifest.Boot{Exec: `C:\GAME.EXE`, Parent: "Base Game (v2).dosz"},
			wantMarkers: []string{"Base Game (v2).dosz.parent"},
		},
		{
			name:     "parent removed",
			manifest: plainManifest + "parent = 'Base Game.dosz'\n",
			opts:     repack.SetOptions{Parent: change("")},
			wantBoot: manifest.Boot{Exec: `C:\GAME.EXE`},
		},
		{
			name:        "parent from another",
			manifest:    plainManifest + "parent = 'Base Game.dosz'\n",
			opts:        repack.SetOptions{Parent: change("Other.dosz:New.dosz")},
			wantBoot:    manifest.Boot{Exec: `C:\GAME.EXE`, Parent: "Base Game.dosz"},
			wantSkipped: 1,
			wantMarkers: []string{"Base Game.dosz.parent"},
		},
		{
			name:     "os renamed",
			manifest: osManifest,
			opts:     repack.SetOptions{OS: change("dosbox_pure/Win98:dosbox_pure/Windows 98")},
			wantBoot: manifest.Boot{OS: "dosbox_pure/Windows 98"},
			wantAuto: "O*dosbox_pure/Windows 98",
		},
		{
			name:        "os from another",
			manifest:    osManifest,
			opts:        repack.SetOptions{OS: change("dosbox_pure/Win95:Windows 95")},
			wantBoot:    manifest.Boot{OS: "dosbox_pure/Win98"},
			wantSkipped: 1,
			wantAuto:    "O*dosbox_pure/Win98",
		},
		{
			name:        "os on an exec archive",
			manifest:    plainManifest,
			opts:        repack.SetOptions{OS: change("Win98")},
			wantBoot:    manifest.Boot{Exec: `C:\GAME.EXE`},
			wantSkipped: 1,
			wantAuto:    `C:\GAME.EXE`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := buildArchive(t, tt.manifest, map[string]string{"GAME.EXE": "x", "NOTES.parent": "not empty: ordinary content"})
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			opts := tt.opts
			opts.Path = path
			res, err := repack.Set(opts)
			if err != nil {
				t.Fatal(err)
			}
			if res.After != tt.wantBoot {
				t.Errorf("boot = %+v, want %+v", res.After, tt.wantBoot)
			}
			if len(res.Skipped) != tt.wantSkipped {
				t.Errorf("skipped %q, want %d", res.Skipped, tt.wantSkipped)
			}
			after, _ := os.ReadFile(path)
			if !res.Changed() {
				if !bytes.Equal(before, after) {
					t.Error("unchanged archive was rewritten")
				}
				return
			}

			entries := readZip(t, path)
			var markers []string
			for name, f := range entries {
				if !strings.Contains(name, "/") && strings.HasSuffix(name, ".parent") && f.UncompressedSize64 == 0 {
					markers = append(markers, name)
				}
			}
			sort.Strings(markers)
			if strings.Join(markers, "|") != strings.Join(tt.wantMarkers, "|") {
				t.Errorf("markers %q, want %q", markers, tt.wantMarkers)
			}
			if _, ok := entries["NOTES.parent"]; !ok {
				t.Error("a non-empty .parent file was removed")
			}
			if tt.wantAuto != "" {
				if got := readZipFile(t, entries["AUTOBOOT.DBP"]); got != tt.wantAuto {
					t.Errorf("AUTOBOOT.DBP = %q, want %q", got, tt.wantAuto)
				}
			}
			m, err := manifest.Parse([]byte(readZipFile(t, entries[manifest.FileName])), "x")
			if err != nil {
				t.Fatal(err)
			}
			if m.Boot != tt.wantBoot {
				t.Errorf("embedded boot = %+v", m.Boot)
			}
			if _, err := verify.Check(path); err != nil {
				t.Errorf("verify: %v", err)
			}

			// The same as repacking the original with the edited manifest.
			mf := filepath.Join(t.TempDir(), "DOSZ.TOML")
			if err := os.WriteFile(mf, []byte(readZipFile(t, entries[manifest.FileName])), 0o644); err != nil {
				t.Fatal(err)
			}
			orig := filepath.Join(t.TempDir(), "orig.dosz")
			if err := os.WriteFile(orig, before, 0o644); err != nil {
				t.Fatal(err)
			}
			want := filepath.Join(t.TempDir(), "want.dosz")
			if _, err := repack.Run(repack.Options{InputPath: orig, OutputPath: want, ManifestPath: mf}); err != nil {
				t.Fatal(err)
			}
			if w, _ := os.ReadFile(want); !bytes.Equal(w, after) {
				t.Error("differs from repack with the edited manifest")
			}
		})
	}

	path := buildArchive(t, plainManifest, map[string]string{"GAME.EXE": "x"})
	if _, err := repack.Set(repack.SetOptions{Path: path, Parent: change("dir/Base.dosz")}); err == nil || !strings.Contains(err.Error(), "bare file name") {
		t.Errorf("parent with a directory: err = %v", err)
	}
}
