package gui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DemiCloud/doszer/internal/builtin"
)

// TestScreenshot draws the window over a real ROM directory and saves it
// as PNGs, to look at: DOSZER_GUI_ROMS (the ROM directory; - for the
// configuration's), DOSZER_GUI_OUT
// (where the PNGs go), DOSZER_GUI_SYSTEM (a system directory, optional),
// DOSZER_GUI_STATS (a play time file, optional, only read),
// DOSZER_GUI_EXE (a doszer binary, optional: shots running a command),
// DOSZER_GUI_CUE (a disc image, optional: Import a disc; with DOSZER_GUI_BUILD
// set, built into the ROM folder). It
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
		"list.png":      func(a *App) { a.Select(1) },
		"grid.png":      func(a *App) { a.Grid(true); a.Select(2) },
		"search.png":    func(a *App) { a.Search("betrayal") },
		"none.png":      func(a *App) { a.Search("zzzz") },
		"systems.png":   func(a *App) { a.Show("systems") },
		"settings.png":  func(a *App) { a.Show("settings") },
		"setup.png":     func(a *App) { a.OpenSetup() },
		"menu.png":      func(a *App) { a.OpenGameMenu(0) },
		"filter.png":    func(a *App) { a.Filter("Win31 (shell)") },
		"filemenu.png":  func(a *App) { a.openMenuItems(a.px(8), a.menuH(), a.menus()[0].items) },
		"add.png":       func(a *App) { a.openAddGame() },
		"buildmenu.png": func(a *App) { a.openMenuItems(a.px(120), a.menuH(), a.menus()[2].items) },
		"toolsmenu.png": func(a *App) {
			m := a.menus()[3].items
			a.openMenuItems(a.px(180), a.menuH(), m)
		},
		"shortcuts.png": func(a *App) { a.showShortcuts() },
		"credits.png":   func(a *App) { a.showCredits() },
		"about.png":     func(a *App) { a.showAbout() },
		"submenu.png": func(a *App) {
			a.openMenuItems(a.px(60), a.menuH(), a.menus()[1].items)
			p := a.popup
			p.child, p.childOf = p.sub(5), 5 // View > Sort by
			p.child.x, p.child.y, p.child.childOf = a.px(60)+a.px(196), a.menuH()+a.px(4)+a.px(30)*4+a.px(9), -1
		},
		"folder.png": func(a *App) { a.openBuildFolder() },
		"folders.png": func(a *App) {
			if items := a.filters(); len(items) > 0 {
				a.openPopup(a.px(480), a.px(12), a.px(200), items, 0, func(int) {})
			}
		},
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
	if cue := os.Getenv("DOSZER_GUI_CUE"); cue != "" && exe != "" && os.Getenv("DOSZER_GUI_BUILD") != "" {
		// Import a disc through to the archive, in the ROM folder: the shot
		// shows the finished dialog.
		shots["addbuild.png"] = func(a *App) {
			a.openAddGame()
			if a.add != nil {
				a.add.useFiles = true
				a.add.files[0].set(cue)
				a.addGameNext()
				a.add.name.set("Test Game")
				a.addGameRun()
			}
		}
	}
	if cue := os.Getenv("DOSZER_GUI_CUE"); cue != "" { // a disc image, for Import a disc's options
		shots["addoptions.png"] = func(a *App) {
			a.openAddGame()
			if a.add != nil {
				a.add.useFiles = true
				a.add.files[0].set(cue)
				a.addGameNext()
			}
		}
	}
	// A source as install leaves one, for the program choice.
	src := filepath.Join(t.TempDir(), "Some Game")
	for p, data := range map[string]string{
		"DOSZ.TOML":        "format = 1\nname = 'Some Game'\n",
		"GAME/GAME.EXE":    "MZ" + string(make([]byte, 62)),
		"GAME/RUNGAME.BAT": "@echo off\r\ngame\r\n",
		"GAME/SNDSET.EXE":  "MZ" + string(make([]byte, 62)),
		"GAME/SETUP.EXE":   "MZ" + string(make([]byte, 62)),
	} {
		os.MkdirAll(filepath.Dir(filepath.Join(src, p)), 0o755)
		os.WriteFile(filepath.Join(src, p), []byte(data), 0o644)
	}
	shots["addstart.png"] = func(a *App) {
		a.chooseStart(&addPlan{name: "Some Game", src: src, out: filepath.Join(t.TempDir(), "Some Game.dosz")})
	}
	shots["prompt.png"] = func(a *App) {
		a.runSteps("Add Some Game", []step{{prompt: "Next, DOSBox Pure opens with the game's disc. Run its setup (SETUP.EXE or INSTALL.EXE: in Windows, from the CD drive), install the game, then close DOSBox Pure's window."}}, nil)
		a.jobs = nil // the shot shows it waiting
	}
	for name, setup := range shots {
		romFlag := roms
		if roms == "-" {
			romFlag = "" // the configuration's
		}
		err := Run(Options{
			ROMDir:     romFlag,
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
