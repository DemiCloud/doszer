package gui

import (
	"strings"
	"unicode/utf8"

	"github.com/DemiCloud/doszer/internal/sdl"
)

// Layers, drawn in order: a click goes to the topmost. A popup can open
// over a dialog (a picker).
const (
	layerPage = iota
	layerModal
	layerPopup
)

// hit is a clickable area drawn this frame.
type hit struct {
	x, y, w, h int32
	layer      int
	fn         func(double bool)
}

// onClick registers fn for a click in the rectangle, on the layer being
// drawn.
func (a *app) onClick(x, y, w, h int32, fn func(double bool)) {
	a.hits = append(a.hits, hit{x, y, w, h, a.layer, fn})
}

// top is the topmost layer open: clicks and hover below it are blocked.
func (a *app) top() int {
	switch {
	case a.popup != nil:
		return layerPopup
	case a.setup != nil || a.dialog != nil || a.add != nil || a.settingsOpen:
		return layerModal
	}
	return layerPage
}

// hovered reports whether the mouse is over the rectangle, on a layer
// that can be hovered.
func (a *app) hovered(x, y, w, h int32) bool {
	return a.layer >= a.top() && a.mx >= x && a.mx < x+w && a.my >= y && a.my < y+h && a.inClip(a.mx, a.my)
}

func (a *app) click(double bool) {
	top := a.top()
	for i := len(a.hits) - 1; i >= 0; i-- {
		h := a.hits[i]
		if h.layer < top {
			break
		}
		if a.mx >= h.x && a.mx < h.x+h.w && a.my >= h.y && a.my < h.y+h.h {
			h.fn(double)
			return
		}
	}
	// A click outside every field ends editing.
	if top == layerPage {
		a.blur()
	}
}

// Clipping: rectangles intersect as they nest.
func (a *app) pushClip(x, y, w, h int32) {
	r := sdl.Rect{X: x, Y: y, W: w, H: h}
	if n := len(a.clips); n > 0 {
		c := a.clips[n-1]
		x0, y0 := max(r.X, c.X), max(r.Y, c.Y)
		x1, y1 := min(r.X+r.W, c.X+c.W), min(r.Y+r.H, c.Y+c.H)
		r = sdl.Rect{X: x0, Y: y0, W: max(0, x1-x0), H: max(0, y1-y0)}
	}
	a.clips = append(a.clips, r)
	sdl.RenderSetClipRect(a.ren, &r)
}

func (a *app) popClip() {
	a.clips = a.clips[:len(a.clips)-1]
	if n := len(a.clips); n > 0 {
		sdl.RenderSetClipRect(a.ren, &a.clips[n-1])
	} else {
		sdl.RenderSetClipRect(a.ren, nil)
	}
}

func (a *app) inClip(x, y int32) bool {
	if len(a.clips) == 0 {
		return true
	}
	c := a.clips[len(a.clips)-1]
	return x >= c.X && x < c.X+c.W && y >= c.Y && y < c.Y+c.H
}

// clipHit registers fn for the part of the rectangle inside the clip.
func (a *app) clipHit(x, y, w, h int32, fn func(bool)) {
	if n := len(a.clips); n > 0 {
		c := a.clips[n-1]
		x0, y0 := max(x, c.X), max(y, c.Y)
		x1, y1 := min(x+w, c.X+c.W), min(y+h, c.Y+c.H)
		if x1 <= x0 || y1 <= y0 {
			return
		}
		x, y, w, h = x0, y0, x1-x0, y1-y0
	}
	a.onClick(x, y, w, h, fn)
}

// Button styles.
const (
	btnNormal = iota
	btnOn     // a selected tab or toggle
	btnAccent // the main action
	btnGhost  // no background until hovered
)

// buttonW is a button's width for label.
func (a *app) buttonW(label string) int32 {
	label, arrow := strings.CutSuffix(label, " ▼")
	w := a.textWidth(label, 13, false) + a.px(24)
	if arrow {
		w += a.px(16)
	}
	return w
}

