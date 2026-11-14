// Package padmap encodes [[input.pad]] gamepad mappings (spec §5.9) as
// DOSBox Pure's PADMAP.DBP (§6.8.2) and DOS.YML input lines (§6.8.3).
//
// The encoding follows DOSBox Pure 1.0 Preview 8: dosbox_pure_pad.h,
// DBP_PadMapping::Save/Load/Apply and BindDecoder (PADMAP.DBP),
// DBP_PadMapping::ParseInputYML (DOS.YML), and dosbox_pure_libretro.cpp's
// DBP_YMLKeyCommands and DBP_SpecialMappings (action names and codes).
package padmap

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
)

// MaxPads is the number of controller ports DOSBox Pure maps
// (DBP_MAX_PORTS in dosbox_pure_libretro.cpp).
const MaxPads = 8

// MaxWheel is the number of action wheel items a pad may have: DOS.YML
// numbers them input_wheel_1 to input_wheel_100 (ParseInputYML).
const MaxWheel = 100

// maxActions is the number of actions a binding may combine (ParseInputYML,
// FillBinds).
const maxActions = 4

// Controls are the [[input.pad]] control keys, in DOS.YML's own order
// (ParseInputYML's padnames). Index i < 16 is the RetroPad button ID; the
// stick directions pair up into IDs 16-19.
var Controls = []string{
	"b", "y", "select", "start", "up", "down", "left", "right",
	"a", "x", "l", "r", "l2", "r2", "l3", "r3",
	"lstick_left", "lstick_right", "lstick_up", "lstick_down",
	"rstick_left", "rstick_right", "rstick_up", "rstick_down",
}

// wheelID is a wheel item's control ID (DBP_PadMapping::WHEEL_ID).
const wheelID = 20

// keyNames are the keyboard actions by code: DBP_YMLKeyCommands, indices 1
// to KBD_LAST-1 of DOSBox's KBD_KEYS (include/keyboard.h).
var keyNames = []string{
	"",
	"1", "2", "3", "4", "5", "6", "7", "8", "9", "0",
	"q", "w", "e", "r", "t", "y", "u", "i", "o", "p",
	"a", "s", "d", "f", "g", "h", "j", "k", "l", "z",
	"x", "c", "v", "b", "n", "m",
	"f1", "f2", "f3", "f4", "f5", "f6", "f7", "f8", "f9", "f10", "f11", "f12",
	"esc", "tab", "backspace", "enter", "space",
	"leftalt", "rightalt", "leftctrl", "rightctrl", "leftshift", "rightshift",
	"capslock", "scrolllock", "numlock",
	"grave", "minus", "equals", "backslash", "leftbracket", "rightbracket",
	"semicolon", "quote", "period", "comma", "slash", "extra_lt_gt",
	"printscreen", "pause",
	"insert", "home", "pageup", "delete", "end", "pagedown",
	"left", "up", "down", "right",
	"kp1", "kp2", "kp3", "kp4", "kp5", "kp6", "kp7", "kp8", "kp9", "kp0",
	"kpdivide", "kpmultiply", "kpminus", "kpplus", "kpenter", "kpperiod",
}

// specialBase is the first special action's code (DBP_SPECIALMAPPINGS_KEY).
const specialBase = 200

// specialNames are the special actions by code - specialBase, in
// DBP_SpecialMappings order. DOS.YML names those with a ymlid; menu and
// hold_port1-4 have none, so they're this specification's names, and a
// binding using them can't be written to DOS.YML (yml false).
var specialNames = []struct {
	name string
	yml  bool
}{
	{"mouse_move_up", true}, {"mouse_move_down", true}, {"mouse_move_left", true}, {"mouse_move_right", true},
	{"mouse_left_click", true}, {"mouse_right_click", true}, {"mouse_middle_click", true},
	{"mouse_speed_up", true}, {"mouse_speed_down", true},
	{"joy_up", true}, {"joy_down", true}, {"joy_left", true}, {"joy_right", true},
	{"joy_button1", true}, {"joy_button2", true}, {"joy_button3", true}, {"joy_button4", true},
	{"joy_hat_up", true}, {"joy_hat_down", true}, {"joy_hat_left", true}, {"joy_hat_right", true},
	{"joy_2_up", true}, {"joy_2_down", true}, {"joy_2_left", true}, {"joy_2_right", true},
	// 225 DBPET_TOGGLEOSD, 226 DBPET_ACTIONWHEEL, 227-230 DBPET_SHIFTPORT
	{"menu", false}, {"wheel", true},
	{"hold_port1", false}, {"hold_port2", false}, {"hold_port3", false}, {"hold_port4", false},
}

