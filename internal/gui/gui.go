// Package gui is doszer's window: the archives under the ROM directory as
// a list or grid, with their icons (DOSZ.ICO), how they boot and how long
// they've been played. Choosing one runs it with `doszer run` in a
// process of its own; the window records the session's length.
package gui

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
	"unsafe"

	"golang.org/x/image/font"

	"github.com/DemiCloud/doszer/internal/config"
	"github.com/DemiCloud/doszer/internal/icon"
	"github.com/DemiCloud/doszer/internal/library"
	"github.com/DemiCloud/doszer/internal/libretro"
	"github.com/DemiCloud/doszer/internal/sdl"
)

// Options say what the window shows and how it runs games.
type Options struct {
	ROMDir    config.Dir // "" Path: none configured or found
	SystemDir string     // passed to `doszer run` as --system-dir, if set
	SaveDir   string     // passed as --save-dir, if set
	SDL       []string   // SDL2 libraries (builtin.Extract); nil for the system's
	StatsPath string     // play time file (config.StatsPath)
	Version   string     // for the status bar

	// Screenshot, if set, saves the window as a PNG there once the scan
	// is done, then closes it: to see the window without one (tests).
	Screenshot string
	Setup      func(a *App) // before the screenshot: select, search, ...
}

// App is the window's state, for Options.Setup.
type App = app

// Select selects the n-th game shown.
func (a *app) Select(n int) { a.sel = min(n, len(a.view)-1); a.reveal() }

// Search types q into the search field.
func (a *app) Search(q string) { a.query = q; a.refilter(a.keepPath()) }

// Grid switches to the grid view.
func (a *app) Grid(on bool) { a.grid = on; a.reveal() }

type sortMode int

const (
	sortName sortMode = iota
	sortLastPlayed
	sortPlayTime
	sortSize
	sortModes
)

func (s sortMode) String() string {
	return [...]string{"Name", "Last played", "Play time", "Size"}[s]
}

// hit is a clickable area drawn this frame.
type hit struct {
	x, y, w, h int32
	button     string // a toolbar button's name, or ""
	game       int    // index into app.view, when button is ""
}

type scanResult struct {
	games []library.Game
	err   error
}

type runResult struct {
	path  string
	start time.Time
	took  time.Duration
	err   error
}

type app struct {
	o        Options
	win, ren uintptr
	w, h     int32   // output size in pixels
	ratio    float64 // output pixels per window coordinate (mouse)
	scale    float64 // logical pixel size in output pixels
	frame    int
	dirty    bool
	quit     bool

	faces  map[faceKey]font.Face
	texts  map[textKey]*texture
	icons  map[string]*texture // by archive path; nil: no icon
	circle *texture

	stats    *library.Stats
	games    []library.Game
	scanning bool
	scanErr  error
	scanCh   chan scanResult

	query  string
	sort   sortMode
	grid   bool
	view   []int // indices into games: filtered and sorted
	sel    int   // index into view, -1 for none
	scroll int32 // pixels
	hover  int   // index into view under the mouse, -1 for none
	hoverB string
	mx, my int32
	hits   []hit

	running string // archive being played, or ""
	runCh   chan runResult
	message string
	msgErr  bool
}