// button draws a button whose label may end in " ▼" (a dropdown), runs fn
// when clicked, and returns its width.
func (a *app) button(label string, x, y int32, style int, fn func()) int32 {
	w, h := a.buttonW(label), a.px(32)
	label, arrow := strings.CutSuffix(label, " ▼")
	col, tc := colButton, colText
	switch style {
	case btnOn:
		col = colButtonOn
	case btnAccent:
		col = colAccentDark
	case btnGhost:
		col = rgba{}
	}
	if fn == nil {
		tc = colFaint
		if style == btnAccent {
			col = colButton
		}
	} else if a.hovered(x, y, w, h) {
		col = colCardSel
		if style == btnAccent {
			col = colAccent
		}
	}
	if col.a != 0 {
		a.round(x, y, w, h, a.px(6), col)
	}
	lh := a.lineHeight(13, false)
	tw := a.text(label, x+a.px(12), y+(h-lh)/2, 13, false, tc)
	if arrow {
		al := a.lineHeight(8, false)
		a.text("▼", x+a.px(18)+tw, y+(h-al)/2, 8, false, colMuted)
	}
	if fn != nil {
		a.clipHit(x, y, w, h, func(bool) { fn() })
	}
	return w
}

// field is an editable line of text.
type field struct {
	text        string
	cur         int // caret, a byte offset
	placeholder string
	onEnter     func()
	onChange    func()
	nav         bool // Up, Down, Left, Right, Home and End move a list, not the caret (the search field)
}

func (f *field) set(s string) { f.text, f.cur = s, len(s) }

func (f *field) insert(s string) {
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return -1
		}
		return r
	}, s)
	f.text = f.text[:f.cur] + s + f.text[f.cur:]
	f.cur += len(s)
	if f.onChange != nil {
		f.onChange()
	}
}

// key handles an editing key, reporting whether it was one.
func (f *field) key(sc int32, ctrl bool) bool {
	changed := false
	switch sc {
	case scBackspace:
		if f.cur > 0 {
			if ctrl {
				f.text, f.cur = f.text[f.cur:], 0
			} else {
				_, n := utf8.DecodeLastRuneInString(f.text[:f.cur])
				f.text = f.text[:f.cur-n] + f.text[f.cur:]
				f.cur -= n
			}
			changed = true
		}
	case scDelete:
		if f.cur < len(f.text) {
			_, n := utf8.DecodeRuneInString(f.text[f.cur:])
			f.text = f.text[:f.cur] + f.text[f.cur+n:]
			changed = true
		}
	case scLeft:
		if f.nav {
			return false
		}
		if f.cur > 0 {
			_, n := utf8.DecodeLastRuneInString(f.text[:f.cur])
			f.cur -= n
		}
	case scRight:
		if f.nav {
			return false
		}
		if f.cur < len(f.text) {
			_, n := utf8.DecodeRuneInString(f.text[f.cur:])
			f.cur += n
		}
	case scHome:
		if f.nav {
			return false
		}
		f.cur = 0
	case scEnd:
		if f.nav {
			return false
		}
		f.cur = len(f.text)
	case scV:
		if !ctrl {
			return false
		}
		f.insert(sdl.Clipboard())
		return true
	case scReturn, scKPEnter:
		if f.onEnter == nil {
			return false
		}
		f.onEnter()
	default:
		return false
	}
	if changed && f.onChange != nil {
		f.onChange()
	}
	return true
}

// drawField draws f in a box and makes a click focus it.
func (a *app) drawField(f *field, x, y, w int32) {
	h := a.px(32)
	focused := a.focus == f
	border := colLine
	if focused {
		border = colAccent
	}
	a.outline(x, y, w, h, a.px(6), a.px(1), border, colField)
	lh := a.lineHeight(13, false)
	ty := y + (h-lh)/2
	inner := w - a.px(24)
	a.pushClip(x+a.px(8), y, w-a.px(16), h)
	defer a.popClip()
	if f.text == "" {
		a.text(a.ellipsize(f.placeholder, inner, 13, false), x+a.px(12), ty, 13, false, colFaint)
		if focused {
			a.fill(x+a.px(12), ty, a.px(1.5), lh, colAccent)
		}
	} else {
		// Scrolled so the caret stays in view.
		cx := a.textWidth(f.text[:f.cur], 13, false)
		shift := int32(0)
		if focused {
			shift = max(0, cx-inner)
		}
		tx := x + a.px(12) - shift
		a.text(f.text, tx, ty, 13, false, colText)
		if focused {
			a.fill(tx+cx, ty, a.px(1.5), lh, colAccent)
		}
	}
	a.clipHit(x, y, w, h, func(bool) {
		a.focus = f
		// The caret goes where the click was.
		rel := a.mx - (x + a.px(12))
		if focused {
			rel += max(0, a.textWidth(f.text[:f.cur], 13, false)-inner)
		}
		f.cur = len(f.text)
		for i := range f.text {
			if a.textWidth(f.text[:i], 13, false) >= rel {
				f.cur = i
				break
			}
		}
	})
}

