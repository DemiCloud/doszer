package padmap

import (
	"fmt"
	"strings"
)

// DecodePADMAP reads PADMAP.DBP - as EncodePADMAP writes it, or as DOSBox
// Pure's own mapper saves it (dosbox_pure_pad.h, DBP_PadMapping::Save:
// a version byte, 0, then per port up to the last customized one a
// binding count and bindings; FillBinds puts a stick axis's negative
// direction before its positive one, KBD_NONE, 0, for none) - into
// pads, without labels: those are DOS.YML's (Label).
func DecodePADMAP(data []byte) ([]Pad, error) {
	if len(data) < 2 || data[0] != 0 {
		return nil, fmt.Errorf("PADMAP.DBP: not version 0 gamepad mapping data")
	}
	var pads []Pad
	p := 1
	for p < len(data) {
		if len(pads) == MaxPads {
			return nil, fmt.Errorf("PADMAP.DBP: more than %d ports", MaxPads)
		}
		count := int(data[p])
		p++
		pad := Pad{Controls: map[string]string{}}
		for i := 0; i < count; i++ {
			if p >= len(data) {
				return nil, fmt.Errorf("PADMAP.DBP: port %d: cut short", len(pads)+1)
			}
			id, n := int(data[p]&63), int(data[p]>>6)+1
			p++
			width := 1
			if id >= 16 && id < wheelID {
				width = 2
			}
			if id > wheelID || p+n*width > len(data) {
				return nil, fmt.Errorf("PADMAP.DBP: port %d: binding %d: control %d, %d action(s) don't fit", len(pads)+1, i+1, id, n)
			}
			keys := data[p : p+n*width]
			p += n * width
			switch {
			case id < 16:
				s, err := names(keys)
				if err != nil {
					return nil, fmt.Errorf("PADMAP.DBP: port %d: %s: %w", len(pads)+1, Controls[id], err)
				}
				pad.Controls[Controls[id]] = s
			case id < wheelID:
				// An axis: each action a pair, negative direction first.
				var neg, pos []byte
				for j := 0; j < n; j++ {
					if keys[2*j] != 0 {
						neg = append(neg, keys[2*j])
					}
					if keys[2*j+1] != 0 {
						pos = append(pos, keys[2*j+1])
					}
				}
				for k, side := range [][]byte{neg, pos} {
					if len(side) == 0 {
						continue
					}
					c := Controls[16+(id-16)*2+k]
					s, err := names(side)
					if err != nil {
						return nil, fmt.Errorf("PADMAP.DBP: port %d: %s: %w", len(pads)+1, c, err)
					}
					pad.Controls[c] = s
				}
			default:
				s, err := names(keys)
				if err != nil {
					return nil, fmt.Errorf("PADMAP.DBP: port %d: wheel: %w", len(pads)+1, err)
				}
				pad.Wheel = append(pad.Wheel, s)
			}
		}
		pads = append(pads, pad)
	}
	return pads, nil
}

// names joins action codes' names with "+", as a binding string has them.
func names(keys []byte) (string, error) {
	var out []string
	for _, k := range keys {
		if k == 0 {
			continue
		}
		if !(int(k) < len(keyNames) || k >= specialBase && int(k-specialBase) < len(specialNames)) {
			return "", fmt.Errorf("unknown action code %d", k)
		}
		out = append(out, name(k))
	}
	if len(out) == 0 {
		return "", fmt.Errorf("no action")
	}
	return strings.Join(out, "+"), nil
}

// ParseDOSYML reads a DOS.YML's input lines (§6.8.3) as one pad: its
// controls and wheel items. Lines that aren't input lines, or whose
// binding doesn't parse, are left out.
func ParseDOSYML(data []byte) Pad {
	pad := Pad{Controls: map[string]string{}}
	for _, l := range splitLines(data) {
		k, v, ok := strings.Cut(l, ":")
		if !ok || !IsInputLine(l) {
			continue
		}
		v = strings.TrimSpace(v)
		if _, err := ParseBinding(v); err != nil {
			continue
		}
		if c, ok := strings.CutPrefix(k, "input_pad_"); ok {
			pad.Controls[c] = v
		} else {
			pad.Wheel = append(pad.Wheel, v)
		}
	}
	return pad
}

// Label gives pads' bindings the labels other pads give the same actions
// - as DOSBox Pure labels a binding from DOS.YML, by its exact actions on
// any port (dosbox_pure_pad.h, FindAutoMapButtonLabel) - keeping a label
// a binding already has.
func Label(pads []Pad, from ...Pad) []Pad {
	labels := map[string]string{}
	add := func(s string) {
		if b, err := ParseBinding(s); err == nil && b.Label != "" {
			if _, ok := labels[string(b.Actions)]; !ok {
				labels[string(b.Actions)] = b.Label
			}
		}
	}
	for _, f := range from {
		for _, s := range f.Controls {
			add(s)
		}
		for _, s := range f.Wheel {
			add(s)
		}
	}
	label := func(s string) string {
		b, err := ParseBinding(s)
		if err != nil || b.Label != "" {
			return s
		}
		if l := labels[string(b.Actions)]; l != "" {
			return s + " " + l
		}
		return s
	}
	out := make([]Pad, len(pads))
	for i, p := range pads {
		q := Pad{Controls: map[string]string{}}
		for c, s := range p.Controls {
			q.Controls[c] = label(s)
		}
		for _, s := range p.Wheel {
			q.Wheel = append(q.Wheel, label(s))
		}
		out[i] = q
	}
	return out
}