// Run opens the window and returns when it is closed.
func Run(o Options) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := libretro.LoadSDL(o.SDL); err != nil {
		return err
	}
	sdl.SetHint("SDL_WINDOWS_DPI_AWARENESS", "permonitorv2")
	if sdl.Init(sdl.InitVideo|sdl.InitEvents) != 0 {
		return sdl.Error("SDL_Init")
	}
	defer sdl.Quit()

	dpi := 1.0
	if runtime.GOOS == "windows" {
		var d, hd, vd float32
		if sdl.GetDisplayDPI(0, &d, &hd, &vd) == 0 && hd > 0 {
			dpi = float64(hd) / 96
		}
	}
	win := sdl.CreateWindow("doszer", sdl.WindowPosCentered, sdl.WindowPosCentered, int32(1120*dpi), int32(720*dpi), sdl.WindowHidden|sdl.WindowResizable|sdl.WindowAllowHighDPI)
	if win == 0 {
		return sdl.Error("SDL_CreateWindow")
	}
	defer sdl.DestroyWindow(win)
	sdl.SetWindowMinimumSize(win, int32(520*dpi), int32(360*dpi))
	libretro.SetIcon(win)
	ren := sdl.CreateRenderer(win, -1, sdl.RendererAccelerated)
	if ren == 0 {
		if ren = sdl.CreateRenderer(win, -1, 0); ren == 0 {
			return sdl.Error("SDL_CreateRenderer")
		}
	}
	defer sdl.DestroyRenderer(ren)
	sdl.SetRenderDrawBlendMode(ren, sdl.BlendModeBlend)

	a := &app{
		o: o, win: win, ren: ren, dirty: true, sel: -1, hover: -1,
		faces: map[faceKey]font.Face{}, texts: map[textKey]*texture{}, icons: map[string]*texture{},
		scanCh: make(chan scanResult, 1), runCh: make(chan runResult, 1),
	}
	defer a.clearCaches()
	defer func() {
		for _, t := range a.icons {
			if t != nil {
				sdl.DestroyTexture(t.t)
			}
		}
		if a.circle != nil {
			sdl.DestroyTexture(a.circle.t)
		}
	}()
	a.makeCircle()
	a.measure()
	if o.StatsPath != "" {
		var err error
		if a.stats, err = library.LoadStats(o.StatsPath, o.ROMDir.Path); err != nil {
			a.message, a.msgErr = "play time: "+err.Error(), true
		}
	}
	a.rescan()
	a.draw()
	sdl.ShowWindow(win)
	sdl.StartTextInput()

	for !a.quit {
		var e sdl.Event
		if sdl.WaitEventTimeout(&e, 100) != 0 {
			a.event(&e)
			for sdl.PollEvent(&e) != 0 {
				a.event(&e)
			}
		}
		select {
		case r := <-a.scanCh:
			a.scanned(r)
		case r := <-a.runCh:
			a.finished(r)
		default:
		}
		if a.scanning || a.running != "" {
			a.dirty = true // the status bar's activity
		}
		if a.dirty {
			a.draw()
		}
		if a.o.Screenshot != "" && !a.scanning {
			if a.o.Setup != nil {
				a.o.Setup(a)
			}
			a.draw()
			return a.screenshot(a.o.Screenshot)
		}
	}
	if a.running != "" {
		// The game's process outlives the window; its play time still
		// counts, recorded when it ends.
		r := <-a.runCh
		a.finished(r)
	}
	return nil
}