func (a *app) blur() {
	if a.focus != nil && a.focus != &a.search {
		a.focus = nil
	}
	if a.page == pageGames {
		a.focus = &a.search
	}
}

// popup is an open dropdown list.
type popup struct {
	x, y, w int32
	items   []string
	current int
	pick    func(i int)
}

// openPopup opens a dropdown under a button at x, y.
func (a *app) openPopup(x, y, w int32, items []string, current int, pick func(int)) {
	a.popup = &popup{x: x, y: y + a.px(36), w: w, items: items, current: current, pick: pick}
}

// openMenu opens a context menu at x, y: items with no current one.
func (a *app) openMenu(x, y int32, items []string, pick func(int)) {
	a.popup = &popup{x: x, y: y, w: a.px(200), items: items, current: -1, pick: pick}
}

// rightClick selects what's under the mouse, then opens its menu.
func (a *app) rightClick() {
	a.popup = nil
	a.menuAt = nil
	a.click(false)
	if a.menuAt != nil {
		a.menuAt()
	}
}

func (a *app) drawPopup() {
	p := a.popup
	if p == nil {
		return
	}
	a.layer = layerPopup
	a.onClick(0, 0, a.w, a.h, func(bool) { a.popup = nil }) // outside closes
	sep := 0
	for _, it := range p.items {
		if it == "-" {
			sep++
		}
	}
	ih := a.px(30)
	w := p.w
	for _, it := range p.items {
		label, keys, _ := strings.Cut(it, "\t")
		w = max(w, a.textWidth(label, 13, false)+a.textWidth(keys, 12, false)+a.px(72))
	}
	h := ih*int32(len(p.items)-sep) + int32(sep)*a.px(9) + a.px(8)
	x, y := min(p.x, a.w-w-a.px(8)), p.y
	if y+h > a.h {
		y = max(a.px(8), a.h-h-a.px(8))
	}
	a.round(x-a.px(1), y-a.px(1), w+a.px(2), h+a.px(2), a.px(8), colLine)
	a.round(x, y, w, h, a.px(8), colPopup)
	lh := a.lineHeight(13, false)
	iy := y + a.px(4)
	for i, it := range p.items {
		if it == "-" {
			a.fill(x+a.px(10), iy+a.px(4), w-a.px(20), a.px(1), colLine)
			iy += a.px(9)
			continue
		}
		if a.hovered(x, iy, w, ih) {
			a.round(x+a.px(4), iy, w-a.px(8), ih, a.px(5), colCardSel)
		}
		c := colText
		if i == p.current {
			a.text("•", x+a.px(10), iy+(ih-lh)/2, 13, false, colAccent)
		}
		label, keys, _ := strings.Cut(it, "\t")
		a.text(label, x+a.px(28), iy+(ih-lh)/2, 13, false, c)
		if keys != "" {
			a.textRight(keys, x+w-a.px(14), iy+(ih-lh)/2, 12, false, colFaint)
		}
		a.onClick(x, iy, w, ih, func(bool) {
			a.popup = nil
			p.pick(i)
		})
		iy += ih
	}
	a.layer = layerPage
}

// SDL scancodes.
const (
	scA         = 4
	scG         = 10
	scN         = 17
	scQ         = 20
	scO         = 18
	scB         = 5
	scL         = 15
	scR         = 21
	scV         = 25
	sc1         = 30
	sc2         = 31
	sc3         = 32
	scReturn    = 40
	scEscape    = 41
	scBackspace = 42
	scTab       = 43
	scComma     = 54
	scF5        = 62
	scHome      = 74
	scPageUp    = 75
	scDelete    = 76
	scEnd       = 77
	scPageDown  = 78
	scRight     = 79
	scLeft      = 80
	scDown      = 81
	scUp        = 82
	scKPEnter   = 88
)
