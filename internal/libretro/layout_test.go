package libretro

import (
	"testing"

	"github.com/DemiCloud/doszer/internal/sdl"
)

func TestWindowSize(t *testing.T) {
	vga := geometry{BaseWidth: 640, BaseHeight: 480}
	tests := []struct {
		name   string
		g      geometry
		usable sdl.Rect
		w, h   int32
	}{
		{"1080p: doubled", vga, sdl.Rect{W: 1920, H: 1040}, 1280, 960},
		{"short screen: once", vga, sdl.Rect{W: 1356, H: 860}, 640, 480},
		{"4K: quadrupled", vga, sdl.Rect{W: 3840, H: 2100}, 2560, 1920},
		{"tiny screen: shrunk", vga, sdl.Rect{W: 600, H: 400}, 507, 380},
		{"320x200 at 4:3", geometry{BaseWidth: 320, BaseHeight: 200, Aspect: 4.0 / 3}, sdl.Rect{W: 1920, H: 1040}, 1068, 800},
		{"no display", vga, sdl.Rect{}, 640, 480},
	}
	for _, tt := range tests {
		if w, h := windowSize(tt.g, tt.usable); w != tt.w || h != tt.h {
			t.Errorf("%s: windowSize = %dx%d, want %dx%d", tt.name, w, h, tt.w, tt.h)
		}
	}
}

func TestFit(t *testing.T) {
	tests := []struct {
		w, h int32
		a    float64
		want sdl.Rect
	}{
		{1280, 800, 4.0 / 3, sdl.Rect{X: 106, Y: 0, W: 1067, H: 800}},
		{800, 800, 4.0 / 3, sdl.Rect{X: 0, Y: 100, W: 800, H: 600}},
		{1280, 960, 4.0 / 3, sdl.Rect{X: 0, Y: 0, W: 1280, H: 960}},
	}
	for _, tt := range tests {
		if got := fit(tt.w, tt.h, tt.a); got != tt.want {
			t.Errorf("fit(%d, %d, %.3f) = %+v, want %+v", tt.w, tt.h, tt.a, got, tt.want)
		}
	}
}

func TestFrameImage(t *testing.T) {
	// 2x1 XRGB8888 (bytes B, G, R, X) with 4 bytes of row padding.
	pix := []byte{0x30, 0x20, 0x10, 0x00, 0x03, 0x02, 0x01, 0xFF, 0xEE, 0xEE, 0xEE, 0xEE}
	img := frameImage(pix, 2, 1, 12)
	want := []byte{0x10, 0x20, 0x30, 255, 0x01, 0x02, 0x03, 255}
	if string(img.Pix) != string(want) {
		t.Fatalf("pixels % x, want % x", img.Pix, want)
	}
}
