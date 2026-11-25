package builtin

import "testing"

func TestPEVersion(t *testing.T) {
	fixed := func(ms, ls uint32) []byte {
		b := []byte("....padding....\xBD\x04\xEF\xFE\x00\x00\x01\x00")
		for _, v := range []uint32{ms, ls} {
			b = append(b, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
		}
		return append(b, make([]byte, 32)...)
	}
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{"SDL3", fixed(3<<16|4, 8<<16), "3.4.8"},
		{"sdl2-compat", fixed(2<<16|32, 64<<16), "2.32.64"},
		{"none", []byte("MZ no version resource"), ""},
		{"truncated", []byte("\xBD\x04\xEF\xFE\x00\x00"), ""},
	}
	for _, tt := range tests {
		if got := peVersion(tt.data); got != tt.want {
			t.Errorf("%s: peVersion = %q, want %q", tt.name, got, tt.want)
		}
	}
	for _, p := range Programs() {
		if p.Name == "" || p.URL == "" {
			t.Errorf("program %+v: missing name or URL", p)
		}
	}
}
