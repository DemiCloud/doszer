package gui

import (
	"os"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/DemiCloud/doszer/internal/install"
	"github.com/DemiCloud/doszer/internal/library"
	"github.com/DemiCloud/doszer/internal/manifest"
	"github.com/DemiCloud/doszer/internal/padmap"
	"github.com/DemiCloud/doszer/internal/repack"
)

// The gamepad editor: a game's [[input.pad]] (spec §5.9), a port at a
// time, on a drawn controller - each control's binding, its actions
// picked from DOSBox Pure's and its label typed - and the action wheel's
// items. Saved as Edit saves: doszer repack --manifest.

type padEditor struct {
	path, name string
	m          *manifest.Manifest
	pads       []padmap.Pad // ports, being edited
	port       int
	sel        string // the control being edited, or "wheel" (wheelSel its item)
	wheelSel   int
	binding    field // the selected binding's text
	saved      []padmap.Pad
	savedFrom  string // where the save's mapping is, "" if there's none
	changed    bool
}

// padControl is a control on the drawn controller: where (as fractions
// of the controller's box), what it shows, and what it is.
type padControl struct {
	key        string
	x, y       float64
	glyph      string
	name       string
	round, big bool
}

// padControls are the RetroPad's controls laid out as a gamepad: b, a,
// y, x are the bottom, right, left and top face buttons.
var padControls = []padControl{
	{"l2", 0.16, 0.06, "L2", "Left trigger", false, false},
	{"r2", 0.84, 0.06, "R2", "Right trigger", false, false},
	{"l", 0.16, 0.17, "L", "Left shoulder", false, false},
	{"r", 0.84, 0.17, "R", "Right shoulder", false, false},
	{"up", 0.20, 0.34, "↑", "D-pad up", false, false},
	{"down", 0.20, 0.54, "↓", "D-pad down", false, false},
	{"left", 0.12, 0.44, "←", "D-pad left", false, false},
	{"right", 0.28, 0.44, "→", "D-pad right", false, false},
	{"select", 0.42, 0.40, "Select", "Select", false, false},
	{"start", 0.58, 0.40, "Start", "Start", false, false},
	{"x", 0.80, 0.32, "X", "Top face button", true, false},
	{"b", 0.80, 0.56, "B", "Bottom face button", true, false},
	{"y", 0.72, 0.44, "Y", "Left face button", true, false},
	{"a", 0.88, 0.44, "A", "Right face button", true, false},
	{"lstick_up", 0.35, 0.64, "↑", "Left stick up", true, false},
	{"lstick_down", 0.35, 0.88, "↓", "Left stick down", true, false},
	{"lstick_left", 0.27, 0.76, "←", "Left stick left", true, false},
	{"lstick_right", 0.43, 0.76, "→", "Left stick right", true, false},
	{"l3", 0.35, 0.76, "L3", "Left stick click", true, true},
	{"rstick_up", 0.65, 0.64, "↑", "Right stick up", true, false},
	{"rstick_down", 0.65, 0.88, "↓", "Right stick down", true, false},
	{"rstick_left", 0.57, 0.76, "←", "Right stick left", true, false},
	{"rstick_right", 0.73, 0.76, "→", "Right stick right", true, false},
	{"r3", 0.65, 0.76, "R3", "Right stick click", true, true},
}

