package gui

import (
	"image"
	"image/color"
	"math"
	"strings"
	"unsafe"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"

	"github.com/DemiCloud/doszer/internal/sdl"
)

// rgba is a colour.
type rgba struct{ r, g, b, a uint8 }

// The palette: dark, with one accent and a colour per boot model.
var (
	colBackground = rgba{30, 30, 32, 255}
	colToolbar    = rgba{22, 22, 24, 255}
	colMenuBar    = rgba{17, 17, 19, 255}
	colCard       = rgba{44, 44, 47, 255}
	colCardHover  = rgba{52, 52, 56, 255}
	colCardSel    = rgba{58, 60, 66, 255}
	colAccent     = rgba{90, 160, 250, 255}
	colAccentDark = rgba{44, 98, 170, 255}
	colPopup      = rgba{38, 38, 42, 255}
	colText       = rgba{240, 240, 242, 255}
	colMuted      = rgba{160, 160, 168, 255}
	colFaint      = rgba{110, 110, 118, 255}
	colField      = rgba{40, 40, 43, 255}
	colButton     = rgba{48, 48, 52, 255}
	colButtonOn   = rgba{70, 72, 80, 255}
	colLine       = rgba{70, 70, 76, 255}
	colError      = rgba{240, 110, 100, 255}
	colDOS        = rgba{110, 200, 120, 255}
	colShell      = rgba{240, 180, 80, 255}
	colOS         = rgba{110, 170, 250, 255}
)

// texture is an SDL texture and its size in pixels.
type texture struct {
	t    uintptr
	w, h int32
	used int // frame it was last drawn in
}

var (
	regularFont, _ = opentype.Parse(goregular.TTF)
	boldFont, _    = opentype.Parse(gobold.TTF)
	monoFont, _    = opentype.Parse(gomono.TTF)
)

type faceKey struct {
	px   int
	bold bool
	mono bool
}

type textKey struct {
	s string
	faceKey
}

// face returns the font face of px pixels.
func (a *app) face(px int, bold bool) font.Face { return a.faceOf(faceKey{px: px, bold: bold}) }

func (a *app) faceOf(k faceKey) font.Face {
	if f, ok := a.faces[k]; ok {
		return f
	}
	f := regularFont
	switch {
	case k.mono:
		f = monoFont
	case k.bold:
		f = boldFont
	}
	face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: float64(k.px), DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		panic(err) // the embedded Go fonts always parse
	}
	a.faces[k] = face
	return face
}

// px is n logical pixels at the window's scale.
func (a *app) px(n float64) int32 { return int32(math.Round(n * a.scale)) }

// textWidth measures s in pixels.
func (a *app) textWidth(s string, size float64, bold bool) int32 {
	return int32(font.MeasureString(a.face(int(a.px(size)), bold), s).Ceil())
}

// text draws s with its top left at x, y, in size logical pixels, and
// returns its width. Rendered strings are cached as textures.
func (a *app) text(s string, x, y int32, size float64, bold bool, c rgba) int32 {
	if s == "" {
		return 0
	}
	return a.drawText(textKey{s, faceKey{px: int(a.px(size)), bold: bold}}, x, y, c)
}

// monoText draws s in the monospaced face, 12 logical pixels.
func (a *app) monoText(s string, x, y int32, c rgba) int32 {
	if s == "" {
		return 0
	}
	return a.drawText(textKey{s, faceKey{px: int(a.px(12)), mono: true}}, x, y, c)
}

func (a *app) drawText(k textKey, x, y int32, c rgba) int32 {
	t, ok := a.texts[k]
	if !ok {
		t = a.renderText(k)
		a.texts[k] = t
	}
	if t == nil {
		return 0
	}
	t.used = a.frame
	sdl.SetTextureColorMod(t.t, c.r, c.g, c.b)
	sdl.SetTextureAlphaMod(t.t, c.a)
	dst := sdl.Rect{X: x, Y: y, W: t.w, H: t.h}
	sdl.RenderCopy(a.ren, t.t, nil, unsafe.Pointer(&dst))
	return t.w
}

// textRight draws s ending at x.
func (a *app) textRight(s string, x, y int32, size float64, bold bool, c rgba) {
	a.text(s, x-a.textWidth(s, size, bold), y, size, bold, c)
}

// lineHeight is a face's height in pixels.
func (a *app) lineHeight(size float64, bold bool) int32 {
	m := a.face(int(a.px(size)), bold).Metrics()
	return int32((m.Ascent + m.Descent).Ceil())
}

