package libretro

import (
	"bytes"
	_ "embed"
	"image"
	"image/draw"
	"image/png"
	"unsafe"

	"github.com/DemiCloud/doszer/internal/sdl"
)

// icon is doszer's icon at 64 pixels (`make icon`, from assets/doszer.svg).
//
//go:embed icon.png
var icon []byte

// SetIcon sets a window's icon to doszer's, for doszer's window as for
// Run's.
func SetIcon(window uintptr) { setIcon(window) }

// setIcon sets the window's icon; without one the window keeps SDL's
// default, so failures are ignored.
func setIcon(window uintptr) {
	src, err := png.Decode(bytes.NewReader(icon))
	if err != nil {
		return
	}
	b := src.Bounds()
	img := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(img, img.Bounds(), src, b.Min, draw.Src)
	surface := sdl.CreateRGBSurfaceWithFormatFrom(unsafe.Pointer(&img.Pix[0]), int32(b.Dx()), int32(b.Dy()), 32, int32(img.Stride), sdl.PixelFormatABGR8888)
	if surface == 0 {
		return
	}
	sdl.SetWindowIcon(window, surface) // copies the pixels
	sdl.FreeSurface(surface)
}
