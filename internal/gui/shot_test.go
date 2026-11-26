package gui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DemiCloud/doszer/internal/builtin"
	"github.com/DemiCloud/doszer/internal/config"
)

// TestScreenshot draws the window over a real ROM directory and saves it
// as PNGs, to look at: DOSZER_GUI_ROMS (the ROM directory), DOSZER_GUI_OUT
// (where the PNGs go), DOSZER_GUI_STATS (a play time file, optional). It
// needs a display.
func TestScreenshot(t *testing.T) {
	roms, out := os.Getenv("DOSZER_GUI_ROMS"), os.Getenv("DOSZER_GUI_OUT")
	if roms == "" || out == "" {
		t.Skip("DOSZER_GUI_ROMS and DOSZER_GUI_OUT not set")
	}
	shots := map[string]func(a *App){
		"list.png":   func(a *App) { a.Select(1) },
		"grid.png":   func(a *App) { a.Grid(true); a.Select(2) },
		"search.png": func(a *App) { a.Search("betrayal") },
		"none.png":   func(a *App) { a.Search("zzzz") },
	}
	_, sdlLibs, err := builtin.Extract()
	if err != nil {
		t.Fatal(err)
	}
	stats := os.Getenv("DOSZER_GUI_STATS") // optional play time file, read only
	for name, setup := range shots {
		if stats == "" {
			stats = filepath.Join(t.TempDir(), "playtime.json")
		}
		err := Run(Options{
			ROMDir:     config.Dir{Path: roms, From: "test"},
			Version:    "test",
			SDL:        sdlLibs,
			StatsPath:  stats,
			Screenshot: filepath.Join(out, name),
			Setup:      setup,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
