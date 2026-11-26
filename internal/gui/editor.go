package gui

import (
	"archive/zip"
	"bytes"
	"errors"
	"image"
	"image/draw"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/DemiCloud/doszer/internal/chain"
	"github.com/DemiCloud/doszer/internal/icon"
	"github.com/DemiCloud/doszer/internal/library"
	"github.com/DemiCloud/doszer/internal/manifest"
	"github.com/DemiCloud/doszer/internal/platform"
	"github.com/DemiCloud/doszer/internal/repack"
	"github.com/DemiCloud/doszer/internal/sdl"
	"github.com/DemiCloud/doszer/internal/start"
)

// editDialog edits an archive's settings - its embedded manifest and its
// WINDOWS.BAT - then rebuilds it from them (doszer repack --manifest).
// Settings is a form over the manifest; WINDOWS.BAT and DOSZ.TOML are the
// files themselves. The generated runtime files (AUTOBOOT.DBP,
// PADMAP.DBP, DOS.YML: spec §6.8) come from the manifest's [boot] and
// [[input.pad]], so they're changed through it: an edit to them directly
// would be undone by the next build.
type editDialog struct {
	path, name string
	m          *manifest.Manifest
	mode       int // editSettings, editBat, editToml
	bat, toml  *textArea
	err        string
	changed    bool // the form changed

	// The form.
	fname     field
	model     int    // 0 DOS, 1 Windows 3.x, 2 Windows 9x
	exec      string // DOS: boot.exec
	shell, os string
	parent    string
	program   string // Windows: what WINDOWS.BAT starts (a path in the archive)
	colors256 bool
	dosProgs  []string // the archive's DOS programs and batch files, as DOS paths
	winProgs  []string // its Windows programs, as paths
	files     map[string]bool
	parents   []string

	// The icon: an archive path, or a file to add as one.
	iconPath    string
	iconAdd     string // a file on disk, added as iconPath (repack --add)
	iconChanged bool
	iconCands   []string // the archive's ICO files and programs with icons
	iconData    []byte   // the icon shown
	iconTex     *texture
}

// Edit modes: the settings form (Edit), or a file's text (Tools >
// Advanced).
const (
	editSettings = iota
	editBat
	editToml
)

// openEditor opens the settings form on a game's archive.
func (a *app) openEditor(g *library.Game) { a.openEditorMode(g, editSettings) }

// openEditorMode opens the editor on a game's archive, as the settings
// form or one file's text.
func (a *app) openEditorMode(g *library.Game, mode int) {
	data, bat, err := repack.EmbeddedManifest(g.Path)
	if errors.Is(err, repack.ErrNoManifest) {
		// Not built by doszer: a manifest to retrofit (repack --manifest).
		data, err = toml.Marshal(map[string]any{"format": 1, "name": g.Name})
	}
	if err != nil {
		a.alert("Can't read "+filepath.Base(g.Path), err.Error())
		return
	}
	m, err := manifest.Parse(data, g.Name)
	if err != nil {
		a.alert("Its manifest doesn't parse", err.Error())
		return
	}
	for _, f := range m.File {
		if strings.EqualFold(f.Path, "WINDOWS.BAT") && f.Text != nil {
			bat = []byte(*f.Text)
		}
	}
	e := &editDialog{path: g.Path, name: g.Name, m: m, mode: mode, bat: newTextArea(string(bat)), toml: newTextArea(string(data)), files: map[string]bool{}}
	if m.Icon != nil {
		e.iconPath = m.Icon.Path
	}
	e.scan()
	e.fromManifest()
	// The parents it could have: archives beside it that wouldn't loop.
	dir := filepath.Dir(g.Path)
	entries, _ := os.ReadDir(dir)
	for _, ent := range entries {
		n := ent.Name()
		ext := strings.ToLower(filepath.Ext(n))
		if ent.IsDir() || (ext != ".dosz" && ext != ".shell" && ext != ".zip") || n == filepath.Base(g.Path) {
			continue
		}
		loops := false
		for _, l := range chain.Resolve(filepath.Join(dir, n)).Links[1:] {
			loops = loops || strings.EqualFold(l.Path, g.Path)
		}
		if !loops {
			e.parents = append(e.parents, n)
		}
	}
	a.edit, a.focus, a.focusArea, a.popup = e, nil, nil, nil
	switch mode {
	case editSettings:
		a.focus = &e.fname
	case editBat:
		a.focusArea = e.bat
	case editToml:
		a.focusArea = e.toml
	}
}

