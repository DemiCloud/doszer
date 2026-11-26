package gui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DemiCloud/doszer/internal/config"
	"github.com/DemiCloud/doszer/internal/library"
)

func TestRefilter(t *testing.T) {
	a := &app{scale: 1, h: 600, w: 800, games: games{list: []library.Game{
		{Path: "/r/b.dosz", Rel: "dos/b.dosz", Name: "Beta", Size: 30, PlayTime: time.Hour},
		{Path: "/r/a.dosz", Rel: "windows9x/a.dosz", Name: "alpha", System: "Windows 98", Size: 10, LastPlayed: time.Unix(200, 0)},
		{Path: "/r/c.dosz", Rel: "dos/c.dosz", Name: "Gamma", Size: 20, LastPlayed: time.Unix(100, 0), PlayTime: 2 * time.Hour},
	}}}
	names := func() (out []string) {
		for _, i := range a.view {
			out = append(out, a.list[i].Name)
		}
		return out
	}
	tests := []struct {
		query string
		sort  sortMode
		want  []string
	}{
		{"", sortName, []string{"alpha", "Beta", "Gamma"}},
		{"", sortLastPlayed, []string{"alpha", "Gamma", "Beta"}},
		{"", sortPlayTime, []string{"Gamma", "Beta", "alpha"}},
		{"", sortSize, []string{"Beta", "Gamma", "alpha"}},
		{"GAM", sortName, []string{"Gamma"}},
		{"windows 98", sortName, []string{"alpha"}},   // by system
		{"dos/", sortName, []string{"Beta", "Gamma"}}, // by folder
		{"nothing", sortName, nil},
	}
	for _, tt := range tests {
		a.search.text, a.sort = tt.query, tt.sort
		a.refilter("/r/c.dosz")
		got := names()
		if len(got) != len(tt.want) {
			t.Errorf("%q %v: %v, want %v", tt.query, tt.sort, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("%q %v: %v, want %v", tt.query, tt.sort, got, tt.want)
				break
			}
		}
		if g := a.selected(); len(tt.want) > 0 && tt.query == "" && (g == nil || g.Name != "Gamma") {
			t.Errorf("%q %v: selection not kept: %+v", tt.query, tt.sort, g)
		}
	}
}

func TestLastError(t *testing.T) {
	tests := []struct{ out, want string }{
		{"[2026-11-25 10:00:00] ERROR: Game.dosz boots dosbox_pure/Win98 from the system directory\r\n", "Game.dosz boots dosbox_pure/Win98 from the system directory"},
		{"something\nexit status 2\n", "exit status 2"},
	}
	for _, tt := range tests {
		b := &tailBuffer{}
		b.Write([]byte(tt.out))
		if got := b.lastError(); got != tt.want {
			t.Errorf("lastError(%q) = %q, want %q", tt.out, got, tt.want)
		}
	}
}

func TestDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{45 * time.Second: "45s", 12 * time.Minute: "12m", 3*time.Hour + 5*time.Minute: "3h 05m"} {
		if got := duration(d); got != want {
			t.Errorf("duration(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestSetupChoices(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	t.Chdir(t.TempDir())
	newApp := func() *app {
		a := &app{scale: 1, w: 800, h: 600, runCh: make(chan runResult, 1)}
		a.initGames(guiState{})
		a.initSystems()
		a.initSettings()
		return a
	}
	files := func() []string {
		var out []string
		filepath.Walk(home, func(p string, fi os.FileInfo, err error) error {
			if err == nil && !fi.IsDir() {
				out = append(out, p)
			}
			return nil
		})
		return out
	}

	// Don't save: nothing written, and what needed the setup isn't done.
	a := newApp()
	ran := false
	a.whenSetUp(func() { ran = true })
	a.setupChoose(2)
	if ran || a.setup != nil || len(files()) != 0 || config.Configured() {
		t.Fatalf("don't save: ran %v, dialog %v, files %v", ran, a.setup, files())
	}

	// User profile: the config file and the data folders, then the
	// pending action.
	a.whenSetUp(func() { ran = true })
	a.setupChoose(1)
	if !ran || a.setup != nil || !config.Configured() {
		t.Fatalf("user profile: ran %v, dialog %v, configured %v (%s)", ran, a.setup, config.Configured(), a.message)
	}
	if len(a.romDirs) != 1 || a.romDirs[0].Path != filepath.Join(home, "data", "doszer", "roms") {
		t.Errorf("ROM dirs %+v", a.romDirs)
	}
}

func TestSortFiles(t *testing.T) {
	g := &addGame{files: []*field{{}}}
	g.setFiles(0, []string{"/d/Game (USA) (Disc 10).zip", "/d/Game (USA) (Disc 2).zip", "/d/Game (USA) (Disc 1).zip"})
	g.addFile("/d/Game (USA) (Disc 2).zip") // dropped again: once
	var got []string
	for _, f := range g.files {
		got = append(got, filepath.Base(f.text))
	}
	want := []string{"Game (USA) (Disc 1).zip", "Game (USA) (Disc 2).zip", "Game (USA) (Disc 10).zip"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("discs = %v, want %v", got, want)
	}
	if n := suggestName(g.files[0].text); n != "Game" {
		t.Errorf("name = %q", n)
	}
}
