package gui

import (
	"image"
	"image/color"
	"math"

	"golang.org/x/image/vector"

	"github.com/DemiCloud/doszer/internal/sdl"
)

// The gamepad editor's controller art: a body outline in curves, its
// rim, the stick wells, the D-pad's cross and the face buttons' base,
// rasterized antialiased (golang.org/x/image/vector) once per controller
// and size, and drawn as a texture under the controls.

// curve is a cubic Bézier segment's control points and end, as fractions
// of the drawing (x of its width, y of its height).
type curve [3][2]float64

// padShape is a controller's outline: its left half from the top centre
// down round to the bottom centre, mirrored for the right half.
type padShape struct {
	start [2]float64
	left  []curve
}

// The outlines, traced from photographs: on the body (0 to 1 down
// it); path places the body under the drawing's trigger strip (by).
var xboxShape = padShape{
	start: [2]float64{0.50, 0.10},
	left: []curve{
		{{0.38, 0.10}, {0.27, 0.06}, {0.17, 0.08}},
		{{0.09, 0.10}, {0.035, 0.19}, {0.02, 0.34}},
		{{0.005, 0.50}, {0.012, 0.70}, {0.045, 0.85}},
		{{0.075, 0.96}, {0.165, 1.00}, {0.215, 0.92}},
		{{0.26, 0.83}, {0.33, 0.68}, {0.42, 0.665}},
		{{0.46, 0.66}, {0.48, 0.66}, {0.50, 0.66}},
	},
}

var steamShape = padShape{
	start: [2]float64{0.50, 0.02},
	left: []curve{
		{{0.35, 0.02}, {0.22, 0.00}, {0.14, 0.03}},
		{{0.07, 0.06}, {0.03, 0.16}, {0.02, 0.32}},
		{{0.01, 0.52}, {0.02, 0.76}, {0.06, 0.90}},
		{{0.09, 0.99}, {0.18, 1.00}, {0.23, 0.92}},
		{{0.27, 0.85}, {0.30, 0.76}, {0.38, 0.74}},
		{{0.44, 0.735}, {0.47, 0.735}, {0.50, 0.735}},
	},
}

// path adds the shape to r, scaled to w by h, shrunk by inset pixels
// towards its centre (the rim's inner edge).
func (s padShape) path(r *vector.Rasterizer, w, h, inset float32) {
	sx, sy := (w-2*inset)/w, (h-2*inset)/h
	pt := func(p [2]float64) (float32, float32) {
		x, y := float32(p[0])*w, float32(by(p[1]))*h
		return inset + x*sx, inset + y*sy
	}
	r.MoveTo(pt(s.start))
	for _, c := range s.left {
		x1, y1 := pt(c[0])
		x2, y2 := pt(c[1])
		x3, y3 := pt(c[2])
		r.CubeTo(x1, y1, x2, y2, x3, y3)
	}
	// The right half: the left's curves in reverse, mirrored.
	mirror := func(p [2]float64) [2]float64 { return [2]float64{1 - p[0], p[1]} }
	for i := len(s.left) - 1; i >= 0; i-- {
		c := s.left[i]
		end := s.start
		if i > 0 {
			end = s.left[i-1][2]
		}
		x1, y1 := pt(mirror(c[1]))
		x2, y2 := pt(mirror(c[0]))
		x3, y3 := pt(mirror(end))
		r.CubeTo(x1, y1, x2, y2, x3, y3)
	}
	r.ClosePath()
}

// circle adds a circle to r.
func circle(r *vector.Rasterizer, cx, cy, rad float32) {
	const k = 0.5523 // a quarter circle's control point distance
	r.MoveTo(cx+rad, cy)
	r.CubeTo(cx+rad, cy+k*rad, cx+k*rad, cy+rad, cx, cy+rad)
	r.CubeTo(cx-k*rad, cy+rad, cx-rad, cy+k*rad, cx-rad, cy)
	r.CubeTo(cx-rad, cy-k*rad, cx-k*rad, cy-rad, cx, cy-rad)
	r.CubeTo(cx+k*rad, cy-rad, cx+rad, cy-k*rad, cx+rad, cy)
	r.ClosePath()
}

// roundRect adds a rounded rectangle to r.
func roundRect(r *vector.Rasterizer, x, y, w, h, rad float32) {
	rad = float32(math.Min(float64(rad), math.Min(float64(w), float64(h))/2))
	const k = 0.5523
	r.MoveTo(x+rad, y)
	r.LineTo(x+w-rad, y)
	r.CubeTo(x+w-rad+k*rad, y, x+w, y+rad-k*rad, x+w, y+rad)
	r.LineTo(x+w, y+h-rad)
	r.CubeTo(x+w, y+h-rad+k*rad, x+w-rad+k*rad, y+h, x+w-rad, y+h)
	r.LineTo(x+rad, y+h)
	r.CubeTo(x+rad-k*rad, y+h, x, y+h-rad+k*rad, x, y+h-rad)
	r.LineTo(x, y+rad)
	r.CubeTo(x, y+rad-k*rad, x+rad-k*rad, y, x+rad, y)
	r.ClosePath()
}

