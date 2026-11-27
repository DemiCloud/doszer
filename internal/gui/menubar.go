package gui

import (
	"bytes"
	_ "embed"
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"strings"

	"github.com/DemiCloud/doszer/internal/builtin"
	"github.com/DemiCloud/doszer/internal/config"
	"github.com/DemiCloud/doszer/internal/dump"
)

// homepage is doszer's, for Help.
const homepage = "https://github.com/demicloud/doszer"

// menuItem is one entry of a menu: "-" is a separator, keys the shortcut
// shown beside it, sub a submenu it opens.
type menuItem struct {
	label, keys string
	fn          func()
	sub         []menuItem
}

type menu struct {
	title string
	items []menuItem
}

// menus are the menu bar's menus, as they are now. Every action is the
// doszer command that does it (runDoszer), or a dialog leading to one.
func (a *app) menus() []menu {
	file := []menuItem{
		{label: "Open…", keys: "Ctrl+O", fn: a.openFile},
		{label: "Export all games…", fn: func() { a.openExport("Export all games", nil) }},
		{label: "-"},
		{label: "Quit", keys: "Ctrl+Q", fn: func() { a.quit = true }},
	}
	sorts := make([]menuItem, 0, sortModes)
	for m := sortName; m < sortModes; m++ {
		m := m
		sorts = append(sorts, menuItem{label: m.String(), fn: func() { a.show(pageGames); a.sort = m; a.refilter(a.keepPath()) }})
	}
	view := []menuItem{
		{label: "List", keys: "Ctrl+L", fn: func() { a.show(pageGames); a.Grid(false) }},
		{label: "Grid", keys: "Ctrl+G", fn: func() { a.show(pageGames); a.Grid(true) }},
		{label: "Sort by", sub: sorts},
		{label: "-"},
		{label: "Refresh", keys: "F5", fn: a.refresh},
	}
	build := []menuItem{
		{label: "Import a disc…", keys: "Ctrl+N", fn: a.openAddGame},
		{label: "Build a folder…", keys: "Ctrl+B", fn: a.openBuildFolder},
		{label: "-"},
		{label: "Repack an archive…", fn: a.repackFile},
		{label: "Prepare a shell or OS image…", fn: a.preparePicker},
	}
	tools := []menuItem{
		{label: "Settings…", keys: "Ctrl+,", fn: a.openSettings},
		{label: "Verify", sub: []menuItem{
			{label: "An archive…", fn: a.verifyFile},
			{label: "All games", fn: a.verifyAll},
		}},
		{label: "Lint", sub: []menuItem{
			{label: "An archive…", fn: a.lintFile},
			{label: "All games", fn: a.lintAll},
		}},
		{label: "Environment", fn: func() { a.runDoszer("Environment", []string{"env"}, nil) }},
	}
	if !config.Configured() {
		tools = append(tools, menuItem{label: "-"}, menuItem{label: "Set up doszer…", fn: a.openSetup})
	}
	help := []menuItem{
		{label: "Keyboard shortcuts", fn: a.showShortcuts},
		{label: "Credits", fn: a.showCredits},
		{label: "-"},
		{label: "About doszer", fn: a.showAbout},
	}
	return []menu{{"File", file}, {"View", view}, {"Build", build}, {"Tools", tools}, {"Help", help}}
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
		items, mx := m.items, x
		a.onClick(x, 0, w, mh, func(bool) { a.openMenuItems(mx, mh, items) })
		x += w
	}
}

// openMenuItems opens a menu's items at x, y; a submenu opens beside its
// item, the menu staying open.
func (a *app) openMenuItems(x, y int32, items []menuItem) {
	a.popup = a.menuPopup(x, y, items)
}

func (a *app) menuPopup(x, y int32, items []menuItem) *popup {
	labels := make([]string, len(items))
	for i, it := range items {
		labels[i] = it.label
		switch {
		case it.sub != nil:
			labels[i] += "\t►"
		case it.keys != "":
			labels[i] += "\t" + it.keys
		}
	}
	return &popup{x: x, y: y, w: a.px(200), items: labels, current: -1, childOf: -1,
		pick: func(i int) {
			if items[i].fn != nil {
				items[i].fn()
			}
		},
		sub: func(i int) *popup {
			if items[i].sub == nil {
				return nil
			}
			return a.menuPopup(0, 0, items[i].sub)
		},
	}
}

