package gui

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/DemiCloud/doszer/internal/library"
)

// What the Library's lists do with several items selected: export them
// (a game with what it needs; a parent or a shell or OS image, the games
// using it), verify them, or delete them, confirmed first.

// gameOrder is the games shown, by path, in order.
func (a *app) gameOrder() []string {
	out := make([]string, len(a.view))
	for i, v := range a.view {
		out[i] = a.list[v].Path
	}
	return out
}

// curGame is the current game's path, or "".
func (a *app) curGame() string {
	if g := a.selected(); g != nil {
		return g.Path
	}
	return ""
}

// selectedGames are the games selected, in the order shown.
func (a *app) selectedGames() []string {
	return a.gameMulti.selected(a.gameOrder(), a.curGame())
}

// gamesMenu is the menu for several games.
func (a *app) gamesMenu(paths []string) {
	n := plural(len(paths), "game")
	a.openMenuItems(a.mx, a.my, []menuItem{
		{label: "Export…", fn: func() { a.openExport("Export "+n, paths) }},
		{label: "Verify", fn: func() { a.runDoszer("Verify "+n, append([]string{"verify"}, paths...), nil) }},
		{label: "-"},
		{label: "Delete…", fn: func() { a.deleteArchives(paths) }},
	})
}

// parentOrder is the parents shown, by path, in order.
func (a *app) parentOrder() []string {
	out := make([]string, len(a.parentList))
	for i, p := range a.parentList {
		out[i] = p.Path
	}
	return out
}

func (a *app) curParent() string {
	if a.parentSel >= 0 && a.parentSel < len(a.parentList) {
		return a.parentList[a.parentSel].Path
	}
	return ""
}

// children are the games whose parent one of parents is: their parent
// marker names its file, beside them.
func (a *app) children(parents []string) []string {
	var out []string
	for i := range a.list {
		g := &a.list[i]
		if len(g.Parents) == 0 {
			continue
		}
		for _, p := range parents {
			if strings.EqualFold(g.Parents[0], filepath.Base(p)) && filepath.Dir(g.Path) == filepath.Dir(p) {
				out = append(out, g.Path)
				break
			}
		}
	}
	return out
}

// exportChildren opens Export for the games using what is selected.
func (a *app) exportChildren(games []string, of string) {
	if len(games) == 0 {
		a.alert("No games use "+of, "Export takes games, with what they need.")
		return
	}
	a.openExport("Export the games using "+of, games)
}

// parentsMenu is the menu for several parents.
func (a *app) parentsMenu(paths []string) {
	a.openMenuItems(a.mx, a.my, []menuItem{
		{label: "Export games using them…", fn: func() { a.exportChildren(a.children(paths), plural(len(paths), "parent")) }},
		{label: "-"},
		{label: "Delete…", fn: func() { a.deleteArchives(paths) }},
	})
}

// sysOrder is the system files shown, by key, in order.
func (a *app) sysOrder() []string {
	out := make([]string, len(a.files))
	for i, f := range a.files {
		out[i] = key(f.Name, f.Shell)
	}
	return out
}

func (a *app) curSys() string {
	if a.sysSel >= 0 && a.sysSel < len(a.files) {
		f := a.files[a.sysSel]
		return key(f.Name, f.Shell)
	}
	return ""
}

// sysFiles are the system files with keys ks.
func (a *app) sysFiles(ks []string) []systemFile {
	var out []systemFile
	for _, f := range a.files {
		for _, k := range ks {
			if key(f.Name, f.Shell) == k {
				out = append(out, f)
			}
		}
	}
	return out
}

// booting are the games that boot one of fs.
func (a *app) booting(fs []systemFile) []string {
	var out []string
	for i := range a.list {
		g := &a.list[i]
		for _, f := range fs {
			if g.Boot == f.Name && (g.Kind == library.KindShell) == f.Shell {
				out = append(out, g.Path)
				break
			}
		}
	}
	return out
}

