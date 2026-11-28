package gamepad

import (
	"testing"

	"github.com/DemiCloud/doszer/internal/padmap"
)

// The player's RetroPad buttons and the editor's controls agree: the SDL
// button behind RetroPad ID i is the control [[input.pad]] lists at i
// (padmap.Controls is in RetroPad ID order), and A is the bottom button
// (the RetroPad's B).
func TestTablesAgree(t *testing.T) {
	for id, b := range retroButtons {
		want := padmap.Controls[id]
		if b < 0 {
			if want != "l2" && want != "r2" {
				t.Errorf("RetroPad %d (%s) has no SDL button", id, want)
			}
			continue
		}
		if got := controlButtons[uint8(b)]; got != want {
			t.Errorf("RetroPad %d: SDL button %d is control %q, want %q", id, b, got, want)
		}
	}
	if controlButtons[buttonA] != "b" || controlButtons[buttonB] != "a" {
		t.Error("face buttons not positional")
	}
	seen := map[string]bool{}
	for _, c := range controlButtons {
		seen[c] = true
	}
	for _, cs := range controlAxes {
		for _, c := range cs {
			if c != "" {
				seen[c] = true
			}
		}
	}
	for _, c := range padmap.Controls {
		if !seen[c] {
			t.Errorf("control %s: no button or axis gives it", c)
		}
	}
}
