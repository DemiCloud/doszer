package gui

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/DemiCloud/doszer/internal/config"
	"github.com/DemiCloud/doszer/internal/install"
	"github.com/DemiCloud/doszer/internal/library"
	"github.com/DemiCloud/doszer/internal/runlog"
)

// The Library's Sources and Saves: the folders games are built from, and
// the DOSBox Pure saves games keep, so they can be found, followed to
// their game or build, and deleted.

type storeResult struct {
	sourcesDir string
	sources    []library.Source
	saves      []library.Save
}

// storage is the Sources and Saves views' state.
type storage struct {
	sourcesDir    string           // paths.sources (or beside doszer, portable), "" for none
	sources       []library.Source // nil until scanned
	saveFiles     []library.Save
	storeCh       chan storeResult
	storeScanning bool
	srcSel        int
	saveSel       int
	saveGame      string // Saves shows this game's only (an archive path), "" every save
	srcMulti      multiSel
	saveMulti     multiSel
}

func (a *app) initStorage() {
	a.storeCh = make(chan storeResult, 1)
}

// rescanStorage reads the sources folder and the games' save folders
// again, in the background: after the games, which the saves are matched
// to.
func (a *app) rescanStorage() {
	if a.storeScanning || a.o.Screenshot != "" && a.list == nil {
		return
	}
	a.storeScanning = true
	srcDir := a.o.SourcesDir
	if srcDir == "" && config.Configured() {
		srcDir = config.SourcesDirOf(a.cfg).Path
	}
	games := append([]library.Game(nil), a.list...)
	configured := a.o.SaveDir
	if configured == "" {
		configured = a.cfg.Paths.Saves
	}
	go func() {
		r := storeResult{sourcesDir: srcDir}
		if srcDir != "" {
			r.sources, _ = library.Sources(srcDir)
		}
		dirOf := func(p string) string {
			d, _ := install.GameSaveDir(p, configured)
			return d
		}
		r.saves = library.Saves(games, dirOf, configured)
		if r.sources == nil {
			r.sources = []library.Source{}
		}
		a.storeCh <- r
	}()
}

func (a *app) storageScanned(r storeResult) {
	a.storeScanning = false
	a.sourcesDir, a.sources, a.saveFiles = r.sourcesDir, r.sources, r.saves
	a.srcSel = min(a.srcSel, len(a.sources)-1)
	a.saveSel = min(a.saveSel, len(a.shownSaves())-1)
	a.dirty = true
}

// samePath reports whether two paths name the same file, as the system
// compares them.
func samePath(x, y string) bool {
	if x == "" || y == "" {
		return false
	}
	x, y = filepath.Clean(x), filepath.Clean(y)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(x, y)
	}
	return x == y
}

// inSources reports whether p is a folder directly in the sources folder:
// the only folders doszer deletes as sources.
func (a *app) inSources(p string) bool {
	return a.sourcesDir != "" && samePath(filepath.Dir(filepath.Clean(p)), a.sourcesDir)
}

// sourceRun is the newest build-type run from source p, or nil.
func (a *app) sourceRun(p string) *runlog.Report {
	for _, r := range a.runs {
		switch r.Command {
		case "build", "make", "repack":
			if samePath(r.Source, p) {
				return r
			}
		}
	}
	return nil
}

// sourceOf is the source folder game was built from, by the logs, if it
// is still in the sources folder.
func (a *app) sourceOf(game string) string {
	for _, r := range a.runs {
		if r.Command == "build" && samePath(r.Archive, game) && a.inSources(r.Source) {
			if fi, err := os.Stat(r.Source); err == nil && fi.IsDir() {
				return r.Source
			}
		}
	}
	return ""
}

// savesOf are the save files of games.
func (a *app) savesOf(games []string) []library.Save {
	var out []library.Save
	for _, s := range a.saveFiles {
		for _, g := range games {
			if samePath(s.Game, g) {
				out = append(out, s)
				break
			}
		}
	}
	return out
}

// shownSaves are the saves the Saves view shows.
func (a *app) shownSaves() []library.Save {
	if a.saveGame == "" {
		return a.saveFiles
	}
	return a.savesOf([]string{a.saveGame})
}

