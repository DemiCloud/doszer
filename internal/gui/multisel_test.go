package gui

import (
	"strings"
	"testing"

	"github.com/DemiCloud/doszer/internal/sdl"
)

func TestMultiSel(t *testing.T) {
	order := []string{"a", "b", "c", "d", "e"}
	type step struct {
		key   string
		mod   uint16
		right bool
		drag  string // then drag to this key
	}
	tests := []struct {
		name  string
		cur   string
		steps []step
		want  string
	}{
		{"click", "a", []step{{key: "c"}}, "c"},
		{"ctrl adds to the current", "a", []step{{key: "c", mod: sdl.KmodCtrl}}, "a,c"},
		{"ctrl removes", "", []step{{key: "a"}, {key: "c", mod: sdl.KmodCtrl}, {key: "a", mod: sdl.KmodCtrl}}, "c"},
		{"shift range", "", []step{{key: "b"}, {key: "d", mod: sdl.KmodShift}}, "b,c,d"},
		{"shift range backwards", "", []step{{key: "d"}, {key: "b", mod: sdl.KmodShift}}, "b,c,d"},
		{"ctrl+shift adds a range", "", []step{{key: "a"}, {key: "d", mod: sdl.KmodCtrl}, {key: "e", mod: sdl.KmodCtrl | sdl.KmodShift}}, "a,d,e"},
		{"drag", "", []step{{key: "b", drag: "e"}}, "b,c,d,e"},
		{"right click in the selection keeps it", "", []step{{key: "b", drag: "d"}, {key: "c", right: true}}, "b,c,d"},
		{"right click outside selects it", "", []step{{key: "b", drag: "d"}, {key: "e", right: true}}, "e"},
	}
	defer func(f func() uint16) { modState = f }(modState)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var m multiSel
			cur := tt.cur
			for _, s := range tt.steps {
				modState = func() uint16 { return s.mod }
				m.click(order, s.key, cur, s.right)
				if s.drag != "" {
					m.dragTo(order, s.drag)
					m.drag = false
				}
				cur = s.key
			}
			if got := strings.Join(m.selected(order, cur), ","); got != tt.want {
				t.Errorf("selected %s, want %s", got, tt.want)
			}
		})
	}
}