// screenshot saves what draw drew as a PNG.
func (a *app) screenshot(path string) error {
	img := image.NewNRGBA(image.Rect(0, 0, int(a.w), int(a.h)))
	if sdl.RenderReadPixels(a.ren, nil, sdl.PixelFormatABGR8888, unsafe.Pointer(&img.Pix[0]), int32(img.Stride)) != 0 {
		return sdl.Error("SDL_RenderReadPixels")
	}
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = 255
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

// measure reads the window's size and scale, clearing the caches if the
// scale changed.
func (a *app) measure() {
	var ww, wh int32
	sdl.GetWindowSize(a.win, &ww, &wh)
	sdl.GetRendererOutputSize(a.ren, &a.w, &a.h)
	ratio := 1.0
	if ww > 0 {
		ratio = float64(a.w) / float64(ww)
	}
	scale := ratio
	if runtime.GOOS == "windows" {
		var d, hd, vd float32
		if sdl.GetDisplayDPI(sdl.GetWindowDisplayIndex(a.win), &d, &hd, &vd) == 0 && hd > 0 {
			scale *= float64(hd) / 96
		}
	}
	a.ratio = ratio
	if scale != a.scale {
		a.scale = scale
		a.clearCaches()
	}
}

// rescan lists the ROM directory in the background.
func (a *app) rescan() {
	if a.o.ROMDir.Path == "" || a.scanning {
		return
	}
	a.scanning = true
	dir, stats := a.o.ROMDir.Path, a.stats
	go func() {
		games, err := library.Scan(dir, stats)
		a.scanCh <- scanResult{games, err}
	}()
}

func (a *app) scanned(r scanResult) {
	a.scanning = false
	a.scanErr = r.err
	var keep string
	if g := a.selected(); g != nil {
		keep = g.Path
	}
	for _, t := range a.icons {
		if t != nil {
			sdl.DestroyTexture(t.t)
		}
	}
	a.icons = map[string]*texture{}
	a.games = r.games
	a.refilter(keep)
}

// refilter recomputes the view after the games, query or sort changed,
// keeping keep (an archive path) selected if it is still shown.
func (a *app) refilter(keep string) {
	q := strings.ToLower(strings.TrimSpace(a.query))
	a.view = a.view[:0]
	for i, g := range a.games {
		if q == "" || strings.Contains(strings.ToLower(g.Name), q) || strings.Contains(strings.ToLower(g.Rel), q) || strings.Contains(strings.ToLower(g.System), q) {
			a.view = append(a.view, i)
		}
	}
	less := func(x, y *library.Game) bool {
		switch a.sort {
		case sortLastPlayed:
			if !x.LastPlayed.Equal(y.LastPlayed) {
				return x.LastPlayed.After(y.LastPlayed)
			}
		case sortPlayTime:
			if x.PlayTime != y.PlayTime {
				return x.PlayTime > y.PlayTime
			}
		case sortSize:
			if x.Size != y.Size {
				return x.Size > y.Size
			}
		}
		return strings.ToLower(x.Name) < strings.ToLower(y.Name)
	}
	sort.SliceStable(a.view, func(i, j int) bool { return less(&a.games[a.view[i]], &a.games[a.view[j]]) })
	a.sel = -1
	for i, gi := range a.view {
		if a.games[gi].Path == keep {
			a.sel = i
		}
	}
	if a.sel < 0 && len(a.view) > 0 {
		a.sel = 0
	}
	a.scroll = 0
	a.reveal()
	a.dirty = true
}

func (a *app) selected() *library.Game {
	if a.sel < 0 || a.sel >= len(a.view) {
		return nil
	}
	return &a.games[a.view[a.sel]]
}

// Layout, in pixels.
func (a *app) toolbarH() int32 { return a.px(56) }
func (a *app) statusH() int32  { return a.px(30) }
func (a *app) pad() int32      { return a.px(14) }
func (a *app) rowH() int32     { return a.px(88) }
func (a *app) rowGap() int32   { return a.px(8) }
func (a *app) tileW() int32    { return a.px(172) }
func (a *app) tileH() int32    { return a.px(196) }

// area is the scrolled list's rectangle.
func (a *app) area() (x, y, w, h int32) {
	return 0, a.toolbarH(), a.w, a.h - a.toolbarH() - a.statusH()
}

// columns is the grid's column count.
func (a *app) columns() int {
	_, _, w, _ := a.area()
	return max(1, int((w-a.pad())/(a.tileW()+a.rowGap())))
}

// itemRect is item i's rectangle in content coordinates (before
// scrolling, relative to the area's top).
func (a *app) itemRect(i int) (x, y, w, h int32) {
	_, _, aw, _ := a.area()
	p := a.pad()
	if !a.grid {
		return p, p + int32(i)*(a.rowH()+a.rowGap()), aw - 2*p, a.rowH()
	}
	cols := a.columns()
	used := int32(cols)*(a.tileW()+a.rowGap()) - a.rowGap()
	left := (aw - used) / 2
	return left + int32(i%cols)*(a.tileW()+a.rowGap()), p + int32(i/cols)*(a.tileH()+a.rowGap()), a.tileW(), a.tileH()
}

func (a *app) contentH() int32 {
	if len(a.view) == 0 {
		return 0
	}
	_, y, _, h := a.itemRect(len(a.view) - 1)
	return y + h + a.pad()
}

func (a *app) clampScroll() {
	_, _, _, h := a.area()
	a.scroll = max(0, min(a.scroll, a.contentH()-h))
}

// reveal scrolls the selection into view.
func (a *app) reveal() {
	if a.sel < 0 {
		return
	}
	_, y, _, h := a.itemRect(a.sel)
	_, _, _, ah := a.area()
	p := a.pad()
	if y-p < a.scroll {
		a.scroll = y - p
	}
	if y+h+p > a.scroll+ah {
		a.scroll = y + h + p - ah
	}
	a.clampScroll()
}

func (a *app) event(e *sdl.Event) {
	a.dirty = true
	switch e.Type() {
	case sdl.EventQuit:
		a.quit = true
	case sdl.EventWindow:
		if e.WindowEvent() == sdl.WindowEventSizeChanged {
			a.measure()
			a.clampScroll()
		}
	case sdl.EventTextInput:
		a.query += e.Text()
		a.refilter(a.keepPath())
	case sdl.EventKeyDown:
		sc, mod, _ := e.Key()
		a.key(sc, mod)
	case sdl.EventMouseMotion:
		x, y := e.MouseXY()
		a.mx, a.my = int32(float64(x)*a.ratio), int32(float64(y)*a.ratio)
	case sdl.EventMouseWheel:
		a.scroll -= e.Wheel() * a.px(72)
		a.clampScroll()
	case sdl.EventMouseButtonDown:
		x, y := e.MouseXY()
		a.mx, a.my = int32(float64(x)*a.ratio), int32(float64(y)*a.ratio)
		if e.Button() == 1 {
			a.click(e.Clicks() >= 2)
		}
	}
}

func (a *app) keepPath() string {
	if g := a.selected(); g != nil {
		return g.Path
	}
	return ""
}

// SDL scancodes.
const (
	scReturn    = 40
	scEscape    = 41
	scBackspace = 42
	scF5        = 62
	scHome      = 74
	scPageUp    = 75
	scEnd       = 77
	scPageDown  = 78
	scRight     = 79
	scLeft      = 80
	scDown      = 81
	scUp        = 82
	scKPEnter   = 88
	scR         = 21
	scG         = 10
	scL         = 15
)

func (a *app) key(sc int32, mod uint16) {
	step := 1
	if a.grid {
		step = a.columns()
	}
	_, _, _, ah := a.area()
	page := 1
	if a.grid {
		page = max(1, int(ah/(a.tileH()+a.rowGap()))) * a.columns()
	} else {
		page = max(1, int(ah/(a.rowH()+a.rowGap())))
	}
	move := func(d int) {
		if len(a.view) == 0 {
			return
		}
		a.sel = max(0, min(len(a.view)-1, a.sel+d))
		a.reveal()
	}
	ctrl := mod&sdl.KmodCtrl != 0
	switch {
	case sc == scReturn || sc == scKPEnter:
		a.play()
	case sc == scEscape:
		if a.query != "" {
			a.query = ""
			a.refilter(a.keepPath())
		}
	case sc == scBackspace:
		if a.query != "" {
			_, n := utf8.DecodeLastRuneInString(a.query)
			if ctrl {
				n = len(a.query)
			}
			a.query = a.query[:len(a.query)-n]
			a.refilter(a.keepPath())
		}
	case sc == scF5 || ctrl && sc == scR:
		a.rescan()
	case ctrl && sc == scG:
		a.grid = true
		a.reveal()
	case ctrl && sc == scL:
		a.grid = false
		a.reveal()
	case sc == scUp:
		move(-step)
	case sc == scDown:
		move(step)
	case sc == scLeft && a.grid:
		move(-1)
	case sc == scRight && a.grid:
		move(1)
	case sc == scPageUp:
		move(-page)
	case sc == scPageDown:
		move(page)
	case sc == scHome && a.query == "":
		move(-len(a.view))
	case sc == scEnd && a.query == "":
		move(len(a.view))
	}
}

func (a *app) click(double bool) {
	for _, h := range a.hits {
		if a.mx < h.x || a.mx >= h.x+h.w || a.my < h.y || a.my >= h.y+h.h {
			continue
		}
		switch h.button {
		case "list":
			a.grid = false
			a.reveal()
		case "grid":
			a.grid = true
			a.reveal()
		case "sort":
			a.sort = (a.sort + 1) % sortModes
			a.refilter(a.keepPath())
		case "refresh":
			a.rescan()
		case "clear":
			a.query = ""
			a.refilter(a.keepPath())
		case "":
			a.sel = h.game
			if double {
				a.play()
			}
		}
		return
	}
}

// play runs the selected game with `doszer run`, one at a time.
func (a *app) play() {
	g := a.selected()
	if g == nil || a.running != "" {
		return
	}
	if g.Err != "" {
		a.message, a.msgErr = g.Name+": "+g.Err, true
		return
	}
	exe, err := os.Executable()
	if err != nil {
		a.message, a.msgErr = err.Error(), true
		return
	}
	args := []string{"run"}
	if a.o.SystemDir != "" {
		args = append(args, "--system-dir", a.o.SystemDir)
	}
	if a.o.SaveDir != "" {
		args = append(args, "--save-dir", a.o.SaveDir)
	}
	args = append(args, "--", g.Path)
	cmd := exec.Command(exe, args...)
	hideConsole(cmd)
	tail := &tailBuffer{}
	cmd.Stdout, cmd.Stderr = tail, tail
	start := time.Now()
	if err := cmd.Start(); err != nil {
		a.message, a.msgErr = err.Error(), true
		return
	}
	a.running = g.Path
	a.message, a.msgErr = "", false
	go func() {
		err := cmd.Wait()
		if err != nil {
			if line := tail.lastError(); line != "" {
				err = errors.New(line)
			}
		}
		a.runCh <- runResult{g.Path, start, time.Since(start), err}
	}()
}

func (a *app) finished(r runResult) {
	a.running = ""
	var name string
	for i := range a.games {
		g := &a.games[i]
		if g.Path != r.path {
			continue
		}
		name = g.Name
		g.LastPlayed = r.start
		g.PlayTime += r.took.Round(time.Second)
	}
	if a.stats != nil {
		if err := a.stats.Add(r.path, r.start, r.took); err != nil {
			a.message, a.msgErr = "play time: "+err.Error(), true
		}
	}
	if r.err != nil {
		a.message, a.msgErr = name+": "+r.err.Error(), true
	} else if !a.msgErr {
		a.message = ""
	}
	if a.sort != sortName {
		a.refilter(r.path)
	}
	if a.win != 0 && !a.quit {
		sdl.RaiseWindow(a.win)
	}
}

// tailBuffer keeps the end of a game process's output, for its error.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > 8<<10 {
		t.buf = t.buf[len(t.buf)-8<<10:]
	}
	return len(p), nil
}

