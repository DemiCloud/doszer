package gui

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/DemiCloud/doszer/internal/chain"
	"github.com/DemiCloud/doszer/internal/install"
	"github.com/DemiCloud/doszer/internal/library"
	"github.com/DemiCloud/doszer/internal/platform"
	"github.com/DemiCloud/doszer/internal/runlog"
)

// gameMenu opens a game's right-click menu at the mouse, its submenus
// cascading. Each action is a doszer command (runDoszer), shown with its
// command line.
func (a *app) gameMenu(g *library.Game) {
	path, name := g.Path, g.Name
	rescan := func(err error) {
		if err == nil {
			a.rescan()
		}
	}
	tools := []menuItem{
		{label: "Info", fn: func() { a.runDoszer("Info: "+name, []string{"info", path}, nil) }},
		{label: "Dependencies", fn: func() { a.showDeps(path, name) }},
		{label: "Verify", fn: func() { a.runDoszer("Verify: "+name, []string{"verify", path}, nil) }},
		{label: "Lint", fn: func() { a.lint(path, name) }},
	}
	if g.Redump != "" && canOpen() {
		rn, rl := g.Redump, g.RedumpLink
		tools = append(tools, menuItem{label: "Redump page", fn: func() { a.openRedump(rn, rl) }})
	}
	if r := runlog.Latest(a.runs, path); r != nil {
		id := r.ID
		tools = append(tools, menuItem{label: "Build log", fn: func() { a.showRun(id) }})
	}
	tools = append(tools, []menuItem{
		{label: "-"},
		{label: "Repack…", fn: func() {
			a.confirm("Repack "+name+"?", "Repack", func() {
				a.runDoszer("Repack: "+name, []string{"repack", path, path}, rescan)
			}, "Rebuilds the archive from its embedded manifest, in place.")
		}},
		{label: "Export…", fn: func() { a.openExport("Export "+name, []string{path}) }},
		{label: "Convert to parent…", fn: func() { a.convert(path, name, "dosp") }},
		{label: "-"},
		{label: "Advanced", sub: []menuItem{
			{label: "Edit WINDOWS.BAT…", fn: func() { a.openEditorMode(g, editBat) }},
			{label: "Edit DOSZ.TOML…", fn: func() { a.openEditorMode(g, editToml) }},
		}},
	}...)
	change := []menuItem{{label: "Parent", sub: a.parentItems(g)}, {label: "Boots", sub: a.bootItems(g)}}
	items := []menuItem{
		{label: "Play", fn: a.play},
		{label: "Edit…", fn: func() { a.openEditor(g) }},
		{label: "Gamepad…", fn: func() { a.openPadEditor(g) }},
		{label: "Saves…", fn: func() { a.showSaves(path) }},
		{label: "-"},
		{label: "Tools", sub: tools},
		{label: "Change", sub: change},
	}
	if canOpen() {
		items = append(items, menuItem{label: "-"}, menuItem{label: "Show in folder", fn: func() { a.showInFolder(path) }})
	}
	items = append(items, menuItem{label: "-"}, menuItem{label: "Delete…", fn: func() { a.deleteArchives([]string{path}) }})
	a.openMenuItems(a.mx, a.my, items)
}

// parentItems are the archives beside g that can be its parent: the
// parents (.dosp) but those g already holds up (which would loop), its
// current one whatever its type (marked), and None.
func (a *app) parentItems(g *library.Game) []menuItem {
	dir := filepath.Dir(g.Path)
	entries, _ := os.ReadDir(dir)
	cur := ""
	if len(g.Parents) > 0 {
		cur = g.Parents[0]
	}
	path, name := g.Path, g.Name
	set := func(label string, args []string, what string) menuItem {
		return menuItem{label: label, fn: func() {
			a.confirm("Change "+name+"'s parent?", "Change", func() {
				a.runDoszer("Parent: "+name, args, func(err error) {
					if err == nil {
						a.rescan()
					}
				})
			}, "Gives "+filepath.Base(path)+" "+what+".")
		}}
	}
	none := set("None", []string{"set", "--no-parent", path}, "no parent")
	if cur == "" {
		none = menuItem{label: "• None"}
	}
	items := []menuItem{none}
	for _, e := range entries {
		n := e.Name()
		ext := strings.ToLower(filepath.Ext(n))
		if e.IsDir() || (ext != ".dosp" && n != cur) || n == filepath.Base(g.Path) {
			continue
		}
		loops := false
		for _, l := range chain.Resolve(filepath.Join(dir, n)).Links[1:] {
			if strings.EqualFold(l.Path, g.Path) {
				loops = true
			}
		}
		switch {
		case loops:
		case n == cur:
			items = append(items, menuItem{label: "• " + n})
		default:
			items = append(items, set(n, []string{"set", "--parent", n, path}, n+" as its parent"))
		}
	}
	return items
}

