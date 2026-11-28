package gui

// scancodeActions are DOSBox Pure's keyboard actions (spec §5.9) by the
// SDL scancode of the key that types them on a PC keyboard: what the
// gamepad editor's Press key adds.
var scancodeActions = func() map[int32]string {
	m := map[int32]string{
		40: "enter", 41: "esc", 42: "backspace", 43: "tab", 44: "space",
		45: "minus", 46: "equals", 47: "leftbracket", 48: "rightbracket", 49: "backslash",
		51: "semicolon", 52: "quote", 53: "grave", 54: "comma", 55: "period", 56: "slash",
		57: "capslock", 70: "printscreen", 71: "scrolllock", 72: "pause",
		73: "insert", 74: "home", 75: "pageup", 76: "delete", 77: "end", 78: "pagedown",
		79: "right", 80: "left", 81: "down", 82: "up",
		83: "numlock", 84: "kpdivide", 85: "kpmultiply", 86: "kpminus", 87: "kpplus", 88: "kpenter",
		98: "kp0", 99: "kpperiod", 100: "extra_lt_gt",
		224: "leftctrl", 225: "leftshift", 226: "leftalt", 228: "rightctrl", 229: "rightshift", 230: "rightalt",
	}
	for i := int32(0); i < 26; i++ {
		m[4+i] = string(rune('a' + i)) // SDL_SCANCODE_A..Z
	}
	for i := int32(0); i < 9; i++ {
		m[30+i] = string(rune('1' + i)) // SDL_SCANCODE_1..9
		m[89+i] = "kp" + string(rune('1'+i))
	}
	m[39] = "0"
	for i := int32(0); i < 12; i++ {
		m[58+i] = "f" + itoa(int(i+1)) // SDL_SCANCODE_F1..F12
	}
	return m
}()
