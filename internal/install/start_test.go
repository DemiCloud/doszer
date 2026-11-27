package install

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestStartPrograms(t *testing.T) {
	add := func(paths ...string) []Change {
		var cs []Change
		for _, p := range paths {
			cs = append(cs, Change{Path: p, Kind: Added})
		}
		return cs
	}
	tests := []struct {
		name     string
		changes  []Change
		dir      string
		programs []string
	}{
		{
			"launcher beside the game folder (Sierra)",
			add("SIERRA/KQ7DOS.BAT", "SIERRA/KQ7DOS/KQ7DOS.BAT", "SIERRA/KQ7DOS/SIERRA.EXE",
				"SIERRA/KQ7DOS/CHECKCD.BAT", "SIERRA/KQ7DOS/RESOURCE.CFG", "SIERRA/KQ7DOS/INSTALL.EXE"),
			"SIERRA",
			[]string{"SIERRA/KQ7DOS.BAT", "SIERRA/KQ7DOS/CHECKCD.BAT", "SIERRA/KQ7DOS/KQ7DOS.BAT",
				"SIERRA/KQ7DOS/INSTALL.EXE", "SIERRA/KQ7DOS/SIERRA.EXE"},
		},
		{
			"programs only: no pick",
			add("GAME/GAME.EXE", "GAME/SETUP.EXE", "GAME/DATA/LEVEL1.DAT"),
			"GAME",
			[]string{"GAME/GAME.EXE", "GAME/SETUP.EXE"},
		},
		{
			"one batch file among programs",
			add("GAME/SETUP.EXE", "GAME/PLAY.BAT", "GAME/GAME.COM"),
			"GAME",
			[]string{"GAME/PLAY.BAT", "GAME/GAME.COM", "GAME/SETUP.EXE"},
		},
		{
			"two batch files: no pick",
			add("GAME/PLAY.BAT", "GAME/SETSOUND.BAT"),
			"GAME",
			[]string{"GAME/PLAY.BAT", "GAME/SETSOUND.BAT"},
		},
		{
			"deeper than one folder, outside dir and deleted are left out",
			append(add("GAME/A/B/DEEP.BAT", "OTHER/RUN.BAT", "GAME/A/RUN.EXE"), Change{Path: "GAME/OLD.BAT", Kind: Deleted}),
			"GAME",
			[]string{"GAME/A/RUN.EXE"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StartPrograms(tt.changes, tt.dir)
			if !reflect.DeepEqual(got, tt.programs) {
				t.Errorf("programs %q, want %q", got, tt.programs)
			}
		})
	}
}

func TestRunFromPureZipSetsExec(t *testing.T) {
	save := filepath.Join(t.TempDir(), "x.pure.zip")
	os.WriteFile(save, zipBytes(t, map[string]string{
		"SIERRA/GO.BAT":       "@echo off\r\ncd game\r\nrun\r\n",
		"SIERRA/GAME/RUN.EXE": "MZ" + strings.Repeat("\x00", 62),
	}), 0o644)
	manifest := "format = 1\n[boot]\n# exec = 'C:\\GAME\\GAME.EXE'\n"

	src := writeSource(t, manifest, map[string]string{"README.TXT": ""})
	rep, err := Run(Options{Source: src, From: save, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Exec != "" || !reflect.DeepEqual(rep.Programs, []string{`C:\SIERRA\GO.BAT`, `C:\SIERRA\GAME\RUN.EXE`}) {
		t.Fatalf("report %+v", rep) // a dry run lists the programs, picks none
	}
	if b, _ := os.ReadFile(filepath.Join(src, "DOSZ.TOML")); string(b) != manifest {
		t.Fatal("dry run changed the manifest")
	}

	if _, err := Run(Options{Source: src, From: save}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(src, "DOSZ.TOML")); !strings.Contains(string(b), "\nexec = 'C:\\SIERRA\\GO.BAT'\n") {
		t.Fatalf("exec not set:\n%s", b)
	}

	// A manifest that already has exec keeps it.
	src = writeSource(t, "format = 1\n[boot]\nexec = 'C:\\MINE.BAT'\n", map[string]string{"README.TXT": ""})
	rep, err = Run(Options{Source: src, From: save})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Exec != "" || len(rep.Programs) != 2 {
		t.Fatalf("report %+v", rep)
	}
}
