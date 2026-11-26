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