// lastError is the last "ERROR: " line's message (main's logError), else
// the last line.
func (t *tailBuffer) lastError() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	lines := strings.Split(strings.TrimSpace(string(bytes.ReplaceAll(t.buf, []byte("\r"), nil))), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if _, msg, ok := strings.Cut(lines[i], "ERROR: "); ok {
			return msg
		}
	}
	return strings.TrimSpace(lines[len(lines)-1])
}

func (a *app) draw() {
	a.frame++
	a.dirty = false
	a.hits = a.hits[:0]
	c := colBackground
	sdl.SetRenderDrawColor(a.ren, c.r, c.g, c.b, 255)
	sdl.RenderClear(a.ren)

	a.drawList()
	a.drawToolbar()
	a.drawStatus()

	sdl.RenderPresent(a.ren)
	if a.frame%300 == 0 {
		a.evict()
	}
}

// button draws a toolbar button and registers it; returns its width.
func (a *app) button(name, label string, x, y int32, on bool) int32 {
	label, arrow := strings.CutSuffix(label, " ▼")
	h := a.px(32)
	w := a.textWidth(label, 13, false) + a.px(24)
	if arrow {
		w += a.px(16)
	}
	col := colButton
	if on {
		col = colButtonOn
	}
	if a.hoverButton(x, y, w, h) {
		col = colCardSel
	}
	a.round(x, y, w, h, a.px(6), col)
	lh := a.lineHeight(13, false)
	tw := a.text(label, x+a.px(12), y+(h-lh)/2, 13, false, colText)
	if arrow {
		al := a.lineHeight(8, false)
		a.text("▼", x+a.px(18)+tw, y+(h-al)/2, 8, false, colMuted)
	}
	a.hits = append(a.hits, hit{x: x, y: y, w: w, h: h, button: name})
	return w
}