// openFile asks for an archive and plays it (File > Open).
func (a *app) openFile() {
	if p, ok := pickFile("Open an archive", fileArchive); ok {
		a.playFile(p)
	}
}

// drop handles a file dropped on the window: an archive plays, a disc
// image or a folder starts an import or a build.
func (a *app) drop(p string) {
	if a.setup != nil || a.dialog != nil || a.settingsOpen {
		return
	}
	fi, err := os.Stat(p)
	if err != nil {
		a.say(err.Error(), true)
		return
	}
	switch ext := strings.ToLower(filepath.Ext(p)); {
	case fi.IsDir():
		a.openBuildFolder()
		if a.add != nil {
			a.add.folder.set(p)
		}
	case dump.IsArchive(p):
		kind, err := dump.ArchiveKind(p)
		switch {
		case err != nil:
			a.alert("Can't open "+filepath.Base(p), err.Error())
		case kind == dump.ArchiveDoszer && ext == ".dosz":
			a.playFile(p)
		case kind == dump.ArchiveDoszer && ext == ".dosp":
			a.showDeps(p, strings.TrimSuffix(filepath.Base(p), ext))
		case kind == dump.ArchiveDoszer:
			a.repackInto(p)
		default:
			// A disc image in a zip, a game's files, or an archive made
			// without doszer: imported, then built as doszer builds.
			a.importFile(p)
		}
	case ext == ".cue" || ext == ".iso" || dump.IsDiskImage(p):
		a.importFile(p)
	default:
		a.alert("Can't open "+filepath.Base(p), "Open a game's archive (.dosz, .zip), a disc image (.cue, .iso), a floppy image (.img, .ima, .vfd) or a game's folder.")
	}
}

// importFile opens Import with p among its files.
func (a *app) importFile(p string) {
	if a.add == nil {
		a.openAddGame()
	}
	if a.add != nil && a.add.page == 0 && !a.add.useFolder {
		a.add.useFiles = true
		a.add.addFile(p)
	}
}

// repackInto repacks a doszer archive not named as one (a .zip with a
// DOSZ.TOML) into the ROM folder as a .dosz.
func (a *app) repackInto(p string) {
	if len(a.romDirs) == 0 {
		a.alert("No ROM folder", "Choose where games go first: Tools, Settings.")
		return
	}
	name := strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
	out := filepath.Join(a.romDirs[0].Path, name+".dosz")
	a.confirm("Repack "+filepath.Base(p)+"?", "Repack", func() {
		a.runDoszer("Repack: "+name, []string{"repack", p, out}, func(err error) {
			if err == nil {
				a.selectAfterScan = out
				a.rescan()
			}
		})
	}, "A doszer archive: rebuilt from its manifest into "+a.romDirs[0].Path+".")
}

// repackFile asks for an archive and repacks it in place.
func (a *app) repackFile() {
	p, ok := pickFile("Repack an archive", fileArchive)
	if !ok {
		return
	}
	name := filepath.Base(p)
	a.confirm("Repack "+name+"?", "Repack", func() {
		a.runDoszer("Repack: "+name, []string{"repack", p, p}, func(err error) {
			if err == nil {
				a.rescan()
			}
		})
	}, "Rebuilds the archive from its embedded manifest, in place.")
}

// preparePicker offers the system folder's shells and images to prepare.
func (a *app) preparePicker() {
	var files []systemFile
	var items []string
	for _, f := range a.files {
		if !f.missing {
			files = append(files, f)
			items = append(items, f.Name)
		}
	}
	if len(files) == 0 {
		a.alert("Nothing to prepare", "The system folder has no shells or OS images: "+a.sysDir.Path)
		return
	}
	a.openMenu(a.px(120), a.menuH(), items, func(i int) { a.prepare(files[i]) })
}

