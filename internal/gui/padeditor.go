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

// padLayout is a controller to draw the RetroPad's controls as: the
// labels its buttons carry and where they are (fractions of the drawing),
// and its inert parts (a Steam Controller's trackpads). b, a, y, x are
// the RetroPad's bottom, right, left and top face buttons whatever the
// controller calls them.
type padLayout struct {
	name   string
	glyphs map[string]string
	pos    map[string][2]float64
	inert  [][4]float64 // x, y (centre), w, h
	body   [][4]float64 // the outline, as rounded rectangles: x, y, w, h
}

var xboxGlyphs = map[string]string{
	"b": "A", "a": "B", "y": "X", "x": "Y",
	"l": "LB", "r": "RB", "l2": "LT", "r2": "RT", "l3": "LS", "r3": "RS",
	"select": "View", "start": "Menu",
	"up": "↑", "down": "↓", "left": "←", "right": "→",
	"lstick_up": "↑", "lstick_down": "↓", "lstick_left": "←", "lstick_right": "→",
	"rstick_up": "↑", "rstick_down": "↓", "rstick_left": "←", "rstick_right": "→",
}

// layoutAt places the controls: triggers and bumpers fixed, the sticks,
// the D-pad, the face buttons and View/Menu where the controller has them.
func layoutAt(lstick, rstick, dpad, face, view, menu [2]float64) map[string][2]float64 {
	p := map[string][2]float64{
		"l2": {0.24, 0.05}, "l": {0.24, 0.15}, "r2": {0.76, 0.05}, "r": {0.76, 0.15},
		"select": view, "start": menu,
	}
	cross := func(c [2]float64, dx, dy float64, up, down, left, right, mid string) {
		p[up] = [2]float64{c[0], c[1] - dy}
		p[down] = [2]float64{c[0], c[1] + dy}
		p[left] = [2]float64{c[0] - dx, c[1]}
		p[right] = [2]float64{c[0] + dx, c[1]}
		if mid != "" {
			p[mid] = c
		}
	}
	cross(lstick, 0.068, 0.115, "lstick_up", "lstick_down", "lstick_left", "lstick_right", "l3")
	cross(rstick, 0.068, 0.115, "rstick_up", "rstick_down", "rstick_left", "rstick_right", "r3")
	cross(dpad, 0.05, 0.085, "up", "down", "left", "right", "")
	cross(face, 0.05, 0.085, "x", "b", "y", "a", "")
	return p
}

// padLayouts are the controllers the editor draws; the first is the
// default.
var padLayouts = []padLayout{
	{
		name:   "Xbox",
		glyphs: xboxGlyphs,
		pos:    layoutAt([2]float64{0.30, 0.40}, [2]float64{0.60, 0.68}, [2]float64{0.40, 0.68}, [2]float64{0.72, 0.40}, [2]float64{0.44, 0.38}, [2]float64{0.56, 0.38}),
		body:   [][4]float64{{0.08, 0.22, 0.84, 0.56}, {0.10, 0.45, 0.30, 0.52}, {0.60, 0.45, 0.30, 0.52}},
	},
	{
		name:   "Steam Controller",
		glyphs: xboxGlyphs,
		pos:    layoutAt([2]float64{0.37, 0.46}, [2]float64{0.63, 0.46}, [2]float64{0.17, 0.34}, [2]float64{0.83, 0.34}, [2]float64{0.40, 0.22}, [2]float64{0.60, 0.22}),
		inert:  [][4]float64{{0.37, 0.76, 0.17, 0.26}, {0.63, 0.76, 0.17, 0.26}},
		body:   [][4]float64{{0.06, 0.20, 0.88, 0.62}, {0.08, 0.45, 0.26, 0.52}, {0.66, 0.45, 0.26, 0.52}},
	},
}

// padGroups are the controls the side lists show, by side and group, in
// the order the controller has them.
var padGroups = [2][]struct {
	title string
	keys  []string
}{
	{
		{"Left", []string{"l2", "l"}},
		{"Left stick", []string{"l3", "lstick_up", "lstick_down", "lstick_left", "lstick_right"}},
		{"D-pad", []string{"up", "down", "left", "right"}},
	},
	{
		{"Right", []string{"r2", "r"}},
		{"Face buttons", []string{"x", "y", "a", "b"}},
		{"Right stick", []string{"r3", "rstick_up", "rstick_down", "rstick_left", "rstick_right"}},
		{"Menu buttons", []string{"select", "start"}},
	},
}