// actionGroups are DOSBox Pure's actions (spec §5.9) as the Add action
// menu offers them.
var actionGroups = []struct {
	name string
	sub  []struct {
		name    string
		actions []string
	}
}{
	{"Keyboard", []struct {
		name    string
		actions []string
	}{
		{"Letters A-M", strings.Fields("a b c d e f g h i j k l m")},
		{"Letters N-Z", strings.Fields("n o p q r s t u v w x y z")},
		{"Digits", strings.Fields("1 2 3 4 5 6 7 8 9 0")},
		{"Function keys", strings.Fields("f1 f2 f3 f4 f5 f6 f7 f8 f9 f10 f11 f12")},
		{"Editing and arrows", strings.Fields("esc tab backspace enter space insert delete home end pageup pagedown up down left right")},
		{"Modifiers", strings.Fields("leftshift rightshift leftctrl rightctrl leftalt rightalt capslock numlock scrolllock")},
		{"Symbols", strings.Fields("grave minus equals leftbracket rightbracket backslash semicolon quote comma period slash extra_lt_gt printscreen pause")},
		{"Keypad", strings.Fields("kp0 kp1 kp2 kp3 kp4 kp5 kp6 kp7 kp8 kp9 kpdivide kpmultiply kpminus kpplus kpenter kpperiod")},
	}},
	{"Mouse", []struct {
		name    string
		actions []string
	}{{"", strings.Fields("mouse_left_click mouse_right_click mouse_middle_click mouse_move_up mouse_move_down mouse_move_left mouse_move_right mouse_speed_up mouse_speed_down")}}},
	{"Joystick", []struct {
		name    string
		actions []string
	}{{"", strings.Fields("joy_button1 joy_button2 joy_button3 joy_button4 joy_up joy_down joy_left joy_right joy_hat_up joy_hat_down joy_hat_left joy_hat_right joy_2_up joy_2_down joy_2_left joy_2_right")}}},
	{"DOSBox Pure", []struct {
		name    string
		actions []string
	}{{"", strings.Fields("menu wheel hold_port1 hold_port2 hold_port3 hold_port4")}}},
}

// openPadEditor opens the gamepad editor on a game's archive.
func (a *app) openPadEditor(g *library.Game) {
	data, _, err := repack.EmbeddedManifest(g.Path)
	if err != nil {
		a.alert("Can't edit "+g.Name+"'s gamepad mapping", err.Error())
		return
	}
	stem := strings.TrimSuffix(g.Rel, ".dosz")
	m, err := manifest.Parse(data, stem)
	if err != nil {
		a.alert("Can't edit "+g.Name+"'s gamepad mapping", err.Error())
		return
	}
	e := &padEditor{path: g.Path, name: g.Name, m: m, pads: m.Pads(), sel: "b"}
	if len(e.pads) == 0 {
		e.pads = []padmap.Pad{{Controls: map[string]string{}}}
	}
	for i := range e.pads {
		if e.pads[i].Controls == nil {
			e.pads[i].Controls = map[string]string{}
		}
	}
	// The mapping DOSBox Pure's Controller Mapper saved, to take over.
	if dir, err := install.GameSaveDir(g.Path, a.saveDirSetting()); err == nil {
		if raw, where, err := install.SavedPadmap(g.Path, dir); err == nil {
			if pads, err := padmap.DecodePADMAP(raw); err == nil {
				e.saved, e.savedFrom = padmap.Label(pads, m.Pads()...), where
			}
		}
	}
	a.padEd = e
	a.selectPadControl(e, "b")
}

// cur is the port being edited.
func (e *padEditor) cur() *padmap.Pad { return &e.pads[e.port] }

// selectPadControl makes key (a control, or "wheel") the one edited.
func (a *app) selectPadControl(e *padEditor, key string) {
	e.sel = key
	text := ""
	if key == "wheel" {
		if e.wheelSel < len(e.cur().Wheel) {
			text = e.cur().Wheel[e.wheelSel]
		}
	} else {
		text = e.cur().Controls[key]
	}
	e.binding = field{placeholder: "Actions joined by +, then a label: leftctrl+x Use Item", onChange: func() { a.padBindingChanged(e) }}
	e.binding.set(text)
	a.focus = &e.binding
}

// padBindingChanged stores the binding field's text as the selected
// control's (or wheel item's) binding: none when empty.
func (a *app) padBindingChanged(e *padEditor) {
	e.changed = true
	t := strings.TrimSpace(e.binding.text)
	p := e.cur()
	if e.sel == "wheel" {
		if e.wheelSel < len(p.Wheel) {
			p.Wheel[e.wheelSel] = t
		}
		return
	}
	if t == "" {
		delete(p.Controls, e.sel)
	} else {
		p.Controls[e.sel] = t
	}
}

// addAction adds an action to the selected binding, before its label.
func (a *app) addAction(e *padEditor, action string) {
	t := strings.TrimSpace(e.binding.text)
	keys, label, _ := strings.Cut(t, " ")
	switch {
	case keys == "":
		keys = action
	case len(strings.Split(keys, "+")) >= 4:
		return // at most four actions: the binding error says so
	default:
		keys += "+" + action
	}
	if label != "" {
		keys += " " + label
	}
	e.binding.set(keys)
	a.padBindingChanged(e)
}