// scan lists the archive's programs, for the form's choices.
func (e *editDialog) scan() {
	zr, err := zip.OpenReader(e.path)
	if err != nil {
		return
	}
	defer zr.Close()
	for _, f := range zr.File {
		e.files[strings.ToUpper(f.Name)] = true
		up := strings.ToUpper(f.Name)
		switch {
		case up == "DOSZ.ICO":
			e.iconData, _ = readZip(f)
			continue
		case strings.HasSuffix(up, ".ICO"):
			e.iconCands = append(e.iconCands, f.Name)
			continue
		}
		if strings.HasSuffix(strings.ToUpper(f.Name), ".BAT") && !strings.EqualFold(f.Name, "WINDOWS.BAT") {
			if d, ok := platform.DOSPath(f.Name); ok {
				e.dosProgs = append(e.dosProgs, d)
			}
			continue
		}
		if !platform.IsProgramName(f.Name) || platform.IsHelper(f.Name) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		t := platform.Classify(f.Name, rc)
		rc.Close()
		switch t.Platform {
		case platform.DOS:
			if d, ok := platform.DOSPath(f.Name); ok {
				e.dosProgs = append(e.dosProgs, d)
			}
		case platform.Win16, platform.Win32:
			e.winProgs = append(e.winProgs, f.Name)
			if data, err := readZip(f); err == nil {
				if _, err := icon.Make(bytes.NewReader(data), int64(len(data)), 0); err == nil {
					e.iconCands = append(e.iconCands, f.Name)
				}
			}
		}
	}
	sort.Strings(e.dosProgs)
	sort.Strings(e.winProgs)
	sort.Strings(e.iconCands)
}