// padRowName is a control's name in its group: a direction, Click, or
// what the controller calls it.
func padRowName(l *padLayout, key string) string {
	switch {
	case strings.HasSuffix(key, "_up") || key == "up":
		return "Up"
	case strings.HasSuffix(key, "_down") || key == "down":
		return "Down"
	case strings.HasSuffix(key, "_left") || key == "left":
		return "Left"
	case strings.HasSuffix(key, "_right") || key == "right":
		return "Right"
	case key == "l3" || key == "r3":
		return "Click (" + l.glyphs[key] + ")"
	}
	return l.glyphs[key]
}

// padTitle is a control's full name: what the controller calls it, what
// the RetroPad does, and the manifest's key.
func padTitle(l *padLayout, key string) string {
	where := map[string]string{
		"b": "bottom face button", "a": "right face button", "y": "left face button", "x": "top face button",
		"l": "left bumper", "r": "right bumper", "l2": "left trigger", "r2": "right trigger",
		"l3": "left stick click", "r3": "right stick click", "select": "select", "start": "start",
		"up": "D-pad up", "down": "D-pad down", "left": "D-pad left", "right": "D-pad right",
		"lstick_up": "left stick up", "lstick_down": "left stick down", "lstick_left": "left stick left", "lstick_right": "left stick right",
		"rstick_up": "right stick up", "rstick_down": "right stick down", "rstick_left": "right stick left", "rstick_right": "right stick right",
	}[key]
	g := l.glyphs[key]
	if g == "" || strings.ContainsAny(g, "↑↓←→") {
		return strings.ToUpper(where[:1]) + where[1:] + " (" + key + ")"
	}
	return g + ", " + where + " (" + key + ")"
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
	w, h := min(a.w-a.px(40), a.px(1180)), a.h-a.px(40)
	x, y := (a.w-w)/2, (a.h-h)/2
	a.round(x-a.px(1), y-a.px(1), w+a.px(2), h+a.px(2), a.px(14), colLine)
	a.round(x, y, w, h, a.px(14), colPopup)
	p := a.px(18)
	l := &padLayouts[min(a.padLayout, len(padLayouts)-1)]

	// Header: the game, the controller drawn, the ports.
	tx := x + p + a.text(a.ellipsize(e.name, w/3, 17, true), x+p, y+p, 17, true, colText) + a.px(16)
	label := l.name + " ▼"
	lx, ly := tx, y+p-a.px(6)
	names := make([]string, len(padLayouts))
	for i, pl := range padLayouts {
		names[i] = pl.name
	}
	a.button(label, lx, ly, btnNormal, func() {
		a.openPopup(lx, ly, a.px(200), names, a.padLayout, func(i int) { a.padLayout = i })
	})
	bx := x + w - p
	if len(e.pads) < padmap.MaxPads {
		bx -= a.buttonW("Add port")
		a.button("Add port", bx, ly, btnNormal, func() {
			e.pads = append(e.pads, padmap.Pad{Controls: map[string]string{}})
			e.port, e.changed = len(e.pads)-1, true
			a.selectPadControl(e, "b")
		})
		bx -= a.px(8)
	}
	for i := len(e.pads) - 1; i >= 0; i-- {
		pl := "Port " + itoa(i+1)
		bx -= a.buttonW(pl)
		style := btnNormal
		if i == e.port {
			style = btnOn
		}
		port := i
		a.button(pl, bx, ly, style, func() {
			e.port, e.wheelSel = port, 0
			a.selectPadControl(e, e.sel)
		})
		bx -= a.px(4)
	}

	top := y + p + a.px(42)
	bottom := y + h - p - a.px(46)
	side := a.px(250)
	gap := a.px(14)
	cx, cw := x+p+side+gap, w-2*p-2*side-2*gap

	a.drawPadPanel(e, l, padGroups[0], x+p, top, side, bottom-top)
	a.drawPadPanel(e, l, padGroups[1], x+w-p-side, top, side, bottom-top)

	// Centre: the controller, the binding being edited, the wheel.
	editH := a.px(128)
	drawH := min((bottom-top)*45/100, cw*62/100)
	a.drawController(e, l, cx, top, cw, drawH)
	ey := top + drawH + gap
	a.drawBindingEditor(e, l, cx, ey, cw, editH)
	wy := ey + editH + gap
	a.drawWheelList(e, cx, wy, cw, bottom-wy)

	// Footer: import, the save's mapping, problems, Cancel and Save.
	by := y + h - p - a.px(32)
	fx := x + p
	fx += a.button("Import…", fx, by, btnNormal, func() {
		for _, path := range pickFiles("Import a gamepad mapping", filePad) {
			a.padImport(path)
		}
	}) + a.px(8)
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
		}) + a.px(8)
	}
	errs := e.padErrors()
	if len(errs) > 0 {
		a.text(a.ellipsize(errs[0], x+w-p-a.px(200)-fx-a.px(8), 12, false), fx+a.px(8), by+a.px(8), 12, false, colError)
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

// drawPadPanel draws a side's groups of controls, each a row with its
// binding; a click edits it.
func (a *app) drawPadPanel(e *padEditor, l *padLayout, groups []struct {
	title string
	keys  []string
}, x, y, w, h int32) {
	a.round(x, y, w, h, a.px(10), colCard)
	a.pushClip(x, y, w, h)
	defer a.popClip()
	rh := a.px(28)
	nameW := a.px(76)
	yy := y + a.px(10)
	for _, g := range groups {
		a.text(g.title, x+a.px(14), yy+a.px(4), 12, true, colMuted)
		yy += a.px(24)
		for _, k := range g.keys {
			sel := e.sel == k
			rx, rw := x+a.px(6), w-a.px(12)
			if sel {
				a.round(rx, yy, rw, rh, a.px(6), colCardSel)
			} else if a.hovered(rx, yy, rw, rh) {
				a.round(rx, yy, rw, rh, a.px(6), colCardHover)
			}
			a.text(padRowName(l, k), x+a.px(14), yy+(rh-a.lineHeight(13, false))/2, 13, false, colText)
			bx, bw := x+a.px(14)+nameW, w-a.px(28)-nameW
			binding := e.cur().Controls[k]
			box := colField
			if binding != "" {
				box = rgba{40, 64, 104, 255}
			}
			a.round(bx, yy+a.px(3), bw, rh-a.px(6), a.px(5), box)
			shown, c := "—", colFaint
			if binding != "" {
				shown, c = bindingShown(binding), colText
			}
			sw := a.textWidth(a.ellipsize(shown, bw-a.px(12), 12, false), 12, false)
			a.text(a.ellipsize(shown, bw-a.px(12), 12, false), bx+(bw-sw)/2, yy+(rh-a.lineHeight(12, false))/2, 12, false, c)
			key := k
			a.clipHit(rx, yy, rw, rh, func(bool) { a.selectPadControl(e, key) })
			yy += rh + a.px(2)
		}
		yy += a.px(8)
	}
}

// bindingShown is what a binding shows: its label, else its actions.
func bindingShown(binding string) string {
	if b, err := padmap.ParseBinding(binding); err == nil && b.Label != "" {
		return b.Label
	}
	keys, _, _ := strings.Cut(strings.TrimSpace(binding), " ")
	return keys
}

// drawController draws the controller and its controls, in a box of the
// controller's proportions inside x, y, w, h.
func (a *app) drawController(e *padEditor, l *padLayout, x, y, w, h int32) {
	bw, bh := w, w*58/100
	if bh > h {
		bh, bw = h, h*100/58
	}
	bx, by := x+(w-bw)/2, y+(h-bh)/2
	frac := func(fx, fy float64) (int32, int32) {
		return bx + int32(fx*float64(bw)), by + int32(fy*float64(bh))
	}
	body := rgba{50, 50, 55, 255}
	for _, r := range l.body {
		rx, ry := frac(r[0], r[1])
		a.round(rx, ry, int32(r[2]*float64(bw)), int32(r[3]*float64(bh)), int32(0.14*float64(bh)), body)
	}
	for _, r := range l.inert {
		rw, rh := int32(r[2]*float64(bw)), int32(r[3]*float64(bh))
		rx, ry := frac(r[0], r[1])
		a.round(rx-rw/2, ry-rh/2, rw, rh, a.px(10), rgba{40, 40, 45, 255})
	}
	// Stick wells.
	for _, k := range []string{"l3", "r3"} {
		c := l.pos[k]
		r := int32(0.18 * float64(bh))
		cx, cy := frac(c[0], c[1])
		a.round(cx-r, cy-r, 2*r, 2*r, r, rgba{38, 38, 43, 255})
	}
	s := max(a.px(22), int32(0.085*float64(bh)))
	for key, c := range l.pos {
		g := l.glyphs[key]
		w := s
		round := !strings.ContainsAny(key, "_") && len(g) == 1 || key == "l3" || key == "r3" || strings.Contains(key, "stick")
		if !round {
			w = max(s, a.textWidth(g, 11, true)+a.px(14))
		}
		cx, cy := frac(c[0], c[1])
		px, py := cx-w/2, cy-s/2
		binding := e.cur().Controls[key]
		bg := colButton
		if binding != "" {
			bg = colAccentDark
		}
		if a.hovered(px, py, w, s) {
			bg = colCardSel
			if binding != "" {
				bg = colAccent
			}
		}
		r := a.px(5)
		if round {
			r = s / 2
		}
		if e.sel == key {
			a.outline(px-a.px(3), py-a.px(3), w+a.px(6), s+a.px(6), r+a.px(3), a.px(2), colText, colPopup)
		}
		a.round(px, py, w, s, r, bg)
		gw := a.textWidth(g, 11, true)
		a.text(g, px+(w-gw)/2, py+(s-a.lineHeight(11, true))/2, 11, true, colText)
		k := key
		a.onClick(px, py, w, s, func(bool) { a.selectPadControl(e, k) })
	}
}

// drawBindingEditor draws the selected control's (or wheel item's)
// binding: its text, Add action, Clear, and what it shows or what's
// wrong with it.
func (a *app) drawBindingEditor(e *padEditor, l *padLayout, x, y, w, h int32) {
	a.round(x, y, w, h, a.px(10), colCard)
	ix, iw := x+a.px(14), w-a.px(28)
	title := "Wheel item " + itoa(e.wheelSel+1)
	if e.sel != "wheel" {
		title = padTitle(l, e.sel)
	}
	a.text(a.ellipsize(title, iw, 14, true), ix, y+a.px(12), 14, true, colText)
	fy := y + a.px(42)
	addW, clrW := a.buttonW("Add action ▼"), a.buttonW("Clear")
	a.drawField(&e.binding, ix, fy, iw-addW-clrW-a.px(16))
	axx := ix + iw - addW - clrW - a.px(8)
	a.button("Add action ▼", axx, fy, btnNormal, func() {
		a.openMenuItems(axx, fy+a.px(36), a.actionMenu(e))
	})
	a.button("Clear", ix+iw-clrW, fy, btnNormal, func() {
		e.binding.set("")
		a.padBindingChanged(e)
	})
	msg, c := "Actions joined by +, then the label DOSBox Pure shows", colFaint
	if t := strings.TrimSpace(e.binding.text); t != "" {
		if b, err := padmap.ParseBinding(t); err != nil {
			msg, c = err.Error(), colError
		} else if b.Label == "" {
			msg, c = "No label: DOSBox Pure shows the actions", colMuted
		} else {
			msg, c = "Shown as “"+b.Label+"”", colMuted
		}
	}
	a.text(a.ellipsize(msg, iw, 12, false), ix, fy+a.px(44), 12, false, c)
}

// drawWheelList draws the action wheel's items, Add and Remove.
func (a *app) drawWheelList(e *padEditor, x, y, w, h int32) {
	a.round(x, y, w, h, a.px(10), colCard)
	pd := e.cur()
	hx := x + a.px(14)
	a.text("Action wheel", hx, y+a.px(12), 14, true, colText)
	bx := x + w - a.px(14)
	if e.sel == "wheel" && e.wheelSel < len(pd.Wheel) {
		bx -= a.buttonW("Remove")
		a.button("Remove", bx, y+a.px(6), btnNormal, func() {
			pd.Wheel = append(pd.Wheel[:e.wheelSel], pd.Wheel[e.wheelSel+1:]...)
			e.wheelSel, e.changed = max(0, e.wheelSel-1), true
			if len(pd.Wheel) == 0 {
				a.selectPadControl(e, "b")
			} else {
				a.selectPadControl(e, "wheel")
			}
		})
		bx -= a.px(6)
	}
	if len(pd.Wheel) < padmap.MaxWheel {
		bx -= a.buttonW("Add item")
		a.button("Add item", bx, y+a.px(6), btnNormal, func() {
			pd.Wheel = append(pd.Wheel, "")
			e.wheelSel, e.changed = len(pd.Wheel)-1, true
			a.selectPadControl(e, "wheel")
		})
	}
	a.pushClip(x, y+a.px(44), w, h-a.px(48))
	defer a.popClip()
	ly := y + a.px(46)
	if len(pd.Wheel) == 0 {
		a.text("No items: the wheel action opens a menu of these", hx, ly+a.px(4), 12, false, colFaint)
		return
	}
	// Items in two columns.
	colW := (w - a.px(28) - a.px(8)) / 2
	rh := a.px(28)
	for i, item := range pd.Wheel {
		cx := hx + int32(i%2)*(colW+a.px(8))
		cy := ly + int32(i/2)*(rh+a.px(4))
		sel := e.sel == "wheel" && e.wheelSel == i
		bg := colField
		switch {
		case sel:
			bg = colCardSel
		case a.hovered(cx, cy, colW, rh):
			bg = colCardHover
		}
		a.round(cx, cy, colW, rh, a.px(6), bg)
		shown := bindingShown(item)
		if strings.TrimSpace(shown) == "" {
			shown = "(empty)"
		}
		a.text(a.ellipsize(itoa(i+1)+".  "+shown, colW-a.px(16), 12, false), cx+a.px(10), cy+(rh-a.lineHeight(12, false))/2, 12, false, colText)
		idx := i
		a.clipHit(cx, cy, colW, rh, func(bool) {
			e.wheelSel = idx
			a.selectPadControl(e, "wheel")
		})
	}
}