// actionMenu is the Add action menu.
func (a *app) actionMenu(e *padEditor) []menuItem {
	var items []menuItem
	pick := func(act string) menuItem {
		return menuItem{label: act, fn: func() { a.addAction(e, act) }}
	}
	for _, g := range actionGroups {
		var sub []menuItem
		if len(g.sub) == 1 && g.sub[0].name == "" {
			for _, act := range g.sub[0].actions {
				sub = append(sub, pick(act))
			}
		} else {
			for _, s := range g.sub {
				var acts []menuItem
				for _, act := range s.actions {
					acts = append(acts, pick(act))
				}
				sub = append(sub, menuItem{label: s.name, sub: acts})
			}
		}
		items = append(items, menuItem{label: g.name, sub: sub})
	}
	return items
}

// padErrors are the mapping's problems, by port, or nil.
func (e *padEditor) padErrors() []string {
	var out []string
	for i, p := range e.pads {
		for _, err := range p.Validate() {
			out = append(out, "Port "+itoa(i+1)+": "+err.Error())
		}
	}
	return out
}

func (a *app) padKey(sc int32, ctrl bool) {
	if a.focus != nil && sc != scEscape && a.focus.key(sc, ctrl) {
		return
	}
	if sc == scEscape {
		a.padEd, a.focus = nil, nil
	}
}

// savePadEditor writes the mapping into the manifest and repacks.
func (a *app) savePadEditor() {
	e := a.padEd
	pads := e.pads
	// Trailing unmapped ports say nothing.
	for len(pads) > 0 && len(pads[len(pads)-1].Controls) == 0 && len(pads[len(pads)-1].Wheel) == 0 {
		pads = pads[:len(pads)-1]
	}
	var in manifest.Input
	for _, p := range pads {
		var wheel []string
		for _, w := range p.Wheel {
			if strings.TrimSpace(w) != "" {
				wheel = append(wheel, w)
			}
		}
		p.Wheel = wheel
		in.Pad = append(in.Pad, manifest.PadFrom(p))
	}
	m := *e.m
	m.Input = in
	data, err := toml.Marshal(&m)
	if err == nil {
		_, err = manifest.Parse(data, e.name)
	}
	if err != nil {
		a.alert("Can't save the mapping", strings.Split(err.Error(), "\n")...)
		return
	}
	tmp, err := os.CreateTemp("", "doszer-manifest-*.toml")
	if err != nil {
		a.alert("Can't save the mapping", err.Error())
		return
	}
	tmp.Write(data)
	tmp.Close()
	path, name := e.path, e.name
	a.padEd, a.focus = nil, nil
	a.runDoszer("Repack: "+name, []string{"repack", "--manifest", tmp.Name(), path, path}, func(err error) {
		os.Remove(tmp.Name())
		if err == nil {
			a.rescan()
		}
	})
}

