package gui

import (
	"archive/zip"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/DemiCloud/doszer/internal/chain"
	"github.com/DemiCloud/doszer/internal/library"
	"github.com/DemiCloud/doszer/internal/manifest"
	"github.com/DemiCloud/doszer/internal/platform"
	"github.com/DemiCloud/doszer/internal/repack"
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
	tab        int // 0 Settings, 1 WINDOWS.BAT, 2 DOSZ.TOML
	bat, toml  *textArea
	err        string
	changed    bool // the form changed

	// The form.
	fname        field
	model        int    // 0 DOS, 1 Windows 3.x, 2 Windows 9x
	exec         string // DOS: boot.exec
	shell, os    string
	parent       string
	program      string // Windows: what WINDOWS.BAT starts (a path in the archive)
	colors256    bool
	dosProgs     []string // the archive's DOS programs and batch files, as DOS paths
	winProgs     []string // its Windows programs, as paths
	files        map[string]bool
	parents      []string
	tomlFromForm bool // the DOSZ.TOML tab shows what the form made
}

var editTabs = []string{"Settings", "WINDOWS.BAT", manifest.FileName}

// openEditor opens the editor on a game's archive.
func (a *app) openEditor(g *library.Game) {
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
	e := &editDialog{path: g.Path, name: g.Name, m: m, bat: newTextArea(string(bat)), toml: newTextArea(string(data)), files: map[string]bool{}}
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
	a.edit, a.focus, a.focusArea, a.popup = e, &e.fname, nil, nil
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
		}
	}
	sort.Strings(e.dosProgs)
	sort.Strings(e.winProgs)
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

// switchTab moves between the tabs, keeping them in step: the form makes
// DOSZ.TOML, and an edited DOSZ.TOML refills the form, if it parses.
func (a *app) switchTab(to int) {
	e := a.edit
	if e.tab == 2 && e.toml.changed {
		m, err := manifest.Parse([]byte(e.toml.text()), e.name)
		if err != nil {
			e.err = strings.ReplaceAll(err.Error(), "\n", "; ")
			return
		}
		e.m = m
		e.fromManifest()
		e.changed = true
	}
	if to == 2 {
		e.toManifest()
		if data, err := toml.Marshal(e.m); err == nil {
			changed := e.toml.changed || e.changed
			e.toml.set(string(data))
			e.toml.changed = changed
		}
	}
	e.err, e.tab = "", to
	a.focus, a.focusArea = nil, nil
	switch to {
	case 0:
		a.focus = &e.fname
	case 1:
		a.focusArea = e.bat
	case 2:
		a.focusArea = e.toml
	}
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
	return e.changed || e.bat.changed || e.toml.changed || strings.TrimSpace(e.fname.text) != e.m.Name
}

func (a *app) closeEditor() {
	if a.editChanged() {
		a.confirm("Discard your changes?", "Discard", func() { a.edit, a.focusArea, a.focus = nil, nil, nil }, "")
		return
	}
	a.edit, a.focusArea, a.focus = nil, nil, nil
}

// saveEditor checks the manifest and repacks the archive with it.
func (a *app) saveEditor() {
	e := a.edit
	if e.tab == 2 {
		a.switchTab(0) // the DOSZ.TOML tab's text, parsed
		if e.err != "" {
			e.tab = 2
			return
		}
	}
	e.toManifest()
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
	a.edit, a.focusArea, a.focus = nil, nil, nil
	a.runDoszer("Repack: "+name, []string{"repack", "--format", "plain", "--manifest", tmp.Name(), path, path}, func(err error) {
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
	a.text(a.ellipsize(e.name, w/2, 17, true), x+p, y+p, 17, true, colText)
	tx := x + w - p
	for i := len(editTabs) - 1; i >= 0; i-- {
		tx -= a.buttonW(editTabs[i])
		style := btnNormal
		if i == e.tab {
			style = btnOn
		}
		idx := i
		a.button(editTabs[i], tx, y+p-a.px(6), style, func() { a.switchTab(idx) })
		tx -= a.px(4)
	}
	top := y + p + a.px(40)
	bottom := y + h - p - a.px(44)
	switch e.tab {
	case 0:
		a.drawEditForm(e, x+p, top, w-2*p)
	case 1:
		a.drawTextArea(e.bat, x+p, top, w-2*p, bottom-top, a.focusArea == e.bat)
	case 2:
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
	row("Display", e.model == 2)
	box := "□  256 colours"
	if e.colors256 {
		box = "■  256 colours"
	}
	var toggle func()
	if e.model == 2 && e.program != "" {
		toggle = func() { e.colors256, e.changed = !e.colors256, true; a.regenerate() }
	}
	a.button(box, fx, y, btnGhost, toggle)
	y += a.px(48)

	row("Parent", true)
	choose(true, e.parents, e.parent, "None", func(v string) { e.parent = v })
}