func (a *app) hoverButton(x, y, w, h int32) bool {
	return a.mx >= x && a.mx < x+w && a.my >= y && a.my < y+h
}

func (a *app) drawToolbar() {
	th := a.toolbarH()
	a.fill(0, 0, a.w, th, colToolbar)
	p := a.pad()
	y := (th - a.px(32)) / 2
	x := p
	x += a.button("list", "List", x, y, !a.grid) + a.px(4)
	x += a.button("grid", "Grid", x, y, a.grid) + a.px(12)
	x += a.button("sort", "Sort: "+a.sort.String()+" ▼", x, y, false) + a.px(12)
	a.button("refresh", "Refresh", x, y, false)

	// The search field: typing anywhere goes into it.
	fw := min(a.px(320), a.w/3)
	fx := a.w - p - fw
	fh := a.px(32)
	a.outline(fx, y, fw, fh, a.px(6), a.px(1), colLine, colField)
	lh := a.lineHeight(13, false)
	ty := y + (fh-lh)/2
	if a.query == "" {
		a.text("Type to search…", fx+a.px(12), ty, 13, false, colFaint)
		return
	}
	q := a.ellipsize(a.query, fw-a.px(52), 13, false)
	tw := a.text(q, fx+a.px(12), ty, 13, false, colText)
	a.fill(fx+a.px(13)+tw, ty, a.px(1.5), lh, colAccent) // the caret
	cw := a.px(28)
	a.textRight("×", fx+fw-a.px(10), ty, 15, true, colMuted)
	a.hits = append(a.hits, hit{x: fx + fw - cw, y: y, w: cw, h: fh, button: "clear"})
}