// showSaves shows a game's saves.
func (a *app) showSaves(game string) {
	a.show(pageSaves)
	a.saveGame, a.saveSel = game, 0
	a.saveMulti.clear()
}

// showSource shows the source folder p, selected.
func (a *app) showSource(p string) {
	a.show(pageSources)
	for i, s := range a.sources {
		if samePath(s.Path, p) {
			a.srcSel = i
			a.srcMulti.clear()
		}
	}
}

// --- Sources ---

func (a *app) sourceOrder() []string {
	out := make([]string, len(a.sources))
	for i, s := range a.sources {
		out[i] = s.Path
	}
	return out
}

func (a *app) curSource() string {
	if a.srcSel >= 0 && a.srcSel < len(a.sources) {
		return a.sources[a.srcSel].Path
	}
	return ""
}

func (a *app) storageRowH() int32 { return a.px(72) }

func (a *app) sourcesH() int32 {
	return a.pad() + int32(len(a.sources))*(a.storageRowH()+a.rowGap()) + a.pad()
}

func (a *app) sourcesToolbar(x, y int32) {
	if a.sourcesDir != "" && canOpen() {
		dir := a.sourcesDir
		x += a.button("Open folder", x, y, btnNormal, func() { os.MkdirAll(dir, 0o755); a.open(dir) }) + a.px(4)
	}
	a.button("Refresh", x, y, btnNormal, a.rescanStorage)
}

func (a *app) sourcesStatus() string {
	if a.sourcesDir == "" {
		return "No sources folder"
	}
	var n int64
	for _, s := range a.sources {
		n += s.Size
	}
	return plural(len(a.sources), "source") + ", " + size(n) + "  |  " + a.sourcesDir
}

func (a *app) drawSources() {
	switch {
	case !config.Configured() && a.o.SourcesDir == "":
		a.emptyMessage("No sources", "doszer isn't set up: sources go in a temporary folder, deleted after the build.")
		return
	case a.sources == nil:
		a.emptyMessage("Scanning", a.sourcesDir)
		return
	case len(a.sources) == 0:
		a.emptyMessage("No sources", "Games dumped, imported or installed are kept in "+a.sourcesDir+" until deleted here.")
		return
	}
	ax, ay, aw, ah := a.area()
	p := a.pad()
	rh := a.storageRowH()
	for i := range a.sources {
		s := &a.sources[i]
		x, y, w := ax+p, ay+p+int32(i)*(rh+a.rowGap())-a.scroll[pageSources], aw-2*p
		if y+rh < ay || y > ay+ah {
			continue
		}
		sel := i == a.srcSel
		if a.srcMulti.count() > 1 {
			sel = a.srcMulti.has(s.Path)
		}
		a.card(x, y, w, rh, sel, a.hovered(x, y, w, rh))
		path, idx := s.Path, i
		a.dragItem(&a.srcMulti, a.sourceOrder, path, x, y, w, rh, nil)
		a.clipHit(x, y, w, rh, func(bool) {
			a.srcMulti.click(a.sourceOrder(), path, a.curSource(), a.rightClicking)
			a.srcSel = idx
			a.menuAt = func() { a.sourcesMenu(a.srcMulti.selected(a.sourceOrder(), a.curSource())) }
		})
		is := a.px(44)
		a.initialTile(s.Name, colMuted, x+a.px(14), y+(rh-is)/2, is)
		tx := x + a.px(14) + is + a.px(14)
		right := a.px(220)
		tw := w - (tx - x) - right
		a.text(a.ellipsize(s.Name, tw, 15, true), tx, y+a.px(12), 15, true, colText)
		by := y + a.px(12) + a.lineHeight(15, true) + a.px(6)
		label, c := "Not built", colShell
		if r := a.sourceRun(s.Path); r != nil {
			switch {
			case r.Result == runlog.ResultFailed:
				label, c = "Last build failed", colError
			case r.Archive != "":
				label, c = "Built "+filepath.Base(r.Archive), colDOS
				if _, err := os.Stat(r.Archive); err != nil {
					label, c = "Built "+filepath.Base(r.Archive)+", since removed", colShell
				}
			}
		}
		bx := tx + a.badge(a.ellipsize(label, tw/2, 12, false), c, tx, by) + a.px(8)
		a.text(a.ellipsize(filepath.Base(s.Path), tx+tw-bx, 12, false), bx, by+a.px(3), 12, false, colMuted)
		rx := x + w - a.px(16)
		a.textRight(size(s.Size), rx, y+a.px(14), 13, false, colText)
		a.textRight(plural(s.Files, "file"), rx, y+a.px(36), 12, false, colMuted)
	}
}