func (a *app) drawPadEditor() {
	e := a.padEd
	if e == nil {
		return
	}
	a.layer = layerModal
	defer func() { a.layer = layerPage }()
	a.fill(0, 0, a.w, a.h, rgba{0, 0, 0, 170})
	a.onClick(0, 0, a.w, a.h, func(bool) {})
	w, h := min(a.w-a.px(60), a.px(1040)), a.h-a.px(60)
	x, y := (a.w-w)/2, (a.h-h)/2
	a.round(x-a.px(1), y-a.px(1), w+a.px(2), h+a.px(2), a.px(14), colLine)
	a.round(x, y, w, h, a.px(14), colPopup)
	p := a.px(20)
	a.text(a.ellipsize(e.name+": gamepad", w/2, 17, true), x+p, y+p, 17, true, colText)

	// The ports: one button each, and one to add another.
	bx := x + w - p
	if len(e.pads) < padmap.MaxPads {
		bx -= a.buttonW("Add port")
		a.button("Add port", bx, y+p-a.px(6), btnNormal, func() {
			e.pads = append(e.pads, padmap.Pad{Controls: map[string]string{}})
			e.port, e.changed = len(e.pads)-1, true
			a.selectPadControl(e, "b")
		})
		bx -= a.px(8)
	}
	for i := len(e.pads) - 1; i >= 0; i-- {
		label := "Port " + itoa(i+1)
		bx -= a.buttonW(label)
		style := btnNormal
		if i == e.port {
			style = btnOn
		}
		port := i
		a.button(label, bx, y+p-a.px(6), style, func() {
			e.port, e.wheelSel = port, 0
			a.selectPadControl(e, "b")
		})
		bx -= a.px(4)
	}

	top := y + p + a.px(44)
	bottom := y + h - p - a.px(44)
	rightW := a.px(300)
	lw := w - 2*p - rightW - p
	editH := a.px(150)

	// The controller.
	cx, cy, cw, ch := x+p, top, lw, bottom-top-editH-p
	a.drawPadBody(cx, cy, cw, ch)
	for _, c := range padControls {
		a.drawPadButton(e, c, cx, cy, cw, ch)
	}

	// The selected binding.
	ey := bottom - editH
	a.round(x+p, ey, lw, editH, a.px(10), colCard)
	ix, iw := x+p+a.px(16), lw-a.px(32)
	title := "Wheel item " + itoa(e.wheelSel+1)
	if e.sel != "wheel" {
		for _, c := range padControls {
			if c.key == e.sel {
				title = c.name + " (" + c.key + ")"
			}
		}
	}
	a.text(title, ix, ey+a.px(14), 14, true, colText)
	fy := ey + a.px(44)
	addW := a.buttonW("Add action ▼")
	clrW := a.buttonW("Clear")
	a.drawField(&e.binding, ix, fy, iw-addW-clrW-a.px(16))
	axx, axy := ix+iw-addW-clrW-a.px(8), fy
	a.button("Add action ▼", axx, axy, btnNormal, func() {
		a.openMenuItems(axx, axy+a.px(36), a.actionMenu(e))
	})
	a.button("Clear", ix+iw-clrW, fy, btnNormal, func() {
		e.binding.set("")
		a.padBindingChanged(e)
	})
	if t := strings.TrimSpace(e.binding.text); t != "" {
		msg, c := "", colMuted
		if b, err := padmap.ParseBinding(t); err != nil {
			msg, c = err.Error(), colError
		} else if b.Label == "" {
			msg = "No label: DOSBox Pure shows the actions"
		} else {
			msg = "Shown as " + b.Label
		}
		a.text(a.ellipsize(msg, iw, 12, false), ix, fy+a.px(46), 12, false, c)
	}

	// The action wheel.
	rx := x + w - p - rightW
	a.round(rx, top, rightW, bottom-top, a.px(10), colCard)
	a.text("Action wheel", rx+a.px(16), top+a.px(14), 14, true, colText)
	wy := top + a.px(46)
	lh := a.px(34)
	pd := e.cur()
	for i, item := range pd.Wheel {
		if wy+lh > bottom-a.px(52) {
			break
		}
		sel := e.sel == "wheel" && e.wheelSel == i
		bg := colCard
		switch {
		case sel:
			bg = colCardSel
		case a.hovered(rx+a.px(8), wy, rightW-a.px(16), lh):
			bg = colCardHover
		}
		a.round(rx+a.px(8), wy, rightW-a.px(16), lh, a.px(6), bg)
		label := item
		if b, err := padmap.ParseBinding(item); err == nil && b.Label != "" {
			label = b.Label
		}
		if strings.TrimSpace(label) == "" {
			label = "(empty)"
		}
		a.text(a.ellipsize(itoa(i+1)+".  "+label, rightW-a.px(40), 13, false), rx+a.px(18), wy+(lh-a.lineHeight(13, false))/2, 13, false, colText)
		idx := i
		a.onClick(rx+a.px(8), wy, rightW-a.px(16), lh, func(bool) {
			e.wheelSel = idx
			a.selectPadControl(e, "wheel")
		})
		wy += lh + a.px(4)
	}
	if len(pd.Wheel) == 0 {
		a.text("No items", rx+a.px(16), wy, 13, false, colFaint)
	}
	wb := bottom - a.px(40)
	bx2 := rx + a.px(16)
	if len(pd.Wheel) < padmap.MaxWheel {
		bx2 += a.button("Add item", bx2, wb, btnNormal, func() {
			pd.Wheel = append(pd.Wheel, "")
			e.wheelSel, e.changed = len(pd.Wheel)-1, true
			a.selectPadControl(e, "wheel")
		}) + a.px(8)
	}
	if e.sel == "wheel" && e.wheelSel < len(pd.Wheel) {
		a.button("Remove", bx2, wb, btnNormal, func() {
			pd.Wheel = append(pd.Wheel[:e.wheelSel], pd.Wheel[e.wheelSel+1:]...)
			e.wheelSel, e.changed = max(0, e.wheelSel-1), true
			if len(pd.Wheel) == 0 {
				a.selectPadControl(e, "b")
			} else {
				a.selectPadControl(e, "wheel")
			}
		})
	}

	// Footer: the save's mapping, problems, Cancel and Save.
	by := y + h - p - a.px(32)
	fx := x + p
	if e.savedFrom != "" {
		fx += a.button("From save", fx, by, btnNormal, func() {
			a.confirm("Take the mapping from the save?", "Take it", func() {
				e.pads = append([]padmap.Pad(nil), e.saved...)
				if len(e.pads) == 0 {
					e.pads = []padmap.Pad{{Controls: map[string]string{}}}
				}
				e.port, e.wheelSel, e.changed = 0, 0, true
				a.selectPadControl(e, "b")
			}, "The mapping made in DOSBox Pure's Controller Mapper replaces this one:", e.savedFrom)
		}) + a.px(12)
	}
	errs := e.padErrors()
	if len(errs) > 0 {
		a.text(a.ellipsize(errs[0], w-2*p-(fx-x-p)-a.px(220), 12, false), fx, by+a.px(8), 12, false, colError)
	}
	sx := x + w - p - a.buttonW("Save")
	var save func()
	if e.changed && len(errs) == 0 {
		save = a.savePadEditor
	}
	a.button("Save", sx, by, btnAccent, save)
	sx -= a.buttonW("Cancel") + a.px(8)
	a.button("Cancel", sx, by, btnNormal, func() { a.padEd, a.focus = nil, nil })
}

