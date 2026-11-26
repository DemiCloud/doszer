package gui

import (
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/DemiCloud/doszer/internal/start"
)

// childDialog is Create child's form: a game over a parent, starting one
// of its programs (`doszer child`).
type childDialog struct {
	parent, parentName string
	cands              []start.Candidate
	choice             int
	name               field
	named              bool // the name was typed, not suggested
}

// openChild opens Create child for the parent at p.
func (a *app) openChild(p, name string) {
	cs, err := start.ParentCandidates(p)
	if err != nil {
		a.alert("Create child", err.Error())
		return
	}
	if len(cs) == 0 {
		a.alert("No programs", filepath.Base(p)+" has no program to start.")
		return
	}
	c := &childDialog{parent: p, parentName: name, cands: cs}
	c.name.set(programName(cs[0].Path))
	c.name.onChange = func() { c.named = true }
	a.child, a.focus, a.popup = c, &c.name, nil
}

// programName suggests a game's name from its program: JEZZBALL.EXE is
// Jezzball.
func programName(p string) string {
	base := strings.TrimSuffix(path.Base(p), path.Ext(p))
	r := []rune(strings.ToLower(base))
	if len(r) > 0 {
		r[0] = unicode.ToUpper(r[0])
	}
	return string(r)
}

func (c *childDialog) output() string {
	return filepath.Join(filepath.Dir(c.parent), safeName(c.name.text)+".dosz")
}

func (a *app) createChild() {
	c := a.child
	name := strings.TrimSpace(c.name.text)
	if safeName(name) == "" {
		return
	}
	out := c.output()
	args := []string{"child", "--format", "plain", "--name", name}
	if _, err := os.Stat(out); err == nil {
		args = append(args, "-f")
	}
	args = append(args, c.parent, c.cands[c.choice].Path, out)
	a.child, a.focus = nil, nil
	a.runDoszer("Create "+name, args, func(err error) {
		if err == nil {
			a.selectAfterScan = out
			a.rescan()
		}
	})
}

func (a *app) childKey(sc int32, ctrl bool) {
	if a.focus != nil && sc != scEscape && sc != scReturn && sc != scKPEnter && a.focus.key(sc, ctrl) {
		return
	}
	switch sc {
	case scEscape:
		a.child, a.focus = nil, nil
	case scReturn, scKPEnter:
		a.createChild()
	}
}

func (a *app) drawChild() {
	c := a.child
	if c == nil {
		return
	}
	a.layer = layerModal
	defer func() { a.layer = layerPage }()
	a.fill(0, 0, a.w, a.h, rgba{0, 0, 0, 170})
	a.onClick(0, 0, a.w, a.h, func(bool) {})
	w, h := min(a.w-a.px(40), a.px(720)), a.px(260)
	x, y := (a.w-w)/2, (a.h-h)/2
	a.round(x-a.px(1), y-a.px(1), w+a.px(2), h+a.px(2), a.px(14), colLine)
	a.round(x, y, w, h, a.px(14), colPopup)
	p := a.px(24)
	a.text(a.ellipsize("Create a child of "+c.parentName, w-2*p, 17, true), x+p, y+p, 17, true, colText)
	fx := x + p + a.px(120)
	fw := x + w - p - fx
	ry := y + p + a.px(48)
	row := func(label string) { a.text(label, x+p, ry+a.px(7), 13, false, colText) }

	row("Program")
	items := make([]string, len(c.cands))
	for i, cand := range c.cands {
		items[i] = strings.ReplaceAll(cand.Path, "/", `\`) + "\t" + cand.Target
	}
	label := strings.ReplaceAll(c.cands[c.choice].Path, "/", `\`) + " ▼"
	bx, by := fx, ry
	a.button(label, bx, by, btnNormal, func() {
		a.openPopup(bx, by, max(a.buttonW(label), a.px(320)), items, c.choice, func(i int) {
			c.choice = i
			if !c.named {
				c.name.set(programName(c.cands[i].Path))
			}
		})
	})
	ry += a.px(44)

	row("Name")
	a.drawField(&c.name, fx, ry, fw)
	ry += a.px(36)
	out := c.output()
	if _, err := os.Stat(out); err == nil && safeName(c.name.text) != "" {
		a.text(a.ellipsize("Replaces "+filepath.Base(out), fw, 12, false), fx, ry, 12, false, colShell)
	} else {
		a.text(a.ellipsize(filepath.Base(out), fw, 12, false), fx, ry, 12, false, colFaint)
	}

	by = y + h - p - a.px(32)
	bx = x + w - p - a.buttonW("Create")
	var create func()
	if safeName(strings.TrimSpace(c.name.text)) != "" {
		create = a.createChild
	}
	a.button("Create", bx, by, btnAccent, create)
	bx -= a.buttonW("Cancel") + a.px(8)
	a.button("Cancel", bx, by, btnNormal, func() { a.child, a.focus = nil, nil })
}