func (a *app) drawStatus() {
	sh := a.statusH()
	y := a.h - sh
	a.fill(0, y, a.w, sh, colToolbar)
	a.fill(0, y, a.w, a.px(1), colLine)
	p := a.pad()
	lh := a.lineHeight(12, false)
	ty := y + (sh-lh)/2

	var total time.Duration
	for _, g := range a.games {
		total += g.PlayTime
	}
	left := fmt.Sprintf("%d of %d games", len(a.view), len(a.games))
	if len(a.view) == len(a.games) {
		left = plural(len(a.games), "game")
	}
	if total > 0 {
		left += "  |  Total play time: " + duration(total)
	}
	x := p + a.text(left, p, ty, 12, false, colMuted)

	right, rc := a.o.ROMDir.Path, colFaint
	switch {
	case a.running != "":
		name := filepath.Base(a.running)
		if g := a.find(a.running); g != nil {
			name = g.Name
		}
		right, rc = "Playing "+name+spinner(a.frame), colAccent
	case a.scanning:
		right, rc = "Scanning"+spinner(a.frame), colAccent
	case a.message != "":
		right, rc = a.message, colMuted
		if a.msgErr {
			rc = colError
		}
	case a.o.Version != "":
		right = "doszer " + a.o.Version + "  |  " + right
	}
	right = a.ellipsize(right, a.w-x-3*p, 12, false)
	a.textRight(right, a.w-p, ty, 12, false, rc)
}

