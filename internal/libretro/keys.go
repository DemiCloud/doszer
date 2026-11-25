package libretro

// Key codes: libretro's RETROK_* (libretro.h, enum retro_key) from SDL's
// scancodes (SDL_scancode.h, the USB HID usage IDs), so DOS sees physical
// key positions whatever the host keyboard layout.
const (
	retrokReturn = 13
	retrokF5     = 286
	retrokF10    = 291
	retrokF12    = 293
	retrokPause  = 19
	retrokLast   = 324
)

var scancodeKeys = func() map[int32]uint32 {
	m := map[int32]uint32{
		40: 13, 41: 27, 42: 8, 43: 9, 44: 32, // return, escape, backspace, tab, space
		45: 45, 46: 61, 47: 91, 48: 93, 49: 92, 50: 92, // - = [ ] \ (non-US #)
		51: 59, 52: 39, 53: 96, 54: 44, 55: 46, 56: 47, // ; ' ` , . /
		57: 301, 70: 316, 71: 302, 72: 19, // caps lock, print, scroll lock, pause
		73: 277, 74: 278, 75: 280, 76: 127, 77: 279, 78: 281, // insert, home, page up, delete, end, page down
		79: 275, 80: 276, 81: 274, 82: 273, // right, left, down, up
		83: 300, 84: 267, 85: 268, 86: 269, 87: 270, 88: 271, // num lock, keypad / * - + enter
		98: 256, 99: 266, 100: 323, 101: 319, 103: 272, // keypad 0 ., OEM 102, menu, keypad =
		224: 306, 225: 304, 226: 308, 227: 311, // left ctrl, shift, alt, super
		228: 305, 229: 303, 230: 307, 231: 312, // right ctrl, shift, alt, super
	}
	for i := int32(0); i < 26; i++ {
		m[4+i] = 'a' + uint32(i) // A-Z
	}
	for i := int32(0); i < 9; i++ {
		m[30+i] = '1' + uint32(i) // 1-9
		m[89+i] = 257 + uint32(i) // keypad 1-9
	}
	m[39] = '0'
	for i := int32(0); i < 12; i++ {
		m[58+i] = 282 + uint32(i) // F1-F12
	}
	return m
}()

func retrokFromScancode(sc int32) uint32 { return scancodeKeys[sc] }
