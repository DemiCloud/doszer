package padmap

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestParseBinding(t *testing.T) {
	tests := []struct {
		in      string
		actions []byte
		label   string
		err     string
	}{
		{in: "space", actions: []byte{53}},
		{in: "space Fire", actions: []byte{53}, label: "Fire"},
		{in: "  LeftCtrl+X   Use Item  ", actions: []byte{56, 31}, label: "Use Item"},
		{in: "1+0+f12+kpperiod", actions: []byte{1, 10, 48, 102}},
		{in: "mouse_move_up+joy_2_right", actions: []byte{200, 224}},
		{in: "menu", actions: []byte{225}},
		{in: "wheel Actions", actions: []byte{226}, label: "Actions"},
		{in: "hold_port1+hold_port4", actions: []byte{227, 230}},
		{in: "", err: "at least one action"},
		{in: "a+b+c+d+e", err: "at most 4"},
		{in: "space+", err: `unknown action ""`},
		{in: "wait", err: `unknown action "wait"`}, // autoinput only, not a key
		{in: "none", err: `unknown action "none"`},
		{in: "space Fire # comment", err: `" #"`},
		{in: "space Fi\tre", err: "control characters"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			b, err := ParseBinding(tt.in)
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("err = %v, want containing %q", err, tt.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(b.Actions, tt.actions) || b.Label != tt.label {
				t.Errorf("got %v %q, want %v %q", b.Actions, b.Label, tt.actions, tt.label)
			}
		})
	}
}

// Codes must match DOSBox Pure's tables: KBD_KEYS has 102 keys before
// KBD_LAST, DBP_SpecialMappings 31 entries from 200.
func TestCodeTables(t *testing.T) {
	if n := len(keyNames) - 1; n != 102 {
		t.Errorf("%d keyboard actions, want 102", n)
	}
	if n := len(specialNames); n != 31 {
		t.Errorf("%d special actions, want 31", n)
	}
	for name, code := range map[string]byte{"esc": 49, "extra_lt_gt": 74, "right": 86, "mouse_speed_down": 208, "joy_button1": 213, "joy_hat_up": 217, "menu": 225, "wheel": wheelCode, "hold_port1": 227} {
		if got := codes[name]; got != code {
			t.Errorf("code(%s) = %d, want %d", name, got, code)
		}
	}
}

func TestEncodePADMAP(t *testing.T) {
	tests := []struct {
		name string
		pads []Pad
		want []byte
	}{
		{"one empty pad", []Pad{{}}, []byte{0, 0}},
		{
			"buttons in ID order, not map order",
			[]Pad{{Controls: map[string]string{"a": "leftctrl+enter Fire", "b": "space"}}},
			// b is ID 0, a is ID 8 with two actions: 8 | 1<<6 = 72.
			[]byte{0, 2, 0, 53, 72, 56, 52},
		},
		{
			"stick axis pairs negative and positive",
			[]Pad{{Controls: map[string]string{"lstick_left": "left+a", "lstick_right": "right", "rstick_down": "mouse_move_down"}}},
			// ID 16, n=2: (left,right) (a,0). ID 19, n=1: (0,mouse_move_down).
			[]byte{0, 2, 16 | 1<<6, 83, 86, 21, 0, 19, 0, 201},
		},
		{
			"wheel items after controls",
			[]Pad{{Controls: map[string]string{"r": "wheel"}, Wheel: []string{"f1 Help", "f10+y Quit"}}},
			[]byte{0, 3, 11, 226, 20, 37, 20 | 1<<6, 46, 16},
		},
		{
			"second pad follows the first",
			[]Pad{{Controls: map[string]string{"start": "enter"}}, {Controls: map[string]string{"l3": "menu"}}},
			[]byte{0, 1, 3, 52, 1, 14, 225},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EncodePADMAP(tt.pads); !bytes.Equal(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestYMLLines(t *testing.T) {
	pad := Pad{
		Controls: map[string]string{
			"lstick_right": "RIGHT",
			"lstick_left":  "left Move",
			"a":            "space Fire",
			"b":            "leftctrl+X Use Item",
			"l3":           "menu",            // not expressible
			"r":            "hold_port2+f1 X", // not expressible
			"select":       "wheel",
		},
		Wheel: []string{"f1 Help", "menu", "f2"},
	}
	want := []string{
		"input_pad_b: leftctrl+x Use Item",
		"input_pad_select: wheel",
		"input_pad_a: space Fire",
		"input_pad_lstick_left: left Move",
		"input_pad_lstick_right: right",
		"input_wheel_1: f1 Help",
		"input_wheel_2: f2",
	}
	if got := YMLLines(pad); !reflect.DeepEqual(got, want) {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}
}

func TestMergeDOSYML(t *testing.T) {
	lines := []string{"input_pad_b: space Fire"}
	tests := []struct {
		name     string
		existing string
		want     string
	}{
		{"no DOS.YML", "", "input_pad_b: space Fire\r\n"},
		{
			"keeps other lines, drops input lines",
			"cpu_cycles: 20000\ninput_pad_a: enter\r\n# comment\rinput_wheel_1: f1\nsound_card: sb16",
			"cpu_cycles: 20000\r\n# comment\r\nsound_card: sb16\r\ninput_pad_b: space Fire\r\n",
		},
		{"keeps empty lines", "a: 1\n\nb: 2\n", "a: 1\r\n\r\nb: 2\r\ninput_pad_b: space Fire\r\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var existing []byte
			if tt.existing != "" {
				existing = []byte(tt.existing)
			}
			got := MergeDOSYML(existing, lines)
			if string(got) != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
			if again := MergeDOSYML(got, lines); !bytes.Equal(again, got) {
				t.Errorf("not idempotent: %q", again)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	wheel := make([]string, MaxWheel+1)
	for i := range wheel {
		wheel[i] = "f1"
	}
	tests := []struct {
		name string
		pad  Pad
		errs []string
	}{
		{"valid", Pad{Controls: map[string]string{"b": "space"}, Wheel: []string{"f1"}}, nil},
		{"bad binding", Pad{Controls: map[string]string{"b": "spcae"}}, []string{`b: unknown action "spcae"`}},
		{"wheel in wheel", Pad{Wheel: []string{"f1", "wheel"}}, []string{`wheel[1]: must not contain`}},
		{"too many wheel items", Pad{Wheel: wheel}, []string{"wheel: at most 100"}},
		{"unknown control", Pad{Controls: map[string]string{"z": "space"}}, []string{"unknown control"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := tt.pad.Validate()
			if len(errs) != len(tt.errs) {
				t.Fatalf("errs = %v, want %d", errs, len(tt.errs))
			}
			for i, e := range errs {
				if !strings.Contains(e.Error(), tt.errs[i]) {
					t.Errorf("err %d = %v, want containing %q", i, e, tt.errs[i])
				}
			}
		})
	}
}