// drawPadBody draws the controller's outline: a body with two grips.
func (a *app) drawPadBody(x, y, w, h int32) {
	body := rgba{50, 50, 55, 255}
	a.round(x+w*6/100, y+h*24/100, w*88/100, h*50/100, h*12/100, body)
	a.round(x+w*16/100, y+h*50/100, w*30/100, h*48/100, h*16/100, body)
	a.round(x+w*54/100, y+h*50/100, w*30/100, h*48/100, h*16/100, body)
	// Stick wells.
	for _, sx := range []float64{0.35, 0.65} {
		r := min(w, h) * 15 / 100
		a.round(x+int32(sx*float64(w))-r, y+int32(0.76*float64(h))-r, 2*r, 2*r, r, rgba{36, 36, 40, 255})
	}
}

// drawPadButton draws one control, bound or not, selected or not, with
// its binding's label beside it; a click edits it.
func (a *app) drawPadButton(e *padEditor, c padControl, x, y, w, h int32) {
	s := a.px(34)
	bw := s
	if !c.round {
		bw = max(s, a.textWidth(c.glyph, 12, true)+a.px(16))
	}
	if c.big {
		s, bw = a.px(30), a.px(30)
	}
	bx := x + int32(c.x*float64(w)) - bw/2
	by := y + int32(c.y*float64(h)) - s/2
	binding := e.cur().Controls[c.key]
	bg := colButton
	if binding != "" {
		bg = colAccentDark
	}
	if a.hovered(bx, by, bw, s) {
		bg = colCardSel
		if binding != "" {
			bg = colAccent
		}
	}
	r := a.px(6)
	if c.round {
		r = s / 2
	}
	if e.sel == c.key {
		a.outline(bx-a.px(3), by-a.px(3), bw+a.px(6), s+a.px(6), r+a.px(3), a.px(2), colText, colPopup)
	}
	a.round(bx, by, bw, s, r, bg)
	gw := a.textWidth(c.glyph, 12, true)
	a.text(c.glyph, bx+(bw-gw)/2, by+(s-a.lineHeight(12, true))/2, 12, true, colText)
	key := c.key
	a.onClick(bx, by, bw, s, func(bool) { a.selectPadControl(e, key) })
	if binding == "" {
		return
	}
	label := binding
	if b, err := padmap.ParseBinding(binding); err == nil {
		label = b.Label
		if label == "" {
			label, _, _ = strings.Cut(strings.TrimSpace(binding), " ")
		}
	}
	label = a.ellipsize(label, a.px(96), 11, false)
	lw := a.textWidth(label, 11, false)
	a.text(label, bx+(bw-lw)/2, by+s+a.px(3), 11, false, colMuted)
}
