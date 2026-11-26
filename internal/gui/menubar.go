package gui

import (
	"runtime"

	"github.com/DemiCloud/doszer/internal/config"
)

// homepage is doszer's, for Help.
const homepage = "https://github.com/demicloud/doszer"

// menuItem is one entry of a menu bar menu: "-" is a separator, keys the
// shortcut shown beside it.
type menuItem struct {
	label, keys string
	fn          func()
}

// menus are the menu bar's menus, as they are now.
func (a *app) menus() []struct {
	title string
	items []menuItem
} {
	open := []menuItem{}
	if canOpen() {
		for _, d := range a.romDirs {
			d := d
			open = append(open, menuItem{label: "Open " + d.Path, fn: func() { a.open(d.Path) }})
		}
		if a.sysDir.Path != "" {
			open = append(open, menuItem{label: "Open " + a.sysDir.Path, fn: func() { a.open(a.sysDir.Path) }})
		}
	}
	file := []menuItem{{"Add a game…", "Ctrl+N", a.openAddGame}}
	if len(open) > 0 {
		file = append(file, menuItem{label: "-"})
		file = append(file, open...)
	}
	file = append(file, menuItem{label: "-"}, menuItem{"Quit", "Ctrl+Q", func() { a.quit = true }})

	view := []menuItem{
		{"Games", "Ctrl+1", func() { a.show(pageGames) }},
		{"Systems", "Ctrl+2", func() { a.show(pageSystems) }},
		{"Settings", "Ctrl+3", func() { a.show(pageSettings) }},
		{label: "-"},
		{"List", "Ctrl+L", func() { a.show(pageGames); a.Grid(false) }},
		{"Grid", "Ctrl+G", func() { a.show(pageGames); a.Grid(true) }},
		{label: "-"},
		{"Refresh", "F5", func() { a.rescan(); a.rescanSystems() }},
	}

	tools := []menuItem{
		{"Verify all games", "", func() {
			if len(a.romDirs) > 0 {
				a.runDoszer("Verify all games", append([]string{"verify", "--format", "plain", "--all"}, config.DirPaths(a.romDirs)...), nil)
			}
		}},
		{"Show doszer's environment", "", func() { a.runDoszer("Environment", []string{"env", "--format", "plain"}, nil) }},
	}
	if !config.Configured() {
		tools = append(tools, menuItem{"Set up doszer…", "", a.openSetup})
	}
	if runtime.GOOS == "windows" {
		tools = append(tools, menuItem{label: "-"}, menuItem{"Open .dosz files with doszer", "", func() {
			a.confirm("Open .dosz files with doszer?", "Associate", func() {
				a.runDoszer("Associate .dosz files", []string{"associate", "--format", "plain"}, nil)
			}, "Registers this doszer for your user as the program that opens", ".dosz files in Explorer, with its icon.")
		}})
	}

	help := []menuItem{
		{"Keyboard shortcuts", "", func() {
			a.alert("Keyboard shortcuts",
				"Ctrl+N  add a game          Ctrl+Q  quit",
				"Ctrl+1/2/3  Games, Systems, Settings",
				"Ctrl+L / Ctrl+G  list or grid   F5  rescan",
				"Type to search, Esc to clear; arrows, Page Up/Down, Home, End move",
				"Enter or a double click plays; a right click opens a game's menu")
		}},
		{"doszer's versions", "", func() { a.runDoszer("Versions", []string{"version", "--format", "plain"}, nil) }},
	}
	if canOpen() {
		help = append(help, menuItem{"doszer on the web", "", func() { a.open(homepage) }})
	}
	return []struct {
		title string
		items []menuItem
	}{{"File", file}, {"View", view}, {"Tools", tools}, {"Help", help}}
}

func (a *app) drawMenuBar() {
	mh := a.menuH()
	a.fill(0, 0, a.w, mh, colMenuBar)
	x := a.px(8)
	lh := a.lineHeight(13, false)
	for _, m := range a.menus() {
		w := a.textWidth(m.title, 13, false) + a.px(20)
		if a.hovered(x, 0, w, mh) {
			a.round(x, a.px(3), w, mh-a.px(6), a.px(5), colCardHover)
		}
		a.text(m.title, x+a.px(10), (mh-lh)/2, 13, false, colText)
		items := m.items
		mx := x
		a.onClick(x, 0, w, mh, func(bool) {
			labels := make([]string, len(items))
			for i, it := range items {
				labels[i] = it.label
				if it.keys != "" {
					labels[i] += "\t" + it.keys
				}
			}
			a.openMenu(mx, mh, labels, func(i int) {
				if items[i].fn != nil {
					items[i].fn()
				}
			})
		})
		x += w
	}
}
