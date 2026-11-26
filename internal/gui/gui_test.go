package gui

import (
	"testing"
	"time"

	"github.com/DemiCloud/doszer/internal/library"
)

func TestRefilter(t *testing.T) {
	a := &app{scale: 1, h: 600, w: 800, games: []library.Game{
		{Path: "/r/b.dosz", Rel: "dos/b.dosz", Name: "Beta", Size: 30, PlayTime: time.Hour},
		{Path: "/r/a.dosz", Rel: "windows9x/a.dosz", Name: "alpha", System: "Windows 98", Size: 10, LastPlayed: time.Unix(200, 0)},
		{Path: "/r/c.dosz", Rel: "dos/c.dosz", Name: "Gamma", Size: 20, LastPlayed: time.Unix(100, 0), PlayTime: 2 * time.Hour},
	}}
	names := func() (out []string) {
		for _, i := range a.view {
			out = append(out, a.games[i].Name)
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
		a.query, a.sort = tt.query, tt.sort
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