// sourcesMenu is the selected source folders' menu.
func (a *app) sourcesMenu(paths []string) {
	if len(paths) == 0 {
		return
	}
	var items []menuItem
	if len(paths) == 1 {
		p := paths[0]
		if canOpen() {
			items = append(items, menuItem{label: "Open folder", fn: func() { a.open(p) }})
		}
		if r := a.sourceRun(p); r != nil {
			id := r.ID
			items = append(items, menuItem{label: "Build log", fn: func() { a.showRun(id) }})
			if g := a.find(r.Archive); g != nil {
				arch := r.Archive
				items = append(items, menuItem{label: "Show the game", fn: func() { a.showGame(arch) }})
			}
		}
		items = append(items, menuItem{label: "-"})
	}
	items = append(items, menuItem{label: "Delete…", fn: func() { a.deleteSources(paths) }})
	a.openMenuItems(a.mx, a.my, items)
}

// deleteSources deletes source folders, once confirmed: the archives
// built from them stay.
func (a *app) deleteSources(paths []string) {
	var ok []string
	var total int64
	for _, p := range paths {
		if !a.inSources(p) {
			continue
		}
		ok = append(ok, p)
		for _, s := range a.sources {
			if samePath(s.Path, p) {
				total += s.Size
			}
		}
	}
	if len(ok) == 0 {
		return
	}
	what := plural(len(ok), "source folder")
	lines := append(names(ok), "", size(total)+", deleted for good. The archives built from them stay.")
	a.confirm("Delete "+what+"?", "Delete", func() {
		a.removeAll(ok, what)
		a.srcMulti.clear()
		a.rescanStorage()
	}, lines...)
}

// removeAll deletes folders, saying how it went; only folders in the
// sources folder.
func (a *app) removeAll(paths []string, what string) {
	var failed []string
	for _, p := range paths {
		if !a.inSources(p) {
			failed = append(failed, p+": not in "+a.sourcesDir)
			continue
		}
		if err := os.RemoveAll(p); err != nil {
			failed = append(failed, err.Error())
		}
	}
	if len(failed) > 0 {
		a.alert("Couldn't delete everything", failed...)
		return
	}
	a.say("Deleted "+what, false)
}

// --- Saves ---

func (a *app) saveOrder() []string {
	ss := a.shownSaves()
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = s.Path
	}
	return out
}

func (a *app) curSave() string {
	if ss := a.shownSaves(); a.saveSel >= 0 && a.saveSel < len(ss) {
		return ss[a.saveSel].Path
	}
	return ""
}

func (a *app) savesH() int32 {
	return a.pad() + int32(len(a.shownSaves()))*(a.storageRowH()+a.rowGap()) + a.pad()
}

func (a *app) savesToolbar(x, y int32) {
	if a.saveGame != "" {
		x += a.button("All saves", x, y, btnNormal, func() { a.saveGame, a.saveSel = "", 0; a.saveMulti.clear() }) + a.px(4)
	}
	a.button("Refresh", x, y, btnNormal, a.rescanStorage)
}

func (a *app) savesStatus() string {
	ss := a.shownSaves()
	var n int64
	orphans := 0
	for _, s := range ss {
		n += s.Size
		if s.Game == "" {
			orphans++
		}
	}
	st := plural(len(ss), "save file") + ", " + size(n)
	if orphans > 0 {
		st += ", " + itoa(orphans) + " without their game"
	}
	if a.saveGame != "" {
		st += "  |  " + a.gameName(a.saveGame)
	}
	return st
}

// gameName is the game at path's name, else its file name.
func (a *app) gameName(path string) string {
	if g := a.find(path); g != nil {
		return g.Name
	}
	return strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
}

