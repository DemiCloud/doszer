package gui

import (
	"archive/zip"
	"github.com/DemiCloud/doszer/internal/install"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/DemiCloud/doszer/internal/builtin"
	"github.com/DemiCloud/doszer/internal/padmap"
)

// TestScreenshot draws the window over a real ROM directory and saves it
// as PNGs, to look at: DOSZER_GUI_ROMS (the ROM directory; - for the
// configuration's), DOSZER_GUI_OUT
// (where the PNGs go), DOSZER_GUI_SYSTEM (a system directory, optional),
// DOSZER_GUI_STATS (a play time file, optional, only read),
// DOSZER_GUI_LOGS (a run logs folder, optional, only read),
// DOSZER_GUI_SOURCES, DOSZER_GUI_SAVES (sources and saves folders, optional, only read),
// DOSZER_GUI_ONLY (shot names, comma-separated: only those),
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
		"parents.png":   func(a *App) { a.Show("parents") },
		"dashboard.png": func(a *App) { a.Show("dashboard") },
		"logs.png":      func(a *App) { a.Show("logs") },
		"logsbuild.png": func(a *App) { // the newest build, its item open
			a.Show("logs")
			for _, r := range a.runs {
				if r.Build != nil && r.Build.Archive != nil {
					a.selectRun(r.ID)
					break
				}
			}
		},
		"logssession.png": func(a *App) { // a session's summary, open
			a.Show("logs")
			for _, g := range a.groups {
				if g.session() {
					a.logGroup, a.logRun, a.logOpen[g.key] = g.key, "", true
					break
				}
			}
		},
		"logssessionrun.png": func(a *App) { // a session's build
			a.Show("logs")
			for _, r := range a.runs {
				if r.Session != "" && r.Build != nil {
					a.selectRun(r.ID)
					break
				}
			}
		},
		"logsmulti.png": func(a *App) { // two items selected, their menu open
			a.Show("logs")
			if order := a.logOrder(); len(order) >= 2 {
				a.logMulti.all(order[:2])
				a.mx, a.my = a.px(200), a.px(160)
				a.logsMenu()
			}
		},
		"gamesmulti.png": func(a *App) { // games selected, their menu open
			if order := a.gameOrder(); len(order) >= 2 {
				a.gameMulti.all(order[:2])
				a.mx, a.my = a.px(300), a.px(170)
				a.gamesMenu(a.selectedGames())
			}
		},
		"sysright.png": func(a *App) { // right click on the selected system
			a.Show("systems")
			a.render()
			x, y, w, _ := a.area()
			a.mx, a.my = x+w/3, y+a.pad()+a.lineHeight(15, true)+a.px(8)+a.px(30)
			a.click(false) // select it
			a.render()
			a.rightClick()
		},
		"menuhover.png": func(a *App) { // File open, the mouse moved onto View: View opens
			m := a.menus()
			a.openBarMenu(0, a.px(8), m[0].items)
			a.mx = a.px(8) + a.textWidth(m[0].title, 13, false) + a.px(20) + a.px(10)
			a.my = a.menuH() / 2
		},
		"sources.png": func(a *App) { a.Show("sources") },
		"saves.png":   func(a *App) { a.Show("saves") },
		"deletegame.png": func(a *App) { // delete a game: its saves and source offered
			if len(a.view) > 0 {
				a.deleteArchives([]string{a.list[a.view[0]].Path})
			}
		},
		"padedit.png": func(a *App) { // the gamepad editor on the first game with a mapping, else the first
			for i := range a.list {
				if strings.Contains(a.list[i].Name, "Pad") || i == len(a.list)-1 {
					a.openPadEditor(&a.list[i])
					break
				}
			}
		},
		"padimport.png": func(a *App) { // Betrayal in Antara's example mapping imported
			if len(a.list) > 0 {
				a.openPadEditor(&a.list[0])
				a.padImport(filepath.Join("..", "..", "examples", "betrayal-in-antara", "DOSZ.TOML"))
			}
		},
		"padsteam.png": func(a *App) { // the same, drawn as a Steam Controller
			if len(a.list) > 0 {
				a.openPadEditor(&a.list[0])
				a.padImport(filepath.Join("..", "..", "examples", "betrayal-in-antara", "DOSZ.TOML"))
				a.padLayout = 1
				a.selectPadControl(a.padEd, "rstick_left")
			}
		},
		"padempty.png": func(a *App) { // a game with no mapping, two ports
			if len(a.list) > 0 {
				a.openPadEditor(&a.list[0])
				a.padEd.pads = append(a.padEd.pads, padmap.Pad{Controls: map[string]string{}})
				a.padEd.pads[0].Controls["b"] = "space Fire"
			}
		},
		"padmenu.png": func(a *App) { // its Add action menu, Keyboard open
			for i := range a.list {
				if strings.Contains(a.list[i].Name, "Pad") || i == len(a.list)-1 {
					a.openPadEditor(&a.list[i])
					break
				}
			}
			if a.padEd != nil {
				a.render()
				items := a.actionMenu(a.padEd)
				a.openMenuItems(a.w/3, a.h/2, items)
				p := a.popup
				p.child, p.childOf = p.sub(0), 0
				p.child.x, p.child.y, p.child.childOf = a.w/3+a.px(200), a.h/2, -1
			}
		},
		"logsscroll.png": func(a *App) {
			a.Show("logs")
			for _, r := range a.runs {
				if r.Build != nil && r.Build.Archive != nil {
					a.selectRun(r.ID)
					break
				}
			}
			a.logScroll = a.px(600)
		},
		"settings.png": func(a *App) { a.Show("settings") },
		"setup.png":    func(a *App) { a.OpenSetup() },
		"menu.png":     func(a *App) { a.OpenGameMenu(0) },
		"edit.png": func(a *App) {
			if len(a.view) > 0 {
				a.openEditor(&a.list[a.view[0]])
			}
		},
		"edittoml.png": func(a *App) {
			if len(a.view) > 0 {
				a.openEditorMode(&a.list[a.view[0]], editToml)
			}
		},
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
			for i, it := range p.items { // View > Sort by
				if strings.HasPrefix(it, "Sort by") {
					p.child, p.childOf = p.sub(i), i
					p.child.x, p.child.y, p.child.childOf = a.px(60)+a.px(196), a.menuH()+a.px(4)+a.px(30)*5+a.px(9), -1
				}
			}
		},
		"folder.png": func(a *App) { a.openBuildFolder() },
		"installdir.png": func(a *App) { // install's changes span folders
			a.add = &addGame{page: 3, plan: &addPlan{name: "Example Game", src: "/path/to/Example Game"}, dests: []string{"/path/to/roms"},
				retry: &install.Retry{Drive: "C:", From: "x.pure.zip", Folders: []install.Folder{{Name: "GAME", Files: 120, Bytes: 48 << 20}, {Name: "TOOLS", Files: 9, Bytes: 20 << 20}}}}
		},
		"export.png": func(a *App) { a.openExport("Export all games", nil) },
		"child.png": func(a *App) {
			if len(a.parentList) > 0 {
				a.show(pageParents)
				a.openChild(a.parentList[0].Path, a.parentList[0].Name)
			}
		},
		"edgemenu.png": func(a *App) { // a game menu at the right edge: Tools opens left
			a.OpenGameMenu(0)
			p := a.popup
			if p == nil {
				return
			}
			p.x = a.w - a.px(210)
			for i, it := range p.items {
				if strings.HasPrefix(it, "Tools") {
					p.child, p.childOf = p.sub(i), i
					p.child.x, p.child.y, p.child.childOf, p.child.parentX = a.w-a.px(14), p.y+a.px(60), -1, p.x
				}
			}
		},
		"folders.png": func(a *App) {
			if values := a.filters(); len(values) > 0 {
				items := make([]string, len(values))
				for i, v := range values {
					items[i] = filterName(v)
				}
				items[0] = "All"
				a.openPopup(a.px(290), a.menuH()+a.px(12), a.px(200), items, 0, func(int) {})
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
	// An archive made without a manifest, dropped on the window.
	legacy := filepath.Join(t.TempDir(), "Old Game.dosz")
	if f, err := os.Create(legacy); err == nil {
		zw := zip.NewWriter(f)
		w, _ := zw.Create("GAME.EXE")
		w.Write([]byte("MZ"))
		zw.Close()
		f.Close()
	}
	shots["droplegacy.png"] = func(a *App) { a.drop(legacy) }
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
	only := os.Getenv("DOSZER_GUI_ONLY") // comma-separated shot names, without .png
	for name, setup := range shots {
		if only != "" && !slices.Contains(strings.Split(only, ","), strings.TrimSuffix(name, ".png")) {
			continue
		}
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
			LogsDir:    os.Getenv("DOSZER_GUI_LOGS"),
			SourcesDir: os.Getenv("DOSZER_GUI_SOURCES"),
			SaveDir:    os.Getenv("DOSZER_GUI_SAVES"),
			Screenshot: filepath.Join(out, name),
			Setup:      setup,
			Exe:        exe,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