// wheelCode is the code of the wheel action.
const wheelCode = 226

var codes = func() map[string]byte {
	m := make(map[string]byte, len(keyNames)+len(specialNames))
	for i, n := range keyNames[1:] {
		m[n] = byte(i + 1)
	}
	for i, s := range specialNames {
		m[s.name] = byte(specialBase + i)
	}
	return m
}()

// name returns an action code's canonical name.
func name(code byte) string {
	if int(code) < len(keyNames) {
		return keyNames[code]
	}
	return specialNames[code-specialBase].name
}

// inYML reports whether an action can be written to DOS.YML.
func inYML(code byte) bool {
	return int(code) < len(keyNames) || specialNames[code-specialBase].yml
}

// Binding is a parsed binding string (§5.9).
type Binding struct {
	Actions []byte // action codes, in order
	Label   string
}

// ParseBinding parses a binding string: one to four action names joined by
// "+", optionally followed by a space and a label.
func ParseBinding(s string) (Binding, error) {
	keys, label, _ := strings.Cut(strings.Trim(s, " "), " ")
	label = strings.Trim(label, " ")

	var b Binding
	if keys == "" {
		return b, errors.New("must name at least one action")
	}
	names := strings.Split(keys, "+")
	if len(names) > maxActions {
		return b, fmt.Errorf("at most %d actions, got %d", maxActions, len(names))
	}
	for _, n := range names {
		code, ok := codes[strings.ToLower(n)]
		if !ok {
			return b, fmt.Errorf("unknown action %q", n)
		}
		b.Actions = append(b.Actions, code)
	}

	for _, r := range label {
		if r < 0x20 || r == 0x7f {
			return b, errors.New("label must not contain control characters")
		}
	}
	if strings.Contains(label, " #") {
		return b, errors.New(`label must not contain " #"`)
	}
	b.Label = label
	return b, nil
}

// Pad is one [[input.pad]] entry: control key to binding string, and the
// wheel items.
type Pad struct {
	Controls map[string]string
	Wheel    []string
}

// Validate checks a pad's control names and binding strings (§11).
func (p Pad) Validate() []error {
	var errs []error
	known := 0
	for _, c := range Controls {
		s, ok := p.Controls[c]
		if !ok {
			continue
		}
		known++
		if _, err := ParseBinding(s); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", c, err))
		}
	}
	if known != len(p.Controls) {
		errs = append(errs, errors.New("unknown control"))
	}
	if len(p.Wheel) > MaxWheel {
		errs = append(errs, fmt.Errorf("wheel: at most %d items, got %d", MaxWheel, len(p.Wheel)))
	}
	for i, w := range p.Wheel {
		b, err := ParseBinding(w)
		if err != nil {
			errs = append(errs, fmt.Errorf("wheel[%d]: %w", i, err))
			continue
		}
		if bytes.IndexByte(b.Actions, wheelCode) >= 0 {
			errs = append(errs, fmt.Errorf("wheel[%d]: must not contain the action \"wheel\"", i))
		}
	}
	return errs
}

// binding parses a control's binding, or returns nil if it is unset.
// Pads are validated before encoding, so a parse error is a bug.
func (p Pad) binding(control string) *Binding {
	s, ok := p.Controls[control]
	if !ok {
		return nil
	}
	b, err := ParseBinding(s)
	if err != nil {
		panic(fmt.Sprintf("padmap: unvalidated binding %s = %q: %v", control, s, err))
	}
	return &b
}

