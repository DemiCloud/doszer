package gui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DemiCloud/doszer/internal/builtin"
)

// TestScreenshot draws the window over a real ROM directory and saves it
// as PNGs, to look at: DOSZER_GUI_ROMS (the ROM directory), DOSZER_GUI_OUT
// (where the PNGs go), DOSZER_GUI_SYSTEM (a system directory, optional),
// DOSZER_GUI_STATS (a play time file, optional, only read),
// DOSZER_GUI_EXE (a doszer binary, optional: shots running a command). It
// needs a display. The configuration is the real one (read, never written).
func TestScreenshot(t *testing.T) {
	roms, out := os.Getenv("DOSZER_GUI_ROMS"), os.Getenv("DOSZER_GUI_OUT")
	if roms == "" || out == "" {
		t.Skip("DOSZER_GUI_ROMS and DOSZER_GUI_OUT not set")
	}
	_, sdlLibs, err := builtin.Extract()
	if err != nil {
		t.Fatal(err)
	}
	stats := os.Getenv("DOSZER_GUI_STATS")
	if stats == "" {
		stats = filepath.Join(t.TempDir(), "playtime.json")
	}
	shots := map[string]func(a *App){
		"list.png":     func(a *App) { a.Select(1) },
		"grid.png":     func(a *App) { a.Grid(true); a.Select(2) },
		"search.png":   func(a *App) { a.Search("betrayal") },
		"none.png":     func(a *App) { a.Search("zzzz") },
		"systems.png":  func(a *App) { a.Show("systems") },
		"settings.png": func(a *App) { a.Show("settings") },
		"setup.png":    func(a *App) { a.OpenSetup() },
		"menu.png":     func(a *App) { a.OpenGameMenu(0) },
		"filter.png":   func(a *App) { a.Filter("Win31 (shell)") },
		"prepare.png": func(a *App) {
			a.Show("systems")
			for _, f := range a.files {
				if !f.missing && !f.Shell {
					a.prepare(f)
					return
				}
			}
		},
	}
	exe := os.Getenv("DOSZER_GUI_EXE") // a doszer binary, for the shots that run a command
	if exe != "" {
		shots["deps.png"] = func(a *App) { a.RunDeps(0) }
	}
	for name, setup := range shots {
		err := Run(Options{
			ROMDir:     roms,
			SystemDir:  os.Getenv("DOSZER_GUI_SYSTEM"),
			Version:    "test",
			SDL:        sdlLibs,
			StatsPath:  stats,
			Screenshot: filepath.Join(out, name),
			Setup:      setup,
			Exe:        exe,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