func (a *app) renderText(k textKey) *texture {
	face := a.faceOf(k.faceKey)
	m := face.Metrics()
	w := font.MeasureString(face, k.s).Ceil()
	h := (m.Ascent + m.Descent).Ceil()
	if w <= 0 || h <= 0 {
		return nil
	}
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	d := font.Drawer{Dst: img, Src: image.NewUniform(color.White), Face: face, Dot: fixed.Point26_6{Y: m.Ascent}}
	d.DrawString(k.s)
	return a.upload(img, false)
}

// upload makes a texture of img, scaled linearly if linear.
func (a *app) upload(img *image.NRGBA, linear bool) *texture {
	b := img.Bounds()
	t := sdl.CreateTexture(a.ren, sdl.PixelFormatABGR8888, 0, int32(b.Dx()), int32(b.Dy()))
	if t == 0 {
		return nil
	}
	sdl.UpdateTexture(t, nil, unsafe.Pointer(&img.Pix[0]), int32(img.Stride))
	sdl.SetTextureBlendMode(t, sdl.BlendModeBlend)
	mode := int32(sdl.ScaleModeNearest)
	if linear {
		mode = sdl.ScaleModeLinear
	}
	sdl.SetTextureScaleMode(t, mode)
	return &texture{t: t, w: int32(b.Dx()), h: int32(b.Dy()), used: a.frame}
}

