package gui

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DemiCloud/doszer/internal/config"
	"github.com/DemiCloud/doszer/internal/export"
)

// exportTypes are what Export writes, as `doszer export --to` names
// them, what its folder is and the folder picker's name for it.
var exportTypes = []struct{ label, flag, folder, pick string }{
	{"RetroDECK", "retrodeck", "RetroDECK root", "RetroDECK"},
	{"ZIP", "zip", "Folder", "ZIP's"},
}

const exportZip = 1 // index of ZIP in exportTypes

// exportLayouts are a ZIP's roms layouts: a folder per system, or flat.
var exportLayouts = []string{"Folder per system", "One folder"}

// exportSystems label export.Systems.
var exportSystems = []string{"DOS", "Windows 3.x", "Windows 9x"}

// exportDialog is Export's form: the type, its folder, the games.
type exportDialog struct {
	title     string
	files     []string // the games; nil: every game in the ROM folders
	kind      int      // index into exportTypes
	folders   []field  // a folder per type
	systems   []bool   // by export.Systems
	overwrite bool
	flat      bool // a ZIP's games all in roms/
}

// openExport opens Export for files (nil: all games), the folders the
// last export used, else the RetroDECK folder found (export.FindRetroDECK).
func (a *app) openExport(title string, files []string) {
	if files == nil && len(a.romDirs) == 0 {
		a.alert("No ROM folder", "Choose where games go first: Tools, Settings.")
		return
	}
	e := &exportDialog{title: title, files: files, folders: make([]field, len(exportTypes)), systems: make([]bool, len(export.Systems))}
	for i := range e.systems {
		e.systems[i] = true
	}
	dir := a.exportDir
	if dir == "" {
		if t, err := export.FindRetroDECK("", a.sysDir.Path); err == nil && filepath.Dir(t.ROMs) == filepath.Dir(t.BIOS) {
			dir = filepath.Dir(t.ROMs)
		}
	}
	e.folders[0].set(dir)
	e.folders[exportZip].set(a.exportZipDir)
	e.flat = a.exportFlat
	a.exp, a.focus, a.popup = e, nil, nil
}

// exportFolderOK reports whether dir holds roms and bios.
func exportFolderOK(dir string) bool {
	for _, sub := range []string{"roms", "bios"} {
		if fi, err := os.Stat(filepath.Join(dir, sub)); err != nil || !fi.IsDir() {
			return false
		}
	}
	return dir != ""
}

// folderOK reports whether the export's folder will do: a RetroDECK
// root, or any folder for a ZIP.
func (e *exportDialog) folderOK() bool {
	dir := e.folders[e.kind].text
	if e.kind == exportZip {
		fi, err := os.Stat(dir)
		return dir != "" && err == nil && fi.IsDir()
	}
	return exportFolderOK(dir)
}

// chosen are the ticked systems, as export.Systems names them.
func (e *exportDialog) chosen() []string {
	var out []string
	for i, on := range e.systems {
		if on {
			out = append(out, export.Systems[i])
		}
	}
	return out
}

func (e *exportDialog) ready() bool { return e.folderOK() && len(e.chosen()) > 0 }

func (a *app) runExport() {
	e := a.exp
	dir := e.folders[e.kind].text
	args := []string{"export", "--format", "plain", "--to", exportTypes[e.kind].flag, "--dir", dir, "--systems", strings.Join(e.chosen(), ",")}
	if e.overwrite && e.kind != exportZip {
		args = append(args, "--overwrite")
	}
	if e.flat && e.kind == exportZip {
		args = append(args, "--flat")
	}
	if e.files == nil {
		args = append(append(args, "--all"), config.DirPaths(a.romDirs)...)
	} else {
		args = append(args, e.files...)
	}
	if e.kind == exportZip {
		a.exportZipDir, a.exportFlat = dir, e.flat
	} else {
		a.exportDir = dir
	}
	a.saveState()
	a.exp, a.focus = nil, nil
	a.runDoszer(e.title, args, nil)
}

func (a *app) exportKey(sc int32, ctrl bool) {
	if a.focus != nil && sc != scEscape && sc != scReturn && a.focus.key(sc, ctrl) {
		return
	}
	switch sc {
	case scEscape:
		if a.focus != nil {
			a.focus = nil
		} else {
			a.exp = nil
		}
	case scReturn, scKPEnter:
		if a.exp.ready() {
			a.runExport()
		}
	}
}