// bootItems are what g can boot: DOS, each shell and each OS image in
// the system folder, its current one marked. One of its own model is
// swapped in directly (doszer set); one of another model opens Edit on
// it, chosen, for the program it starts. Those the game's programs need a
// newer Windows than are greyed, as is DOS for a Windows-only game.
func (a *app) bootItems(g *library.Game) []menuItem {
	path, name := g.Path, g.Name
	need := g.Need.Game
	tooOld := func(f systemFile) bool {
		if need.Platform == platform.NotProgram || g.Need.DOS {
			return false
		}
		have := install.GuessOS(f.SystemFile)
		return have.Platform != platform.NotProgram && !install.Satisfies(have, need)
	}
	editAs := func(model int, system string) func() {
		return func() {
			a.openEditor(g)
			if e := a.edit; e != nil {
				e.model, e.changed = model, true
				switch model {
				case 1:
					e.shell = system
				case 2:
					e.os = system
				}
				a.regenerate()
			}
		}
	}
	var items []menuItem
	switch {
	case g.Kind == library.KindDOS:
		items = append(items, menuItem{label: "• DOS"})
	case (need.Platform == platform.Win16 || need.Platform == platform.Win32) && !g.Need.DOS:
		items = append(items, menuItem{label: "DOS", keys: "needs Windows", off: true})
	default:
		items = append(items, menuItem{label: "DOS", fn: editAs(0, "")})
	}
	for _, shell := range []bool{true, false} {
		kind, flag, model := library.KindShell, "--shell", 1
		if !shell {
			kind, flag, model = library.KindOS, "--os", 2
		}
		var group []menuItem
		for _, f := range a.files {
			if f.Shell != shell || f.missing {
				continue
			}
			n := f.Name
			switch {
			case g.Kind == kind && n == g.Boot:
				group = append(group, menuItem{label: "• " + n})
			case tooOld(f):
				group = append(group, menuItem{label: n, keys: "too old", off: true})
			case g.Kind == kind:
				group = append(group, menuItem{label: n, fn: func() {
					if a.saving(path) {
						a.say("Still saving "+name, false)
						return
					}
					a.runBackground("Saving "+name, path, name+" boots "+n, []string{"set", flag, n, path}, func(err error) {
						if err == nil {
							a.rescan()
						}
					})
				}})
			default:
				group = append(group, menuItem{label: n, fn: editAs(model, n)})
			}
		}
		if len(group) > 0 {
			items = append(append(items, menuItem{label: "-"}), group...)
		}
	}
	return items
}

// OpenGameMenu opens the n-th shown game's menu, as a right click on it
// would (Options.Setup).
func (a *app) OpenGameMenu(n int) {
	a.Select(n)
	x, y, _, h := a.itemRect(a.sel)
	_, ay, _, _ := a.area()
	a.mx, a.my = x+a.px(220), y+ay-a.scroll[pageGames]+h/2
	if g := a.selected(); g != nil {
		a.gameMenu(g)
	}
}

// RunDeps shows the n-th shown game's dependencies, as its menu would
// (Options.Setup); the shot waits for the command to end.
func (a *app) RunDeps(n int) {
	a.Select(n)
	if g := a.selected(); g != nil {
		a.showDeps(g.Path, g.Name)
	}
}

// showDeps runs `doszer deps` on an archive, with the window's system
// directory.
func (a *app) showDeps(path, name string) {
	args := []string{"deps", "--used-by"}
	if a.o.SystemDir != "" {
		args = append(args, "--system-dir", a.o.SystemDir)
	}
	a.runDoszer("Dependencies: "+name, append(args, path), nil)
}
