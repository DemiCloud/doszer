package gui

import (
	"path/filepath"
	"strings"

	"github.com/DemiCloud/doszer/internal/library"
)

// The Parents page: the .dosp archives (spec §5.3), files games share,
// with what each boots and which games use it.

// users are the games whose parent p is (their parent marker names its
// file, beside them).
func (a *app) users(p *library.Game) []string {
	var out []string
	for i := range a.list {
		g := &a.list[i]
		if len(g.Parents) > 0 && strings.EqualFold(g.Parents[0], filepath.Base(p.Path)) && filepath.Dir(g.Path) == filepath.Dir(p.Path) {
			out = append(out, g.Name)
		}
	}
	return out
}

func (a *app) parentsH() int32 {
	return a.pad() + int32(len(a.parentList))*(a.rowH()+a.rowGap()) + a.pad()
}

func (a *app) parentsKey(sc int32) {
	switch sc {
	case scUp:
		a.parentSel = max(0, a.parentSel-1)
	case scDown:
		a.parentSel = min(len(a.parentList)-1, a.parentSel+1)
	}
}

func (a *app) parentsToolbar(x, y int32) {
	a.button("Refresh", x, y, btnNormal, a.rescan)
}

func (a *app) drawParents() {
	if len(a.parentList) == 0 {
		a.emptyMessage("No parents", "Archives games share (.dosp) show here. A game's Tools > Convert to parent makes one.")
		return
	}
	ax, ay, aw, _ := a.area()
	p := a.pad()
	for i := range a.parentList {
		g := &a.parentList[i]
		x, y, w, h := ax+p, ay+p+int32(i)*(a.rowH()+a.rowGap())-a.scroll[pageParents], aw-2*p, a.rowH()
		a.card(x, y, w, h, i == a.parentSel, a.hovered(x, y, w, h))
		idx := i
		a.clipHit(x, y, w, h, func(bool) {
			a.parentSel = idx
			a.menuAt = func() { a.parentMenu(g) }
		})
		is := a.px(64)
		a.drawIcon(g, x+a.px(12), y+(h-is)/2, is)
		tx := x + a.px(12) + is + a.px(16)
		right := a.px(300)
		tw := w - (tx - x) - right
		a.text(a.ellipsize(g.Name, tw, 16, true), tx, y+a.px(12), 16, true, colText)
		a.text(a.ellipsize(g.Rel, tw, 13, false), tx, y+a.px(34), 13, false, colMuted)
		label, c := kindLabel(g)
		if g.Boot == "" {
			label, c = "Files only", colMuted
		}
		a.badge(label, c, tx, y+a.px(56))

		sx := x + w - right
		a.fill(sx, y+a.px(14), a.px(1), h-a.px(28), colLine)
		users := a.users(g)
		used := "Used by no games"
		switch len(users) {
		case 0:
		case 1:
			used = "Used by " + users[0]
		default:
			used = "Used by " + plural(len(users), "game")
		}
		rx := x + w - a.px(16)
		a.textRight(a.ellipsize(used, right-a.px(30), 13, false), rx, y+a.px(14), 13, false, colText)
		if len(users) > 1 {
			a.textRight(a.ellipsize(strings.Join(users, ", "), right-a.px(30), 12, false), rx, y+a.px(36), 12, false, colFaint)
		}
		a.textRight(size(g.Size), rx, y+a.px(58), 13, false, colMuted)
	}
}

// parentMenu is a parent's right-click menu.
func (a *app) parentMenu(g *library.Game) {
	path, name := g.Path, g.Name
	items := []menuItem{
		{label: "Dependencies", fn: func() { a.showDeps(path, name) }},
		{label: "Verify", fn: func() { a.runDoszer("Verify: "+name, []string{"verify", "--format", "plain", path}, nil) }},
		{label: "Lint", fn: func() { a.lint(path, name) }},
		{label: "-"},
		{label: "Create child…", fn: func() { a.openChild(path, name) }},
		{label: "Convert to game…", fn: func() { a.convert(path, name, "dosz") }},
	}
	if canOpen() {
		items = append(items, menuItem{label: "-"}, menuItem{label: "Show in folder", fn: func() { a.showInFolder(path) }})
	}
	a.openMenuItems(a.mx, a.my, items)
}

// convert runs `doszer convert` on an archive: to a parent (dosp) or a
// game (dosz), re-pointing what uses it.
func (a *app) convert(path, name, to string) {
	what := "a parent (.dosp): files games share, not listed as a game"
	if to == "dosz" {
		what = "a game (.dosz)"
	}
	a.confirm("Convert "+name+"?", "Convert", func() {
		a.runDoszer("Convert: "+name, []string{"convert", "--format", "plain", "--to", to, path}, func(err error) {
			if err == nil {
				a.rescan()
			}
		})
	}, "Makes it "+what+", and re-points the archives that use it.")
}