// EncodePADMAP renders PADMAP.DBP (§6.8.2) for pads, which must have been
// validated.
func EncodePADMAP(pads []Pad) []byte {
	out := []byte{0} // version (DBP_PadMapping::Load)
	for _, p := range pads {
		var entries [][]byte
		for id := 0; id < 16; id++ {
			if b := p.binding(Controls[id]); b != nil {
				entries = append(entries, entry(id, len(b.Actions), b.Actions))
			}
		}
		for id := 16; id < wheelID; id++ {
			neg := p.binding(Controls[16+(id-16)*2])
			pos := p.binding(Controls[16+(id-16)*2+1])
			if neg == nil && pos == nil {
				continue
			}
			n := max(count(neg), count(pos))
			keys := make([]byte, 2*n)
			for i := 0; i < n; i++ {
				if neg != nil && i < len(neg.Actions) {
					keys[2*i] = neg.Actions[i]
				}
				if pos != nil && i < len(pos.Actions) {
					keys[2*i+1] = pos.Actions[i]
				}
			}
			entries = append(entries, entry(id, n, keys))
		}
		for _, w := range p.Wheel {
			b, err := ParseBinding(w)
			if err != nil {
				panic(fmt.Sprintf("padmap: unvalidated wheel item %q: %v", w, err))
			}
			entries = append(entries, entry(wheelID, len(b.Actions), b.Actions))
		}

		out = append(out, byte(len(entries)))
		for _, e := range entries {
			out = append(out, e...)
		}
	}
	return out
}

func count(b *Binding) int {
	if b == nil {
		return 0
	}
	return len(b.Actions)
}

func entry(id, n int, keys []byte) []byte {
	return append([]byte{byte(id | (n-1)<<6)}, keys...)
}

// YMLLines returns pad's DOS.YML lines (§6.8.3), without line endings:
// every binding whose actions DOS.YML can express. pad must have been
// validated.
func YMLLines(pad Pad) []string {
	var lines []string
	// §6.8.2's control ID order: Controls already lists buttons 0-15, then
	// each stick axis's negative direction before its positive one.
	for _, c := range Controls {
		if b := pad.binding(c); b != nil {
			if l, ok := ymlValue(*b); ok {
				lines = append(lines, "input_pad_"+c+": "+l)
			}
		}
	}
	n := 0
	for _, w := range pad.Wheel {
		b, _ := ParseBinding(w)
		if l, ok := ymlValue(b); ok {
			n++
			lines = append(lines, fmt.Sprintf("input_wheel_%d: %s", n, l))
		}
	}
	return lines
}

func ymlValue(b Binding) (string, bool) {
	names := make([]string, len(b.Actions))
	for i, c := range b.Actions {
		if !inYML(c) {
			return "", false
		}
		names[i] = name(c)
	}
	v := strings.Join(names, "+")
	if b.Label != "" {
		v += " " + b.Label
	}
	return v, true
}

// IsInputLine reports whether a DOS.YML line sets an input mapping key,
// which §6.8.3 replaces.
func IsInputLine(line string) bool {
	return strings.HasPrefix(line, "input_pad_") || strings.HasPrefix(line, "input_wheel_")
}

// MergeDOSYML renders DOS.YML (§6.8.3): existing's lines (nil if the build
// has no DOS.YML) without its input mapping lines, then lines, each ending
// with CRLF.
func MergeDOSYML(existing []byte, lines []string) []byte {
	var b bytes.Buffer
	for _, l := range splitLines(existing) {
		if !IsInputLine(l) {
			b.WriteString(l + "\r\n")
		}
	}
	for _, l := range lines {
		b.WriteString(l + "\r\n")
	}
	return b.Bytes()
}

// splitLines splits on CRLF, LF or CR; a final line ending doesn't start
// another line.
func splitLines(data []byte) []string {
	s := strings.ReplaceAll(string(data), "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.TrimSuffix(s, "\n")
	if s == "" && len(data) == 0 {
		return nil
	}
	return strings.Split(s, "\n")
}
