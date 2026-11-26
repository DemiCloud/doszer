package gui

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/DemiCloud/doszer/internal/chain"
	"github.com/DemiCloud/doszer/internal/library"
)

// gameMenu opens a game's right-click menu at the mouse. Each action is a
// doszer command (runDoszer), shown with its command line.
func (a *app) gameMenu(g *library.Game) {
	type item struct {
		label string
		fn    func()
	}
	path, name := g.Path, g.Name
	rescan := func(err error) {
		if err == nil {
			a.rescan()
		}
	}
	items := []item{
		{"Play", a.play},
		{"-", nil},
		{"Info", func() { a.runDoszer("Info: "+name, []string{"info", "--format", "plain", path}, nil) }},
		{"Dependencies", func() { a.showDeps(path, name) }},
		{"Verify", func() { a.runDoszer("Verify: "+name, []string{"verify", "--format", "plain", path}, nil) }},
		{"-", nil},
		{"Repack…", func() {
			a.confirm("Repack "+name+"?", "Repack", func() {
				a.runDoszer("Repack: "+name, []string{"repack", "--format", "plain", path, path}, rescan)
			}, "Rebuilds the archive from its embedded manifest, in place.", "Its saves are named after the file, so they stay with it.")
		}},
		{"Change parent…", func() { a.pickParent(g) }},
	}
	switch g.Kind {
	case library.KindOS:
		items = append(items, item{"Change OS image…", func() { a.pickSystem(g, false) }})
	case library.KindShell:
		items = append(items, item{"Change shell…", func() { a.pickSystem(g, true) }})
	}
	items = append(items, item{"-", nil}, item{"Show in folder", func() { a.showInFolder(path) }})
	if !canOpen() {
		items = items[:len(items)-2]
	}
	labels := make([]string, len(items))
	for i, it := range items {
		labels[i] = it.label
	}
	a.openMenu(a.mx, a.my, labels, func(i int) {
		if items[i].fn != nil {
			items[i].fn()
		}
	})
}

// pickParent offers the archives beside g as its parent: any but g and
// those g already holds up (which would loop).
func (a *app) pickParent(g *library.Game) {
	dir := filepath.Dir(g.Path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		a.say(err.Error(), true)
		return
	}
	cur := ""
	if len(g.Parents) > 0 {
		cur = g.Parents[0]
	}
	items, current := []string{"None"}, 0
	for _, e := range entries {
		n := e.Name()
		ext := strings.ToLower(filepath.Ext(n))
		if e.IsDir() || (ext != ".dosz" && ext != ".shell" && ext != ".zip") || n == filepath.Base(g.Path) {
			continue
		}
		loops := false
		for _, l := range chain.Resolve(filepath.Join(dir, n)).Links[1:] {
			if strings.EqualFold(l.Path, g.Path) {
				loops = true
			}
		}
		if loops {
			continue
		}
		items = append(items, n)
		if n == cur {
			current = len(items) - 1
		}
	}
	path, name := g.Path, g.Name
	a.openPopup(a.mx, a.my-a.px(36), a.px(260), items, current, func(i int) {
		if i == current {
			return
		}
		args := []string{"set", "--format", "plain", "--no-parent", path}
		what := "no parent"
		if i > 0 {
			args = []string{"set", "--format", "plain", "--parent", items[i], path}
			what = items[i] + " as its parent"
		}
		a.confirm("Change "+name+"'s parent?", "Change", func() {
			a.runDoszer("Parent: "+name, args, func(err error) {
				if err == nil {
					a.rescan()
				}
			})
		}, "Gives "+filepath.Base(path)+" "+what+", rewriting it in place.")
	})
}

// pickSystem offers the system directory's shells or OS images to boot g.
func (a *app) pickSystem(g *library.Game, shell bool) {
	items, current := []string{}, -1
	for _, f := range a.files {
		if f.Shell == shell && !f.missing {
			items = append(items, f.Name)
			if f.Name == g.Boot {
				current = len(items) - 1
			}
		}
	}
	what, flag := "OS image", "--os"
	if shell {
		what, flag = "shell", "--shell"
	}
	if len(items) == 0 {
		a.alert("No "+what+" to choose", "The system folder has none: "+a.sysDir.Path)
		return
	}
	path, name := g.Path, g.Name
	a.openPopup(a.mx, a.my-a.px(36), a.px(260), items, current, func(i int) {
		if i == current {
			return
		}
		args := []string{"set", "--format", "plain", flag, items[i], path}
		a.confirm("Change "+name+"'s "+what+"?", "Change", func() {
			a.runDoszer(strings.ToUpper(what[:1])+what[1:]+": "+name, args, func(err error) {
				if err == nil {
					a.rescan()
				}
			})
		}, "Boots "+items[i]+" instead of "+g.Boot+", rewriting "+filepath.Base(path)+" in place.")
	})
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
	args := []string{"deps", "--format", "plain", "--used-by"}
	if a.o.SystemDir != "" {
		args = append(args, "--system-dir", a.o.SystemDir)
	}
	a.runDoszer("Dependencies: "+name, append(args, path), nil)
}