// fill draws what add puts in a rasterizer onto img in c.
func fill(img *image.NRGBA, c color.NRGBA, add func(r *vector.Rasterizer)) {
	b := img.Bounds()
	r := vector.NewRasterizer(b.Dx(), b.Dy())
	add(r)
	r.Draw(img, b, image.NewUniform(c), image.Point{})
}

// padArt renders a controller's art at w by h pixels; button is the
// controls' size in pixels.
func padArt(l *padLayout, w, h int, button float32) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	fw, fh := float32(w), float32(h)
	at := func(key string) (float32, float32) {
		p := l.pos[key]
		return float32(p[0]) * fw, float32(p[1]) * fh
	}
	shape := l.shape
	rim := float32(math.Max(2, float64(fw)/220))
	// The bumpers, along the top edge: the body covers their lower half.
	_, ly := at("l")
	for _, b := range l.bumpers {
		x0, x1 := float32(b[0])*fw, float32(b[1])*fw
		fill(img, color.NRGBA{30, 30, 34, 255}, func(r *vector.Rasterizer) {
			top := ly - button*0.75
			roundRect(r, x0, top, x1-x0, float32(by(0.12))*fh-top, button*0.6)
		})
	}
	fill(img, color.NRGBA{86, 86, 94, 255}, func(r *vector.Rasterizer) { shape.path(r, fw, fh, 0) })
	fill(img, color.NRGBA{52, 52, 58, 255}, func(r *vector.Rasterizer) { shape.path(r, fw, fh, rim) })
	// The Steam Controller's trackpads: drawn, not mapped.
	for _, t := range l.inert {
		tw, th := float32(t[2])*fw, float32(t[3])*fh
		cx, cy := float32(t[0])*fw, float32(t[1])*fh
		fill(img, color.NRGBA{40, 40, 45, 255}, func(r *vector.Rasterizer) { roundRect(r, cx-tw/2, cy-th/2, tw, th, tw/8) })
	}
	// Stick wells.
	for _, k := range []string{"l3", "r3"} {
		cx, cy := at(k)
		lx, _ := at(k[:1] + "stick_left")
		rad := cx - lx + button*0.75 // round the direction buttons
		fill(img, color.NRGBA{36, 36, 41, 255}, func(r *vector.Rasterizer) { circle(r, cx, cy, rad) })
		fill(img, color.NRGBA{44, 44, 50, 255}, func(r *vector.Rasterizer) { circle(r, cx, cy, rad-rim) })
	}
	// The D-pad's cross.
	ux, uy := at("up")
	_, dy := at("down")
	lx, ly := at("left")
	rx, _ := at("right")
	half := button/2 + rim
	fill(img, color.NRGBA{36, 36, 41, 255}, func(r *vector.Rasterizer) {
		roundRect(r, ux-half, uy-half, 2*half, dy-uy+2*half, half/3)
		roundRect(r, lx-half, ly-half, rx-lx+2*half, 2*half, half/3)
	})
	// The Xbox or Steam button: drawn, not mapped.
	if l.logo[2] > 0 {
		cx, cy, rr := float32(l.logo[0])*fw, float32(l.logo[1])*fh, float32(l.logo[2])*fw
		fill(img, color.NRGBA{86, 86, 94, 255}, func(r *vector.Rasterizer) { circle(r, cx, cy, rr) })
		fill(img, color.NRGBA{34, 34, 38, 255}, func(r *vector.Rasterizer) { circle(r, cx, cy, rr-rim) })
	}
	// The face buttons' base.
	fx, _ := at("y")
	gx, _ := at("a")
	_, fy := at("y")
	fill(img, color.NRGBA{44, 44, 50, 255}, func(r *vector.Rasterizer) { circle(r, (fx+gx)/2, fy, (gx-fx)/2+button*0.9) })
	return img
}

// padArtTexture is the controller art for l at w by h, made once per
// controller and size.
func (a *app) padArtTexture(l *padLayout, w, h int32, button int32) *texture {
	key := l.name + ":" + itoa(int(w)) + "x" + itoa(int(h))
	if a.padArtKey == key && a.padArtTex != nil {
		return a.padArtTex
	}
	if a.padArtTex != nil {
		sdl.DestroyTexture(a.padArtTex.t)
		a.padArtTex = nil
	}
	a.padArtTex, a.padArtKey = a.upload(padArt(l, int(w), int(h), float32(button)), true), key
	return a.padArtTex
}