func (a *app) drawExport() {
	e := a.exp
	if e == nil {
		return
	}
	a.layer = layerModal
	defer func() { a.layer = layerPage }()
	a.fill(0, 0, a.w, a.h, rgba{0, 0, 0, 170})
	a.onClick(0, 0, a.w, a.h, func(bool) { a.focus = nil })
	w, h := min(a.w-a.px(40), a.px(720)), a.px(374)
	x, y := (a.w-w)/2, (a.h-h)/2
	a.round(x-a.px(1), y-a.px(1), w+a.px(2), h+a.px(2), a.px(14), colLine)
	a.round(x, y, w, h, a.px(14), colPopup)
	a.onClick(x, y, w, h, func(bool) { a.focus = nil })
	p := a.px(24)
	a.text(a.ellipsize(e.title, w-2*p, 17, true), x+p, y+p, 17, true, colText)
	fx := x + p + a.px(120)
	fw := x + w - p - fx
	ry := y + p + a.px(48)
	row := func(label string) {
		a.text(label, x+p, ry+a.px(7), 13, false, colText)
	}

	row("Type")
	label := exportTypes[e.kind].label + " ▼"
	dx, dy := fx, ry // the closure keeps these; the buttons below have their own
	a.button(label, dx, dy, btnNormal, func() {
		items := make([]string, len(exportTypes))
		for i, t := range exportTypes {
			items[i] = t.label
		}
		a.openPopup(dx, dy, a.buttonW(label), items, e.kind, func(i int) {
			if i != e.kind {
				e.kind, a.focus = i, nil
			}
		})
	})
	ry += a.px(44)

	row(exportTypes[e.kind].folder)
	folder := &e.folders[e.kind]
	bw := a.buttonW("Browse…")
	a.drawField(folder, fx, ry, fw-bw-a.px(8))
	a.button("Browse…", fx+fw-bw, ry, btnNormal, func() {
		if dir, ok := pickFolder(exportTypes[e.kind].pick, folder.text); ok {
			folder.set(dir)
		}
	})
	ry += a.px(36)
	dir := folder.text
	switch {
	case dir == "":
	case e.kind == exportZip && e.folderOK():
		a.text(a.ellipsize(filepath.Base(export.ZipName(dir, time.Now())), fw, 12, false), fx, ry, 12, false, colFaint)
	case e.kind == exportZip:
		a.text("No such folder", fx, ry, 12, false, colError)
	case e.folderOK():
		a.text(a.ellipsize(filepath.Join(dir, "roms")+",  "+filepath.Join(dir, "bios"), fw, 12, false), fx, ry, 12, false, colFaint)
	default:
		a.text("No roms and bios folders here", fx, ry, 12, false, colError)
	}
	ry += a.px(30)

	row("Games")
	games := "All games"
	if e.files != nil {
		games = filepath.Base(e.files[0])
	}
	a.text(a.ellipsize(games, fw, 13, false), fx, ry+a.px(7), 13, false, colText)
	ry += a.px(44)

	row("Systems")
	cx := fx
	for i, name := range exportSystems {
		cx += a.checkbox(cx, ry, e.systems[i], name, func() { e.systems[i] = !e.systems[i] }) + a.px(24)
	}
	ry += a.px(44)

	if e.kind == exportZip { // a ZIP is a new file: no Overwrite
		row("Layout")
		layout := exportLayouts[0]
		if e.flat {
			layout = exportLayouts[1]
		}
		layout += " ▼"
		lx, ly := fx, ry
		a.button(layout, lx, ly, btnNormal, func() {
			cur := 0
			if e.flat {
				cur = 1
			}
			a.openPopup(lx, ly, a.buttonW(layout), exportLayouts, cur, func(i int) { e.flat = i == 1 })
		})
	} else {
		row("Overwrite")
		a.checkbox(fx, ry, e.overwrite, "", func() { e.overwrite = !e.overwrite })
	}

	by := y + h - p - a.px(32)
	bx := x + w - p - a.buttonW("Export")
	var run func()
	if e.ready() {
		run = a.runExport
	}
	a.button("Export", bx, by, btnAccent, run)
	bx -= a.buttonW("Cancel") + a.px(8)
	a.button("Cancel", bx, by, btnNormal, func() { a.exp, a.focus = nil, nil })
}