// readZip reads an archive entry of at most 32 MiB.
func readZip(f *zip.File) ([]byte, error) {
	if f.UncompressedSize64 > 32<<20 {
		return nil, errors.New("too large")
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// setIcon shows data's icon (DOSZ.ICO as build makes it).
func (a *app) setIcon(e *editDialog, data []byte) {
	ico, err := icon.Make(bytes.NewReader(data), int64(len(data)), 0)
	if err != nil {
		e.err = err.Error()
		return
	}
	e.iconData, e.err = ico, ""
	if e.iconTex != nil {
		sdl.DestroyTexture(e.iconTex.t)
		e.iconTex = nil
	}
}

// chooseIconFromArchive uses an archive file's icon.
func (a *app) chooseIconFromArchive(e *editDialog, p string) {
	zr, err := zip.OpenReader(e.path)
	if err != nil {
		e.err = err.Error()
		return
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name == p {
			if data, err := readZip(f); err == nil {
				a.setIcon(e, data)
				if e.err == "" {
					e.iconPath, e.iconAdd, e.iconChanged = p, "", true
				}
			}
			return
		}
	}
}

// chooseIconFile adds a file (an ICO, or a program to take it from) to
// the archive as its icon.
func (a *app) chooseIconFile(e *editDialog) {
	p, ok := pickFile("Choose an icon", fileIcon)
	if !ok {
		return
	}
	data, err := os.ReadFile(p)
	if err != nil {
		e.err = err.Error()
		return
	}
	a.setIcon(e, data)
	if e.err == "" {
		e.iconPath, e.iconAdd, e.iconChanged = filepath.Base(p), p, true
	}
}

// fromManifest fills the form from e.m and the WINDOWS.BAT text.
func (e *editDialog) fromManifest() {
	m := e.m
	e.fname.set(m.Name)
	e.exec, e.shell, e.os, e.parent = m.Boot.Exec, m.Boot.Shell, m.Boot.OS, m.Boot.Parent
	switch m.Model() {
	case manifest.ModelShell:
		e.model = 1
	case manifest.ModelOS:
		e.model = 2
	default:
		e.model = 0
	}
	p, d := start.ParseBatch(e.bat.text())
	e.program, e.colors256 = p, d == "640x480x8"
	e.changed = false
}

// toManifest writes the form into e.m.
func (e *editDialog) toManifest() {
	m := e.m
	m.Name = strings.TrimSpace(e.fname.text)
	m.Boot.Exec, m.Boot.Shell, m.Boot.OS = "", "", ""
	switch e.model {
	case 0:
		m.Boot.Exec = e.exec
	case 1:
		m.Boot.Shell = e.shell
	case 2:
		m.Boot.OS = e.os
	}
	m.Boot.Parent = e.parent
}

// regenerate writes WINDOWS.BAT for the form's program and colours, as
// `doszer start` would (start.Batch).
func (a *app) regenerate() {
	e := a.edit
	if e.program == "" || e.model == 0 {
		return
	}
	model := manifest.ModelShell
	display := ""
	if e.model == 2 {
		model = manifest.ModelOS
		if e.colors256 {
			display = "640x480x8"
		}
	}
	reg := start.FindRegistry(e.program, func(p string) bool { return e.files[strings.ToUpper(p)] })
	text, err := start.Batch(model, e.program, reg, display)
	if err != nil {
		e.err = err.Error()
		return
	}
	e.bat.set(text)
	e.bat.changed = true
	e.err = ""
}

func (a *app) editKey(sc int32, ctrl bool) {
	switch {
	case sc == scEscape:
		a.closeEditor()
	case ctrl && sc == scS:
		a.saveEditor()
	case a.focusArea != nil:
		a.focusArea.key(sc, ctrl, 20)
	case a.focus != nil:
		a.focus.key(sc, ctrl)
	}
}

func (a *app) editChanged() bool {
	e := a.edit
	return e.changed || e.iconChanged || e.bat.changed || e.toml.changed || strings.TrimSpace(e.fname.text) != e.m.Name
}

func (a *app) closeEditor() {
	done := func() {
		if t := a.edit.iconTex; t != nil {
			sdl.DestroyTexture(t.t)
		}
		a.edit, a.focusArea, a.focus = nil, nil, nil
	}
	if a.editChanged() {
		a.confirm("Discard your changes?", "Discard", done, "")
		return
	}
	done()
}

// saveEditor checks the manifest and repacks the archive with it.
func (a *app) saveEditor() {
	e := a.edit
	switch e.mode {
	case editToml:
		m, err := manifest.Parse([]byte(e.toml.text()), e.name)
		if err != nil {
			e.err = strings.ReplaceAll(err.Error(), "\n", "; ")
			return
		}
		e.m = m
	case editSettings:
		e.toManifest()
		if e.iconChanged {
			e.m.Icon = nil
			if e.iconPath != "" {
				e.m.Icon = &manifest.Icon{Path: e.iconPath}
			}
		}
	}
	m := e.m
	// WINDOWS.BAT goes in the manifest, so every later build keeps it.
	if e.bat.changed {
		text := e.bat.text()
		var files []manifest.FileSpec
		for _, f := range m.File {
			if !strings.EqualFold(f.Path, "WINDOWS.BAT") {
				files = append(files, f)
			}
		}
		if strings.TrimSpace(text) != "" {
			files = append(files, manifest.FileSpec{Path: "WINDOWS.BAT", Text: &text})
		}
		m.File = files
	}
	data, err := toml.Marshal(m)
	if err == nil {
		_, err = manifest.Parse(data, e.name)
	}
	if err != nil {
		e.err = strings.ReplaceAll(err.Error(), "\n", "; ")
		return
	}
	tmp, err := os.CreateTemp("", "doszer-manifest-*.toml")
	if err != nil {
		e.err = err.Error()
		return
	}
	tmp.Write(data)
	tmp.Close()
	path, name := e.path, e.name
	args := []string{"repack", "--format", "plain", "--manifest", tmp.Name()}
	if e.iconAdd != "" {
		args = append(args, "--add", e.iconPath+"="+e.iconAdd)
	}
	if e.iconTex != nil {
		sdl.DestroyTexture(e.iconTex.t)
	}
	a.edit, a.focusArea, a.focus = nil, nil, nil
	a.runDoszer("Repack: "+name, append(args, path, path), func(err error) {
		os.Remove(tmp.Name())
		if err == nil {
			a.rescan()
		}
	})
}

func (a *app) drawEditor() {
	e := a.edit
	if e == nil {
		return
	}
	a.layer = layerModal
	defer func() { a.layer = layerPage }()
	a.fill(0, 0, a.w, a.h, rgba{0, 0, 0, 170})
	a.onClick(0, 0, a.w, a.h, func(bool) {})
	w, h := min(a.w-a.px(60), a.px(980)), a.h-a.px(60)
	x, y := (a.w-w)/2, (a.h-h)/2
	a.round(x-a.px(1), y-a.px(1), w+a.px(2), h+a.px(2), a.px(14), colLine)
	a.round(x, y, w, h, a.px(14), colPopup)
	p := a.px(20)
	title := e.name
	switch e.mode {
	case editBat:
		title += ": WINDOWS.BAT"
	case editToml:
		title += ": " + manifest.FileName
	}
	a.text(a.ellipsize(title, w-2*p, 17, true), x+p, y+p, 17, true, colText)
	top := y + p + a.px(40)
	bottom := y + h - p - a.px(44)
	switch e.mode {
	case editSettings:
		a.drawEditForm(e, x+p, top, w-2*p)
	case editBat:
		a.drawTextArea(e.bat, x+p, top, w-2*p, bottom-top, a.focusArea == e.bat)
	case editToml:
		a.drawTextArea(e.toml, x+p, top, w-2*p, bottom-top, a.focusArea == e.toml)
	}

	by := y + h - p - a.px(32)
	if e.err != "" {
		a.text(a.ellipsize(e.err, w-2*p-a.px(200), 12, false), x+p, by+a.px(8), 12, false, colError)
	}
	bx := x + w - p - a.buttonW("Save")
	var save func()
	if a.editChanged() {
		save = a.saveEditor
	}
	a.button("Save", bx, by, btnAccent, save)
	bx -= a.buttonW("Cancel") + a.px(8)
	a.button("Cancel", bx, by, btnNormal, a.closeEditor)
}

// drawEditForm draws the Settings tab: every row always there, the ones
// that don't apply to the boot model greyed.
func (a *app) drawEditForm(e *editDialog, x, y, w int32) {
	labelW := a.px(160)
	fx, fw := x+labelW, w-labelW
	row := func(label string, on bool) {
		c := colText
		if !on {
			c = colFaint
		}
		a.text(label, x, y+a.px(7), 13, false, c)
	}
	choose := func(on bool, items []string, cur string, none string, pick func(string)) {
		label := cur
		if label == "" {
			label = none
		}
		var fn func()
		if on && len(items) > 0 {
			bx, by := fx, y
			all := append([]string{none}, items...)
			current := 0
			for i, it := range all {
				if it == cur {
					current = i
				}
			}
			fn = func() {
				a.openPopup(bx, by, a.px(260), all, current, func(i int) {
					v := ""
					if i > 0 {
						v = all[i]
					}
					pick(v)
					e.changed = true
				})
			}
		}
		a.button(a.ellipsize(label, fw-a.px(40), 13, false)+" ▼", fx, y, btnNormal, fn)
		y += a.px(42)
	}
	names := func(shell bool) []string {
		var out []string
		for _, f := range a.files {
			if f.Shell == shell && !f.missing {
				out = append(out, f.Name)
			}
		}
		return out
	}

	row("Name", true)
	a.drawField(&e.fname, fx, y, fw)
	y += a.px(48)

	row("Boots", true)
	models := []string{"DOS", "Windows 3.x", "Windows 9x"}
	mx, my := fx, y
	a.button(models[e.model]+" ▼", fx, y, btnNormal, func() {
		a.openPopup(mx, my, a.px(200), models, e.model, func(i int) {
			e.model, e.changed = i, true
			a.regenerate()
		})
	})
	y += a.px(48)

	row("Program", e.model == 0)
	choose(e.model == 0, e.dosProgs, e.exec, "None (start menu)", func(v string) { e.exec = v })
	row("Shell", e.model == 1)
	choose(e.model == 1, names(true), e.shell, "None", func(v string) { e.shell = v })
	row("OS image", e.model == 2)
	choose(e.model == 2, names(false), e.os, "None", func(v string) { e.os = v })
	row("Starts", e.model != 0)
	choose(e.model != 0, e.winProgs, e.program, "Unchanged", func(v string) {
		e.program = v
		a.regenerate()
	})
	var toggle func()
	if e.model == 2 && e.program != "" {
		toggle = func() { e.colors256, e.changed = !e.colors256, true; a.regenerate() }
	}
	row("256 colours", toggle != nil)
	a.checkbox(fx, y, e.colors256, "", toggle)
	y += a.px(48)

	row("Parent", true)
	choose(true, e.parents, e.parent, "None", func(v string) { e.parent = v })

	// The icon: what it shows now, and where to take another from.
	row("Icon", true)
	is := a.px(32)
	if e.iconTex == nil && e.iconData != nil {
		if img, err := icon.Decode(e.iconData, int(is)); err == nil {
			b := img.Bounds()
			n := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
			draw.Draw(n, n.Bounds(), img, b.Min, draw.Src)
			e.iconTex = a.upload(n, true)
		}
	}
	if e.iconTex != nil {
		a.image(e.iconTex, fx, y, is)
	} else {
		a.round(fx, y, is, is, a.px(6), colField)
	}
	label := e.iconPath
	if label == "" {
		label = "None"
	}
	ix, iy := fx+is+a.px(12), y
	items := append([]string{"From a file…", "None"}, e.iconCands...)
	a.button(a.ellipsize(label, fw-is-a.px(60), 13, false)+" ▼", ix, iy, btnNormal, func() {
		a.openPopup(ix, iy, a.px(260), items, -1, func(i int) {
			switch i {
			case 0:
				a.chooseIconFile(e)
			case 1:
				e.iconPath, e.iconAdd, e.iconData, e.iconChanged = "", "", nil, true
				if e.iconTex != nil {
					sdl.DestroyTexture(e.iconTex.t)
					e.iconTex = nil
				}
			default:
				a.chooseIconFromArchive(e, items[i])
			}
		})
	})
}
