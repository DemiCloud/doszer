package main

import "testing"

func TestWSLToWindows(t *testing.T) {
	tests := []struct{ in, want string }{
		{"/mnt/d/temp/roms/Recoil.dosz", `D:\temp\roms\Recoil.dosz`},
		{"/mnt/c", `C:\`},
		{"/mnt/c/", `C:\`},
		{"/mnt/wsl/x", "/mnt/wsl/x"},
		{"/home/user/x.dosz", "/home/user/x.dosz"},
		{"Recoil.dosz", "Recoil.dosz"},
		{`D:\temp`, `D:\temp`},
	}
	for _, tt := range tests {
		if got := wslToWindows(tt.in); got != tt.want {
			t.Errorf("wslToWindows(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