func (a *app) find(path string) *library.Game {
	for i := range a.games {
		if a.games[i].Path == path {
			return &a.games[i]
		}
	}
	return nil
}

func (a *app) drawList() {
	ax, ay, aw, ah := a.area()
	sdl.RenderSetClipRect(a.ren, &sdl.Rect{X: ax, Y: ay, W: aw, H: ah})
	defer sdl.RenderSetClipRect(a.ren, nil)

	if msg := a.emptyMessage(); msg != "" {
		lines := strings.Split(msg, "\n")
		lh := a.lineHeight(14, false) + a.px(6)
		y := ay + (ah-int32(len(lines))*lh)/2
		for i, l := range lines {
			size, bold, c := 14.0, false, colMuted
			if i == 0 {
				size, bold, c = 16, true, colText
			}
			w := a.textWidth(l, size, bold)
			a.text(l, (aw-w)/2, y, size, bold, c)
			y += lh
		}
		return
	}

	a.hover = -1
	for i := range a.view {
		x, y, w, h := a.itemRect(i)
		y += ay - a.scroll
		if y+h < ay || y > ay+ah {
			continue
		}
		hover := a.mx >= x && a.mx < x+w && a.my >= y && a.my < y+h && a.my >= ay && a.my < ay+ah
		if hover {
			a.hover = i
		}
		g := &a.games[a.view[i]]
		if a.grid {
			a.drawTile(g, x, y, w, h, i == a.sel, hover)
		} else {
			a.drawRow(g, x, y, w, h, i == a.sel, hover)
		}
		// Only the visible part of an item is clickable.
		cy, ch := max(y, ay), min(y+h, ay+ah)-max(y, ay)
		a.hits = append(a.hits, hit{x: x, y: cy, w: w, h: ch, game: i})
	}
}

func (a *app) emptyMessage() string {
	switch {
	case a.o.ROMDir.Path == "":
		return "No ROM directory\nCreate a roms folder beside doszer, or set one:\ndoszer config set rom_dir DIR"
	case a.scanErr != nil:
		return "Can't read the ROM directory\n" + a.scanErr.Error()
	case a.scanning && len(a.games) == 0:
		return "Scanning\n" + a.o.ROMDir.Path
	case len(a.games) == 0:
		return "No games yet\nNo .dosz archives in " + a.o.ROMDir.Path
	case len(a.view) == 0:
		return "No matches\nNo game matches “" + a.query + "” (Esc clears the search)"
	}
	return ""
}

// card draws an item's background.
func (a *app) card(x, y, w, h int32, sel, hover bool) {
	r := a.px(10)
	switch {
	case sel:
		a.outline(x, y, w, h, r, a.px(2), colAccent, colCardSel)
	case hover:
		a.round(x, y, w, h, r, colCardHover)
	default:
		a.round(x, y, w, h, r, colCard)
	}
}

func (a *app) drawRow(g *library.Game, x, y, w, h int32, sel, hover bool) {
	a.card(x, y, w, h, sel, hover)
	is := a.px(64)
	ix := x + a.px(12)
	iy := y + (h-is)/2
	a.drawIcon(g, ix, iy, is)

	right := a.px(200)
	tx := ix + is + a.px(16)
	tw := w - (tx - x) - right - a.px(24)
	ty := y + a.px(12)
	a.text(a.ellipsize(g.Name, tw, 16, true), tx, ty, 16, true, colText)
	ty += a.lineHeight(16, true) + a.px(2)
	a.text(a.ellipsize(g.Rel, tw, 13, false), tx, ty, 13, false, colMuted)
	ty += a.lineHeight(13, false) + a.px(6)
	a.chip(g, tx, ty)

	sx := x + w - right - a.px(12)
	a.fill(sx, y+a.px(14), a.px(1), h-a.px(28), colLine)
	rx := x + w - a.px(16)
	ry := y + a.px(14)
	lh := a.lineHeight(13, false) + a.px(4)
	played := "Never played"
	if !g.LastPlayed.IsZero() {
		played = g.LastPlayed.Local().Format("Jan 2, 2006 3:04 PM")
	}
	lines := []string{played}
	if g.PlayTime > 0 {
		lines = append(lines, duration(g.PlayTime)+" played")
	}
	lines = append(lines, size(g.Size))
	for i, l := range lines {
		c := colMuted
		if i == 0 {
			c = colText
		}
		a.textRight(l, rx, ry+int32(i)*lh, 13, false, c)
	}
}

