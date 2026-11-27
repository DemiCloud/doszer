package gui

import (
	"testing"

	"golang.org/x/image/font"
)

func TestTextSelection(t *testing.T) {
	a := &app{scale: 1, faces: map[faceKey]font.Face{}, selText: map[string][]string{
		"out":  {"hello world", "second line", "third"},
		"rows": {"Ctrl+C  Copy", "F5  Refresh"},
	}}
	mono := faceKey{px: 12, mono: true}
	cw := a.monoWidth()
	const lh = 16
	for i, l := range a.selText["out"] {
		a.selHits = append(a.selHits, selHit{region: "out", idx: i, x: 0, y: int32(i) * lh, w: 400, h: lh, text: l, tx: 10, face: mono})
	}
	for i := range a.selText["rows"] {
		a.selHits = append(a.selHits, selHit{region: "rows", idx: i, x: 0, y: 100 + int32(i)*lh, w: 400, h: lh})
	}
	at := func(line, col int) (int32, int32) { return 10 + int32(col)*cw, int32(line)*lh + 4 }
	tests := []struct {
		name   string
		down   [2]int // line, column pressed
		clicks uint8
		drag   []int32 // mouse x, y moved to
		want   string
	}{
		{"click", [2]int{0, 3}, 1, nil, ""},
		{"drag in a line", [2]int{0, 0}, 1, []int32{10 + 5*cw, 4}, "hello"},
		{"drag backwards", [2]int{0, 11}, 1, []int32{10 + 6*cw, 4}, "world"},
		{"drag across lines", [2]int{0, 6}, 1, []int32{10 + 6*cw, lh + 4}, "world\nsecond"},
		{"drag below the last line", [2]int{1, 7}, 1, []int32{20, 90}, "line\nthird"},
		{"double click", [2]int{1, 2}, 2, nil, "second"},
		{"triple click", [2]int{1, 2}, 3, nil, "second line"},
		{"table row", [2]int{0, 0}, 1, nil, ""}, // rows: below
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a.mx, a.my = at(tt.down[0], tt.down[1])
			if tt.name == "table row" {
				a.my = 100 + 4
				a.selStart(a.selAt(), 1)
				a.my = 100 + lh + 4
				a.selDrag()
				tt.want = "Ctrl+C  Copy\nF5  Refresh"
			} else {
				a.selStart(a.selAt(), tt.clicks)
				if tt.drag != nil {
					a.mx, a.my = tt.drag[0], tt.drag[1]
					a.selDrag()
				}
			}
			if got := a.selected_(); got != tt.want {
				t.Errorf("selected %q, want %q", got, tt.want)
			}
		})
	}
}

// TestSelectionSeparators: a region built with selAdd copies a wrapped
// line as one line and a label's value a tab after it.
func TestSelectionSeparators(t *testing.T) {
	a := &app{scale: 1, faces: map[faceKey]font.Face{}, selText: map[string][]string{}, selSeps: map[string][]string{}}
	cmd := "doszer build --format plain --flatten 'C:\\path\\to\\sources\\Example Game' 'C:\\path\\to\\roms\\Example Game.dosz'"
	for i, l := range a.wrapMono(cmd, 300) {
		sep := sepWrap
		if i == 0 {
			sep = sepLine
		}
		a.selAdd("r", l, sep)
	}
	a.selAdd("r", "Source", sepLine)
	for i, l := range a.wrapKeep(`C:\path\to\sources\Example Game with a long tail of words`, 120, 13, false) {
		sep := sepWrap
		if i == 0 {
			sep = sepColumn
		}
		a.selAdd("r", l, sep)
	}
	if n := len(a.selText["r"]); n < 5 {
		t.Fatalf("expected wrapping, got %d lines: %q", n, a.selText["r"])
	}
	want := cmd + "\nSource\t" + `C:\path\to\sources\Example Game with a long tail of words`
	if got := a.regionText("r"); got != want {
		t.Errorf("region text\n%q\nwant\n%q", got, want)
	}
	last := len(a.selText["r"]) - 1
	a.tsel = textSel{region: "r", active: true, from: selPos{0, 0}, to: selPos{last, a.lineLen("r", last)}}
	if got := a.selected_(); got != want {
		t.Errorf("selected\n%q\nwant\n%q", got, want)
	}
}
