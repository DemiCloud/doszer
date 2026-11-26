package gui

import (
	"strings"
	"unicode/utf8"

	"github.com/DemiCloud/doszer/internal/sdl"
)

// textArea is an editable multi-line text in the monospaced face: a
// manifest, a batch file.
type textArea struct {
	lines    []string
	row, col int // the caret: a line, and a byte offset in it
	top      int // the first line shown
	left     int // the first column shown (runes)
	changed  bool
}

func newTextArea(s string) *textArea {
	t := &textArea{}
	t.set(s)
	return t
}

func (t *textArea) set(s string) {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
	t.lines = strings.Split(strings.ReplaceAll(s, "\t", "    "), "\n")
	t.row, t.col, t.top, t.left, t.changed = 0, 0, 0, 0, false
}

func (t *textArea) text() string { return strings.Join(t.lines, "\n") }

// insert types s at the caret; newlines split the line.
func (t *textArea) insert(s string) {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
	s = strings.ReplaceAll(s, "\t", "    ")
	parts := strings.Split(s, "\n")
	line := t.lines[t.row]
	head, tail := line[:t.col], line[t.col:]
	if len(parts) == 1 {
		t.lines[t.row] = head + s + tail
		t.col += len(s)
	} else {
		ins := make([]string, len(parts))
		ins[0] = head + parts[0]
		copy(ins[1:], parts[1:])
		ins[len(ins)-1] += tail
		t.lines = append(t.lines[:t.row], append(ins, t.lines[t.row+1:]...)...)
		t.row += len(parts) - 1
		t.col = len(parts[len(parts)-1])
	}
	t.changed = true
}

// key handles an editing or moving key; page is the lines shown.
func (t *textArea) key(sc int32, ctrl bool, page int) bool {
	line := t.lines[t.row]
	clampCol := func() { t.col = min(t.col, len(t.lines[t.row])) }
	switch sc {
	case scLeft:
		switch {
		case t.col > 0:
			_, n := utf8.DecodeLastRuneInString(line[:t.col])
			t.col -= n
		case t.row > 0:
			t.row--
			t.col = len(t.lines[t.row])
		}
	case scRight:
		switch {
		case t.col < len(line):
			_, n := utf8.DecodeRuneInString(line[t.col:])
			t.col += n
		case t.row < len(t.lines)-1:
			t.row, t.col = t.row+1, 0
		}
	case scUp:
		if t.row > 0 {
			t.row--
			clampCol()
		}
	case scDown:
		if t.row < len(t.lines)-1 {
			t.row++
			clampCol()
		}
	case scPageUp:
		t.row = max(0, t.row-page)
		clampCol()
	case scPageDown:
		t.row = min(len(t.lines)-1, t.row+page)
		clampCol()
	case scHome:
		if ctrl {
			t.row = 0
		}
		t.col = 0
	case scEnd:
		if ctrl {
			t.row = len(t.lines) - 1
		}
		t.col = len(t.lines[t.row])
	case scReturn, scKPEnter:
		// Keep the line's indentation.
		indent := line[:len(line)-len(strings.TrimLeft(line, " "))]
		t.insert("\n" + indent[:min(len(indent), t.col)])
	case scTab:
		t.insert("    ")
	case scBackspace:
		switch {
		case t.col > 0:
			_, n := utf8.DecodeLastRuneInString(line[:t.col])
			t.lines[t.row] = line[:t.col-n] + line[t.col:]
			t.col -= n
		case t.row > 0:
			prev := t.lines[t.row-1]
			t.lines[t.row-1] = prev + line
			t.lines = append(t.lines[:t.row], t.lines[t.row+1:]...)
			t.row--
			t.col = len(prev)
		}
		t.changed = true
	case scDelete:
		switch {
		case t.col < len(line):
			_, n := utf8.DecodeRuneInString(line[t.col:])
			t.lines[t.row] = line[:t.col] + line[t.col+n:]
		case t.row < len(t.lines)-1:
			t.lines[t.row] = line + t.lines[t.row+1]
			t.lines = append(t.lines[:t.row+1], t.lines[t.row+2:]...)
		}
		t.changed = true
	case scV:
		if !ctrl {
			return false
		}
		t.insert(sdl.Clipboard())
	default:
		return false
	}
	return true
}

// drawTextArea draws t in a box, keeping the caret in view, and places
// the caret where a click lands.
func (a *app) drawTextArea(t *textArea, x, y, w, h int32, focused bool) {
	a.round(x, y, w, h, a.px(6), colBackground)
	if focused {
		a.outline(x, y, w, h, a.px(6), a.px(1), colAccent, colBackground)
	}
	pad := a.px(10)
	lh := a.lineHeight(12, false) + a.px(3)
	cw := a.monoWidth()
	rows := max(1, int((h-2*pad)/lh))
	cols := max(10, int((w-2*pad-a.px(36))/cw))
	// Follow the caret.
	if t.row < t.top {
		t.top = t.row
	}
	if t.row >= t.top+rows {
		t.top = t.row - rows + 1
	}
	t.top = max(0, min(t.top, len(t.lines)-1))
	ccol := utf8.RuneCountInString(t.lines[t.row][:t.col])
	if ccol < t.left {
		t.left = ccol
	}
	if ccol >= t.left+cols {
		t.left = ccol - cols + 1
	}
	gutter := a.px(36) // line numbers
	tx := x + pad + gutter
	a.pushClip(x, y, w, h)
	ly := y + pad
	for i := t.top; i < len(t.lines) && i < t.top+rows; i++ {
		n := itoa(i + 1)
		a.monoText(n, tx-a.px(10)-cw*int32(len(n)), ly, colFaint)
		r := []rune(t.lines[i])
		if t.left < len(r) {
			a.monoText(string(r[t.left:]), tx, ly, colText)
		}
		if focused && i == t.row {
			a.fill(tx+cw*int32(ccol-t.left), ly, a.px(1.5), lh-a.px(2), colAccent)
		}
		ly += lh
	}
	a.popClip()
	a.scrollbar(x+w-a.px(8), y+a.px(4), h-a.px(8), t.top, rows, len(t.lines))
	a.clipHit(x, y, w, h, func(bool) {
		a.focusArea, a.focus = t, nil
		row := t.top + int((a.my-y-pad)/lh)
		t.row = max(0, min(row, len(t.lines)-1))
		col := t.left + int((a.mx-tx+cw/2)/cw)
		r := []rune(t.lines[t.row])
		col = max(0, min(col, len(r)))
		t.col = len(string(r[:col]))
	})
	if a.hovered(x, y, w, h) {
		a.areaUnderMouse = t
	}
}
