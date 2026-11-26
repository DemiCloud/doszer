package gui

import (
	"os"
	"path/filepath"

	"github.com/DemiCloud/doszer/internal/config"
	"github.com/DemiCloud/doszer/internal/export"
)

// exportTypes are the frontends Export copies into, as `doszer export
// --to` names them.
var exportTypes = []struct{ label, flag string }{
	{"RetroDECK", "retrodeck"},
}

// exportDialog is Export's form: the type, the folder, the games.
type exportDialog struct {
	title     string
	files     []string // the games; nil: every game in the ROM folders
	kind      int      // index into exportTypes
	folder    field
	overwrite bool
}

// openExport opens Export for files (nil: all games), the folder the
// last export used, else the RetroDECK folder found (export.FindRetroDECK).
func (a *app) openExport(title string, files []string) {
	if files == nil && len(a.romDirs) == 0 {
		a.alert("No ROM folder", "Choose where games go first: Tools, Settings.")
		return
	}
	e := &exportDialog{title: title, files: files}
	dir := a.exportDir
	if dir == "" {
		if t, err := export.FindRetroDECK("", a.sysDir.Path); err == nil && filepath.Dir(t.ROMs) == filepath.Dir(t.BIOS) {
			dir = filepath.Dir(t.ROMs)
		}
	}
	e.folder.set(dir)
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

func (a *app) runExport() {
	e := a.exp
	dir := e.folder.text
	args := []string{"export", "--format", "plain", "--to", exportTypes[e.kind].flag, "--dir", dir}
	if e.overwrite {
		args = append(args, "--overwrite")
	}
	if e.files == nil {
		args = append(append(args, "--all"), config.DirPaths(a.romDirs)...)
	} else {
		args = append(args, e.files...)
	}
	a.exportDir = dir
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
		if exportFolderOK(a.exp.folder.text) {
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
	w, h := min(a.w-a.px(40), a.px(720)), a.px(330)
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
	bx, by := fx, ry
	a.button(label, bx, by, btnNormal, func() {
		items := make([]string, len(exportTypes))
		for i, t := range exportTypes {
			items[i] = t.label
		}
		a.openPopup(bx, by, a.buttonW(label), items, e.kind, func(i int) { e.kind = i })
	})
	ry += a.px(44)

	row("Folder")
	bw := a.buttonW("Browse…")
	a.drawField(&e.folder, fx, ry, fw-bw-a.px(8))
	a.button("Browse…", fx+fw-bw, ry, btnNormal, func() {
		if dir, ok := pickFolder("RetroDECK's folder", e.folder.text); ok {
			e.folder.set(dir)
		}
	})
	ry += a.px(36)
	ok := exportFolderOK(e.folder.text)
	switch {
	case e.folder.text == "":
		a.text("Holding roms and bios", fx, ry, 12, false, colFaint)
	case ok:
		a.text(a.ellipsize(filepath.Join(e.folder.text, "roms")+",  "+filepath.Join(e.folder.text, "bios"), fw, 12, false), fx, ry, 12, false, colFaint)
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

	row("Overwrite")
	a.checkbox(fx, ry, e.overwrite, "", func() { e.overwrite = !e.overwrite })

	by = y + h - p - a.px(32)
	bx = x + w - p - a.buttonW("Export")
	var run func()
	if ok {
		run = a.runExport
	}
	a.button("Export", bx, by, btnAccent, run)
	bx -= a.buttonW("Cancel") + a.px(8)
	a.button("Cancel", bx, by, btnNormal, func() { a.exp, a.focus = nil, nil })
}