// ellipsize shortens s with "…" to fit width pixels.
func (a *app) ellipsize(s string, width int32, size float64, bold bool) string {
	if a.textWidth(s, size, bold) <= width {
		return s
	}
	r := []rune(s)
	lo, hi := 0, len(r)
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if a.textWidth(string(r[:mid])+"…", size, bold) <= width {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return string(r[:lo]) + "…"
}

// fill fills a rectangle.
func (a *app) fill(x, y, w, h int32, c rgba) {
	if w <= 0 || h <= 0 {
		return
	}
	sdl.SetRenderDrawColor(a.ren, c.r, c.g, c.b, c.a)
	sdl.RenderFillRect(a.ren, &sdl.Rect{X: x, Y: y, W: w, H: h})
}

// circleRadius is the radius of the circle texture rounded corners are
// cut from.
const circleRadius = 64

// round fills a rectangle with corners of radius r pixels.
func (a *app) round(x, y, w, h, r int32, c rgba) {
	r = min(r, w/2, h/2)
	if r <= 0 || a.circle == nil {
		a.fill(x, y, w, h, c)
		return
	}
	a.fill(x+r, y, w-2*r, h, c)
	a.fill(x, y+r, r, h-2*r, c)
	a.fill(x+w-r, y+r, r, h-2*r, c)
	t := a.circle.t
	sdl.SetTextureColorMod(t, c.r, c.g, c.b)
	sdl.SetTextureAlphaMod(t, c.a)
	const q = circleRadius
	corners := [4][4]int32{{0, 0, x, y}, {q, 0, x + w - r, y}, {0, q, x, y + h - r}, {q, q, x + w - r, y + h - r}}
	for _, k := range corners {
		src := sdl.Rect{X: k[0], Y: k[1], W: q, H: q}
		dst := sdl.Rect{X: k[2], Y: k[3], W: r, H: r}
		sdl.RenderCopy(a.ren, t, unsafe.Pointer(&src), unsafe.Pointer(&dst))
	}
}

// outline draws a rounded rectangle's border, thickness t.
func (a *app) outline(x, y, w, h, r, t int32, c, inside rgba) {
	a.round(x, y, w, h, r, c)
	a.round(x+t, y+t, w-2*t, h-2*t, max(r-t, 0), inside)
}

// makeCircle renders the white anti-aliased circle round cuts corners from.
func (a *app) makeCircle() {
	const d = 2 * circleRadius
	img := image.NewNRGBA(image.Rect(0, 0, d, d))
	for y := range d {
		for x := range d {
			dx, dy := float64(x)+0.5-circleRadius, float64(y)+0.5-circleRadius
			cov := math.Max(0, math.Min(1, circleRadius-math.Hypot(dx, dy)+0.5))
			o := img.PixOffset(x, y)
			img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = 255, 255, 255, uint8(cov*255)
		}
	}
	a.circle = a.upload(img, true)
}

// image draws t scaled to a size x size square at x, y, keeping its
// aspect ratio: nearest-neighbour when enlarged (icons are pixel art),
// linear when reduced.
func (a *app) image(t *texture, x, y, size int32) {
	if t == nil {
		return
	}
	t.used = a.frame
	mode := int32(sdl.ScaleModeLinear)
	if t.w < size {
		mode = sdl.ScaleModeNearest
	}
	sdl.SetTextureScaleMode(t.t, mode)
	w, h := size, size
	if t.w > t.h {
		h = size * t.h / t.w
	} else if t.h > t.w {
		w = size * t.w / t.h
	}
	dst := sdl.Rect{X: x + (size-w)/2, Y: y + (size-h)/2, W: w, H: h}
	sdl.RenderCopy(a.ren, t.t, nil, unsafe.Pointer(&dst))
}

// evict destroys cached text textures not drawn for a while.
func (a *app) evict() {
	for k, t := range a.texts {
		if t == nil || a.frame-t.used > 600 {
			if t != nil {
				sdl.DestroyTexture(t.t)
			}
			delete(a.texts, k)
		}
	}
}

// clearCaches destroys every cached texture and face, after the scale
// changes.
func (a *app) clearCaches() {
	for k, t := range a.texts {
		if t != nil {
			sdl.DestroyTexture(t.t)
		}
		delete(a.texts, k)
	}
	for k, f := range a.faces {
		f.Close()
		delete(a.faces, k)
	}
}

func destroyTexture(t *texture) { sdl.DestroyTexture(t.t) }

func raiseWindow(w uintptr) { sdl.RaiseWindow(w) }

// wrap breaks s into lines of at most width pixels, at spaces.
func (a *app) wrap(s string, width int32, size float64) []string {
	var out []string
	for _, para := range strings.Split(s, "\n") {
		line := ""
		for _, w := range strings.Fields(para) {
			try := w
			if line != "" {
				try = line + " " + w
			}
			if line != "" && a.textWidth(try, size, false) > width {
				out = append(out, line)
				line = w
				continue
			}
			line = try
		}
		out = append(out, line)
	}
	return out
}

// monoWidth is the monospaced face's character width in pixels.
func (a *app) monoWidth() int32 {
	return max(1, int32(font.MeasureString(a.faceOf(faceKey{px: int(a.px(12)), mono: true}), "M").Ceil()))
}

// wrapAny is wrap, also breaking a word longer than width (a path) by
// characters.
func (a *app) wrapAny(s string, width int32, size float64) []string {
	var out []string
	for _, l := range a.wrap(s, width, size) {
		for a.textWidth(l, size, false) > width {
			r := []rune(l)
			n := len(r)
			for n > 1 && a.textWidth(string(r[:n]), size, false) > width {
				n--
			}
			out = append(out, string(r[:n]))
			l = string(r[n:])
		}
		out = append(out, l)
	}
	return out
}

// scrollbar draws a thin bar for lines first..first+visible of total at
// x, y, h tall, when they don't all fit.
func (a *app) scrollbar(x, y, h int32, first, visible, total int) {
	if total <= visible || total == 0 {
		return
	}
	w := a.px(4)
	a.round(x, y, w, h, w/2, rgba{255, 255, 255, 18})
	th := max(a.px(24), h*int32(visible)/int32(total))
	ty := y + (h-th)*int32(first)/int32(max(1, total-visible))
	a.round(x, ty, w, th, w/2, rgba{255, 255, 255, 90})
}

// checkMark is a white anti-aliased check mark, the size of a checkbox,
// drawn once per scale.
func (a *app) checkMark() *texture {
	s := int(a.px(20))
	if a.checkTex != nil && int(a.checkTex.w) == s {
		return a.checkTex
	}
	if a.checkTex != nil {
		sdl.DestroyTexture(a.checkTex.t)
	}
	img := image.NewNRGBA(image.Rect(0, 0, s, s))
	f := float64(s)
	// Two strokes: (0.25,0.52)-(0.42,0.70)-(0.76,0.32), in box fractions.
	segs := [][4]float64{{0.25, 0.52, 0.42, 0.70}, {0.42, 0.70, 0.76, 0.32}}
	half := math.Max(1, f*0.07)
	for y := 0; y < s; y++ {
		for x := 0; x < s; x++ {
			px, py := float64(x)+0.5, float64(y)+0.5
			d := math.Inf(1)
			for _, sg := range segs {
				ax, ay, bx, by := sg[0]*f, sg[1]*f, sg[2]*f, sg[3]*f
				vx, vy := bx-ax, by-ay
				t := math.Max(0, math.Min(1, ((px-ax)*vx+(py-ay)*vy)/(vx*vx+vy*vy)))
				d = math.Min(d, math.Hypot(px-(ax+t*vx), py-(ay+t*vy)))
			}
			cov := math.Max(0, math.Min(1, half-d+0.5))
			o := img.PixOffset(x, y)
			img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = 255, 255, 255, uint8(cov*255)
		}
	}
	a.checkTex = a.upload(img, true)
	return a.checkTex
}

func sdlColorMod(t *texture, c rgba) {
	if t != nil {
		sdl.SetTextureColorMod(t.t, c.r, c.g, c.b)
	}
}