// systemMenu is the menu for the selected shells and OS images.
func (a *app) systemMenu(ks []string) {
	fs := a.sysFiles(ks)
	if len(fs) == 0 {
		return
	}
	of := filepath.Base(fs[0].Name)
	if len(fs) > 1 {
		of = plural(len(fs), "system")
	}
	items := []menuItem{
		{label: "Export games using it…", fn: func() { a.exportChildren(a.booting(fs), of) }},
	}
	if len(fs) > 1 {
		items[0].label = "Export games using them…"
	}
	var present []systemFile
	for _, f := range fs {
		if !f.missing {
			present = append(present, f)
		}
	}
	if len(fs) == 1 && !fs[0].missing {
		f := fs[0]
		items = append(items, menuItem{label: "Prepare…", fn: func() { a.prepare(f) }})
	}
	if canOpen() && len(fs) == 1 && !fs[0].missing {
		p := filepath.Join(a.sysDir.Path, filepath.FromSlash(fs[0].File))
		items = append(items, menuItem{label: "Show in folder", fn: func() { a.showInFolder(p) }})
	}
	if len(present) > 0 {
		items = append(items, menuItem{label: "-"}, menuItem{label: "Delete…", fn: func() { a.deleteSystems(present) }})
	}
	a.openMenuItems(a.mx, a.my, items)
}

// names lists paths' base names, at most a few, for a confirmation.
func names(paths []string) []string {
	const most = 8
	var out []string
	for i, p := range paths {
		if i == most {
			out = append(out, "and "+plural(len(paths)-most, "more"))
			break
		}
		out = append(out, "  "+filepath.Base(p))
	}
	return out
}

// deleteArchives deletes archives, once confirmed, warning about the
// games that use any of them as a parent.
func (a *app) deleteArchives(paths []string) {
	if len(paths) == 0 {
		return
	}
	what := plural(len(paths), "archive")
	lines := append(names(paths), "", "Deleted for good, not moved to the trash.")
	del := map[string]bool{}
	for _, p := range paths {
		del[p] = true
	}
	var orphans []string
	for _, c := range a.children(paths) {
		if !del[c] {
			orphans = append(orphans, c)
		}
	}
	if len(orphans) > 0 {
		lines = append(lines, plural(len(orphans), "game")+" using them won't start.")
	}
	// What goes with them, offered: their saves (progress: kept unless
	// ticked) and the source folders they were built from (ticked).
	saves := a.savesOf(paths)
	var sources []string
	for _, p := range paths {
		if s := a.sourceOf(p); s != "" {
			sources = append(sources, s)
		}
	}
	delSaves, delSources := false, true
	d := &dialog{title: "Delete " + what + "?", lines: lines}
	if len(saves) > 0 {
		var n int64
		for _, s := range saves {
			n += s.Size
		}
		whose := "their"
		if len(paths) == 1 {
			whose = "its"
		}
		d.checks = append(d.checks, dialogCheck{"Also delete " + whose + " saves (" + plural(len(saves), "file") + ", " + size(n) + ")", &delSaves})
	}
	if len(sources) > 0 {
		label := "Also delete their source folders (" + plural(len(sources), "folder") + ")"
		if len(sources) == 1 {
			label = "Also delete its source folder (" + sources[0] + ")"
		}
		d.checks = append(d.checks, dialogCheck{label, &delSources})
	}
	d.buttons = []dialogButton{{label: "Cancel"}, {label: "Delete", style: btnAccent, fn: func() {
		a.removeFiles(paths, what)
		if delSaves {
			var sp []string
			for _, s := range saves {
				sp = append(sp, s.Path)
			}
			a.removeFiles(sp, plural(len(sp), "save file"))
		}
		if delSources && len(sources) > 0 {
			a.removeAll(sources, plural(len(sources), "source folder"))
		}
		a.gameMulti.clear()
		a.parentMulti.clear()
		a.rescan()
	}}}
	a.dialog = d
}

// deleteSystems deletes shells and OS images from the system folder,
// once confirmed, warning about the games that boot them.
func (a *app) deleteSystems(fs []systemFile) {
	var paths []string
	for _, f := range fs {
		paths = append(paths, filepath.Join(a.sysDir.Path, filepath.FromSlash(f.File)))
	}
	what := plural(len(fs), "system file")
	lines := append(names(paths), "", "Deleted for good, not moved to the trash.")
	if n := len(a.booting(fs)); n > 0 {
		lines = append(lines, plural(n, "game")+" booting them won't start.")
	}
	a.confirm("Delete "+what+"?", "Delete", func() {
		a.removeFiles(paths, what)
		a.sysMulti.clear()
		a.rescanSystems()
	}, lines...)
}

// removeFiles deletes paths, saying how it went.
func (a *app) removeFiles(paths []string, what string) {
	var failed []string
	for _, p := range paths {
		if err := os.Remove(p); err != nil {
			failed = append(failed, err.Error())
		}
	}
	if len(failed) > 0 {
		a.alert("Couldn't delete everything", failed...)
		return
	}
	a.say("Deleted "+what, false)
}