func (a *app) verifyFile() {
	if p, ok := pickFile("Verify an archive", fileArchive); ok {
		a.runDoszer("Verify: "+filepath.Base(p), []string{"verify", p}, nil)
	}
}

func (a *app) verifyAll() {
	if len(a.romDirs) == 0 {
		a.alert("No ROM folder", "Choose where games go first: Tools, Settings.")
		return
	}
	a.runDoszer("Verify all games", append([]string{"verify", "--all"}, config.DirPaths(a.romDirs)...), nil)
}

// lint runs `doszer lint` on an archive.
func (a *app) lint(path, name string) {
	a.runDoszer("Lint: "+name, []string{"lint", path}, nil)
}

func (a *app) lintFile() {
	if p, ok := pickFile("Lint an archive", fileArchive); ok {
		a.lint(p, filepath.Base(p))
	}
}

func (a *app) lintAll() {
	if len(a.romDirs) == 0 {
		a.alert("No ROM folder", "Choose where games go first: Tools, Settings.")
		return
	}
	a.runDoszer("Lint all games", append([]string{"lint", "--all"}, config.DirPaths(a.romDirs)...), nil)
}

// showShortcuts lists the keyboard shortcuts as a table.
func (a *app) showShortcuts() {
	a.dialog = &dialog{title: "Keyboard shortcuts", rows: []dialogRow{
		{"Ctrl+O", "Open an archive and play it", ""},
		{"Ctrl+N", "Import a disc", ""},
		{"Ctrl+B", "Build a folder", ""},
		{"Ctrl+,", "Settings", ""},
		{"Ctrl+1 to Ctrl+3", "Dashboard, Library, Logs", ""},
		{"Ctrl+L, Ctrl+G", "List, grid", ""},
		{"F5", "Refresh", ""},
		{"Ctrl+Q", "Quit", ""},
		{"Typing, Esc", "Search the games, clear the search", ""},
		{"Arrows, Page Up/Down", "Move through the games", ""},
		{"Enter, double click", "Play the selected game", ""},
		{"Right click", "A game's menu", ""},
	}, buttons: []dialogButton{{label: "Close", style: btnAccent}}}
}

// showCredits lists doszer and the programs it embeds, linked.
func (a *app) showCredits() {
	rows := []dialogRow{{"doszer", a.o.Version, homepage}}
	for _, p := range builtin.Programs() {
		v := p.Version
		if !p.Embedded {
			v += " (not in this build)"
		}
		rows = append(rows, dialogRow{p.Name, v, p.URL})
	}
	rows = append(rows, dialogRow{"Go fonts", "", "https://go.dev/blog/go-fonts"})
	a.dialog = &dialog{title: "Credits", rows: rows,
		buttons: []dialogButton{{label: "Close", style: btnAccent}}}
}

// aboutIcon is doszer's icon at 256 pixels (`make icon`).
//
//go:embed icon256.png
var aboutIcon []byte

// showAbout shows doszer's icon, name, version and what it is. A right
// click on the icon saves it.
func (a *app) showAbout() {
	if a.aboutTex == nil {
		if img, err := png.Decode(bytes.NewReader(aboutIcon)); err == nil {
			b := img.Bounds()
			n := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
			draw.Draw(n, n.Bounds(), img, b.Min, draw.Src)
			a.aboutTex = a.upload(n, true)
		}
	}
	a.dialog = &dialog{
		title:  "doszer",
		center: true,
		image:  a.aboutTex,
		imageS: 160,
		lines: []string{
			"Version " + a.o.Version,
			"Written by DemiCloud",
			"",
			"A .dosz packer and player for DOSBox Pure: it dumps discs,",
			"installs and builds games, and runs them.",
		},
		onImage: func() {
			a.openMenu(a.mx, a.my, []string{"Save image…"}, func(int) {
				p, ok := pickSave("Save doszer's icon", "doszer.png")
				if !ok {
					return
				}
				if err := os.WriteFile(p, aboutIcon, 0o644); err != nil {
					a.say(err.Error(), true)
					return
				}
				a.say("Saved "+p, false)
			})
		},
		buttons: []dialogButton{{label: "Close", style: btnAccent}},
	}
}