func (a *app) drawTile(g *library.Game, x, y, w, h int32, sel, hover bool) {
	a.card(x, y, w, h, sel, hover)
	is := a.px(96)
	a.drawIcon(g, x+(w-is)/2, y+a.px(18), is)
	ty := y + a.px(18) + is + a.px(14)
	name := a.ellipsize(g.Name, w-a.px(20), 14, true)
	a.text(name, x+(w-a.textWidth(name, 14, true))/2, ty, 14, true, colText)
	ty += a.lineHeight(14, true) + a.px(4)
	label, c := kindLabel(g)
	label = a.ellipsize(label, w-a.px(20), 12, false)
	a.text(label, x+(w-a.textWidth(label, 12, false))/2, ty, 12, false, c)
}

// chip draws how the game boots as a coloured badge.
func (a *app) chip(g *library.Game, x, y int32) {
	label, c := kindLabel(g)
	if g.Err != "" {
		label, c = "Unreadable: "+g.Err, colError
		label = a.ellipsize(label, a.px(420), 12, false)
	}
	w := a.textWidth(label, 12, false) + a.px(16)
	h := a.lineHeight(12, false) + a.px(6)
	a.round(x, y, w, h, a.px(5), rgba{c.r / 5, c.g / 5, c.b / 5, 255})
	a.text(label, x+a.px(8), y+a.px(3), 12, false, c)
}

func kindLabel(g *library.Game) (string, rgba) {
	switch g.Kind {
	case library.KindShell:
		return g.System + " (shell)", colShell
	case library.KindOS:
		return g.System, colOS
	}
	return "DOS", colDOS
}

// drawIcon draws the game's DOSZ.ICO, or a tile with its initial.
func (a *app) drawIcon(g *library.Game, x, y, s int32) {
	t, ok := a.icons[g.Path]
	if !ok {
		t = nil
		if g.Icon != nil {
			if img, err := icon.Decode(g.Icon, int(s)); err == nil {
				b := img.Bounds()
				n := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
				draw.Draw(n, n.Bounds(), img, b.Min, draw.Src)
				t = a.upload(n, true)
			}
		}
		a.icons[g.Path] = t
	}
	if t != nil {
		a.image(t, x, y, s)
		return
	}
	_, c := kindLabel(g)
	a.round(x, y, s, s, s/6, rgba{c.r / 3, c.g / 3, c.b / 3, 255})
	r, _ := utf8.DecodeRuneInString(strings.TrimLeft(g.Name, " ."))
	initial := strings.ToUpper(string(r))
	size := float64(s) / a.scale * 0.5
	w := a.textWidth(initial, size, true)
	lh := a.lineHeight(size, true)
	a.text(initial, x+(s-w)/2, y+(s-lh)/2, size, true, c)
}

func spinner(frame int) string {
	return [...]string{"   ", ".  ", ".. ", "..."}[(frame/3)%4]
}

func plural(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return fmt.Sprintf("%d %ss", n, what)
}

// duration formats a play time: "45s", "12m", "3h 05m".
func duration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh %02dm", int(d.Hours()), int(d.Minutes())%60)
}

func size(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%d KB", (n+1023)/1024)
}