func (a *app) drawSaves() {
	ss := a.shownSaves()
	switch {
	case a.sources == nil && a.storeScanning:
		a.emptyMessage("Scanning")
		return
	case len(ss) == 0 && a.saveGame != "":
		a.emptyMessage("No saves", a.gameName(a.saveGame)+" has no save files yet.")
		return
	case len(ss) == 0:
		a.emptyMessage("No saves", "Games keep their DOSBox Pure saves here once played.")
		return
	}
	ax, ay, aw, ah := a.area()
	p := a.pad()
	rh := a.storageRowH()
	for i := range ss {
		s := ss[i]
		x, y, w := ax+p, ay+p+int32(i)*(rh+a.rowGap())-a.scroll[pageSaves], aw-2*p
		if y+rh < ay || y > ay+ah {
			continue
		}
		sel := i == a.saveSel
		if a.saveMulti.count() > 1 {
			sel = a.saveMulti.has(s.Path)
		}
		a.card(x, y, w, rh, sel, a.hovered(x, y, w, rh))
		path, idx := s.Path, i
		a.dragItem(&a.saveMulti, a.saveOrder, path, x, y, w, rh, nil)
		a.clipHit(x, y, w, rh, func(bool) {
			a.saveMulti.click(a.saveOrder(), path, a.curSave(), a.rightClicking)
			a.saveSel = idx
			a.menuAt = func() { a.savesMenu(a.saveMulti.selected(a.saveOrder(), a.curSave())) }
		})
		is := a.px(44)
		name, c := s.Content, colShell
		if s.Game != "" {
			name, c = a.gameName(s.Game), colDOS
		}
		if g := a.find(s.Game); g != nil {
			a.drawIcon(g, x+a.px(14), y+(rh-is)/2, is)
		} else {
			a.initialTile(name, c, x+a.px(14), y+(rh-is)/2, is)
		}
		tx := x + a.px(14) + is + a.px(14)
		right := a.px(220)
		tw := w - (tx - x) - right
		a.text(a.ellipsize(name, tw, 15, true), tx, y+a.px(12), 15, true, colText)
		by := y + a.px(12) + a.lineHeight(15, true) + a.px(6)
		bx := tx + a.badge(s.Kind, colMuted, tx, by) + a.px(8)
		if s.Game == "" {
			bx += a.badge("no game", colShell, bx, by) + a.px(8)
		}
		a.text(a.ellipsize(filepath.Base(s.Path), tx+tw-bx, 12, false), bx, by+a.px(3), 12, false, colMuted)
		rx := x + w - a.px(16)
		a.textRight(size(s.Size), rx, y+a.px(14), 13, false, colText)
		a.textRight(s.Modified.Local().Format("Jan 2, 2006 3:04 PM"), rx, y+a.px(36), 12, false, colMuted)
	}
}

// savesMenu is the selected save files' menu.
func (a *app) savesMenu(paths []string) {
	if len(paths) == 0 {
		return
	}
	var items []menuItem
	if len(paths) == 1 {
		p := paths[0]
		for _, s := range a.saveFiles {
			if s.Path == p && s.Game != "" {
				g := s.Game
				items = append(items, menuItem{label: "Show the game", fn: func() { a.showGame(g) }})
			}
		}
		if canOpen() {
			items = append(items, menuItem{label: "Show in folder", fn: func() { a.showInFolder(p) }})
		}
		items = append(items, menuItem{label: "-"})
	}
	items = append(items, menuItem{label: "Delete…", fn: func() { a.deleteSaves(paths) }})
	a.openMenuItems(a.mx, a.my, items)
}

// deleteSaves deletes save files, once confirmed: the games' progress.
func (a *app) deleteSaves(paths []string) {
	what := plural(len(paths), "save file")
	var total int64
	for _, s := range a.saveFiles {
		for _, p := range paths {
			if s.Path == p {
				total += s.Size
			}
		}
	}
	lines := append(names(paths), "", size(total)+": the games' progress, deleted for good.")
	a.confirm("Delete "+what+"?", "Delete", func() {
		a.removeFiles(paths, what)
		a.saveMulti.clear()
		a.rescanStorage()
	}, lines...)
}
