package gui

import "github.com/DemiCloud/doszer/internal/sdl"

// multiSel is a list's selection of several items, by key: a click
// selects one, Ctrl+click adds or removes one, Shift+click selects the
// range from the last one clicked, and a drag from an item selects the
// range it crosses. Empty, the list's own current item is the selection.
type multiSel struct {
	keys   map[string]bool
	anchor string // where a range starts
	drag   bool   // a plain click's button is still down: a drag selects
}

func (m *multiSel) has(k string) bool { return m.keys[k] }
func (m *multiSel) count() int        { return len(m.keys) }

func (m *multiSel) clear() { m.keys, m.anchor = nil, "" }

// only selects k alone.
func (m *multiSel) only(k string) {
	m.keys, m.anchor = map[string]bool{k: true}, k
}

// click applies a click on k, in a list showing order; cur is the item
// current before it, which Ctrl and Shift build on. A right click keeps
// a selection holding k, else selects k alone. It reports a plain click.
func (m *multiSel) click(order []string, k, cur string, right bool) bool {
	mod := modState()
	if len(m.keys) == 0 && cur != "" {
		m.only(cur)
	}
	switch {
	case right:
		if !m.has(k) {
			m.only(k)
		}
	case mod&sdl.KmodShift != 0:
		anchor := m.anchor
		if anchor == "" {
			anchor = k
		}
		if mod&sdl.KmodCtrl == 0 {
			m.keys = map[string]bool{}
		}
		m.addRange(order, anchor, k)
		m.anchor = anchor
	case mod&sdl.KmodCtrl != 0:
		if m.has(k) && len(m.keys) > 1 {
			delete(m.keys, k)
		} else {
			m.keys[k] = true
		}
		m.anchor = k
	default:
		m.only(k)
		m.drag = true
		return true
	}
	return false
}

// dragTo selects the range from where a drag started to k.
func (m *multiSel) dragTo(order []string, k string) {
	if !m.drag || m.anchor == "" {
		return
	}
	m.keys = map[string]bool{}
	m.addRange(order, m.anchor, k)
}

func (m *multiSel) addRange(order []string, from, to string) {
	i, j := -1, -1
	for n, k := range order {
		if k == from {
			i = n
		}
		if k == to {
			j = n
		}
	}
	if i < 0 || j < 0 {
		m.keys[to] = true
		return
	}
	if i > j {
		i, j = j, i
	}
	for _, k := range order[i : j+1] {
		m.keys[k] = true
	}
}

// selected are the selected keys, in order; with none selected, cur.
func (m *multiSel) selected(order []string, cur string) []string {
	if len(m.keys) == 0 {
		if cur == "" {
			return nil
		}
		return []string{cur}
	}
	var out []string
	for _, k := range order {
		if m.keys[k] {
			out = append(out, k)
		}
	}
	return out
}

// all selects every item.
func (m *multiSel) all(order []string) {
	m.keys = map[string]bool{}
	for _, k := range order {
		m.keys[k] = true
	}
	if len(order) > 0 {
		m.anchor = order[0]
	}
}

// modState is the modifier keys held (a variable for tests).
var modState = func() uint16 {
	if sdl.GetModState == nil {
		return 0
	}
	return uint16(sdl.GetModState())
}

// dragRow is an item a drag can reach: drawn this frame, at its
// rectangle.
type dragRow struct {
	m          *multiSel
	order      func() []string
	key        string
	x, y, w, h int32
	onto       func() // after the selection grew to it
}

// dragItem registers an item of m's list for drags.
func (a *app) dragItem(m *multiSel, order func() []string, key string, x, y, w, h int32, onto func()) {
	a.dragRows = append(a.dragRows, dragRow{m, order, key, x, y, w, h, onto})
}

// dragSel extends a drag selection to the item under the mouse.
func (a *app) dragSel() {
	for _, r := range a.dragRows {
		if r.m.drag && a.mx >= r.x && a.mx < r.x+r.w && a.my >= r.y && a.my < r.y+r.h {
			r.m.dragTo(r.order(), r.key)
			if r.onto != nil {
				r.onto()
			}
			return
		}
	}
}

// endDrags ends every list's drag.
func (a *app) endDrags() {
	for _, m := range []*multiSel{&a.logMulti, &a.gameMulti, &a.parentMulti, &a.sysMulti, &a.srcMulti, &a.saveMulti} {
		m.drag = false
	}
}
