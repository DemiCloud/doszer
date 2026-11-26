// Package gui is doszer's window. Games: the archives under the ROM
// directory as a list or grid, with their icons (DOSZ.ICO), how they boot
// and how long they've been played; choosing one runs it with `doszer
// run` in a process of its own, timed. Systems: the shells and OS images
// in the system directory, which games use them and which are missing.
// Settings: doszer's configuration, written to its config file. On a
// first run (no config file) it offers to set doszer up portable, in the
// user's profile, or not at all - and an unconfigured doszer writes no
// files of its own.
package gui

import (
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/image/font"

	"github.com/DemiCloud/doszer/internal/config"
	"github.com/DemiCloud/doszer/internal/library"
	"github.com/DemiCloud/doszer/internal/libretro"
	"github.com/DemiCloud/doszer/internal/sdl"
)

// Options say how the window starts. The directories are the command
// line's (--rom-dir, --system-dir, --save-dir): they win over the
// configuration, as for every command.
type Options struct {
	ROMDir, SystemDir, SaveDir string
	SDL                        []string // SDL2 libraries (builtin.Extract); nil for the system's
	Version                    string   // for the status bar

	// Screenshot, if set, saves the window as a PNG there once the scans
	// are done, then closes it: to see the window without one (tests).
	// The first-run dialog isn't opened unless Setup opens it.
	Screenshot string
	Setup      func(a *App)
	StatsPath  string // play time file instead of config.StatsPath's (tests)
}

// App is the window's state, for Options.Setup.
type App = app

type page int

const (
	pageGames page = iota
	pageSystems
	pageSettings
)

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

	// What the configuration says now (reload).
	cfg    config.Config
	romDir config.Dir
	sysDir config.Dir

	page   page
	layer  int
	hits   []hit
	clips  []sdl.Rect
	focus  *field
	popup  *popup
	setup  *setupDialog
	mx, my int32
	scroll [3]int32 // per page, pixels

	games
	systems
	settings

	running string // archive being played, or ""
	runCh   chan runResult
	message string
	msgErr  bool
	msgAt   time.Time
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

	st := loadState()
	dpi := 1.0
	if runtime.GOOS == "windows" {
		var d, hd, vd float32
		if sdl.GetDisplayDPI(0, &d, &hd, &vd) == 0 && hd > 0 {
			dpi = float64(hd) / 96
		}
	}
	w, h := int32(1120*dpi), int32(720*dpi)
	if st.Width > 0 && o.Screenshot == "" {
		w, h = st.Width, st.Height
	}
	win := sdl.CreateWindow("doszer", sdl.WindowPosCentered, sdl.WindowPosCentered, w, h, sdl.WindowHidden|sdl.WindowResizable|sdl.WindowAllowHighDPI)
	if win == 0 {
		return sdl.Error("SDL_CreateWindow")
	}
	defer sdl.DestroyWindow(win)
	sdl.SetWindowMinimumSize(win, int32(640*dpi), int32(420*dpi))
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
		o: o, win: win, ren: ren, dirty: true,
		faces: map[faceKey]font.Face{}, texts: map[textKey]*texture{}, icons: map[string]*texture{},
		runCh: make(chan runResult, 1),
	}
	a.initGames(st)
	a.initSystems()
	a.initSettings()
	defer a.destroy()
	a.makeCircle()
	a.measure()
	a.reload()
	if !config.Configured() && o.Screenshot == "" {
		a.openSetup()
	}
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
		case r := <-a.sysCh:
			a.systemsScanned(r)
		case r := <-a.runCh:
			a.finished(r)
		default:
		}
		if a.busy() {
			a.dirty = true // the status bar's activity
		}
		if a.message != "" && !a.msgErr && time.Since(a.msgAt) > 6*time.Second {
			a.message, a.dirty = "", true
		}
		if a.dirty {
			a.draw()
		}
		if a.o.Screenshot != "" && !a.scanning && !a.sysScanning {
			if a.o.Setup != nil {
				a.o.Setup(a)
			}
			// Read before presenting: after it, the back buffer's
			// content is undefined (Direct3D gave the previous frame).
			a.render()
			return a.screenshot(a.o.Screenshot)
		}
	}
	a.saveState()
	if a.running != "" {
		// The game's process outlives the window; its play time still
		// counts, recorded when it ends.
		a.finished(<-a.runCh)
	}
	return nil
}

func (a *app) busy() bool { return a.scanning || a.sysScanning || a.running != "" }

func (a *app) destroy() {
	a.clearCaches()
	for _, t := range a.icons {
		if t != nil {
			sdl.DestroyTexture(t.t)
		}
	}
	if a.circle != nil {
		sdl.DestroyTexture(a.circle.t)
	}
}

// reload reads the configuration again, after it changed (settings, the
// first-run dialog), rescanning what it moved.
func (a *app) reload() {
	cfg, err := config.Load("")
	if err != nil {
		a.say(err.Error(), true)
	}
	a.cfg = cfg
	rom, sys := config.ROMDirOf(cfg, a.o.ROMDir), config.SystemDirOf(cfg, a.o.SystemDir)
	romChanged := rom.Path != a.romDir.Path || a.games.list == nil
	sysChanged := sys.Path != a.sysDir.Path || a.systems.files == nil
	a.romDir, a.sysDir = rom, sys
	a.loadStats()
	if romChanged {
		a.rescan()
	}
	if sysChanged || romChanged {
		a.rescanSystems()
	}
	a.loadSettings()
	a.dirty = true
}

// say shows a message in the status bar: an error stays until the next
// message, anything else for a few seconds.
func (a *app) say(msg string, isErr bool) {
	a.message, a.msgErr, a.msgAt, a.dirty = msg, isErr, time.Now(), true
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

// Layout, in pixels.
func (a *app) toolbarH() int32 { return a.px(56) }
func (a *app) statusH() int32  { return a.px(30) }
func (a *app) pad() int32      { return a.px(14) }

// area is the page's scrolled rectangle.
func (a *app) area() (x, y, w, h int32) {
	return 0, a.toolbarH(), a.w, a.h - a.toolbarH() - a.statusH()
}

// scrollBy scrolls the page, within contentH.
func (a *app) scrollBy(d, contentH int32) {
	_, _, _, h := a.area()
	s := &a.scroll[a.page]
	*s = max(0, min(*s+d, contentH-h))
}

func (a *app) event(e *sdl.Event) {
	a.dirty = true
	switch e.Type() {
	case sdl.EventQuit:
		a.quit = true
	case sdl.EventWindow:
		if e.WindowEvent() == sdl.WindowEventSizeChanged {
			a.measure()
		}
	case sdl.EventTextInput:
		if a.focus != nil && a.popup == nil {
			a.focus.insert(e.Text())
		}
	case sdl.EventKeyDown:
		sc, mod, _ := e.Key()
		a.key(sc, mod)
	case sdl.EventMouseMotion:
		x, y := e.MouseXY()
		a.mx, a.my = int32(float64(x)*a.ratio), int32(float64(y)*a.ratio)
	case sdl.EventMouseWheel:
		if a.popup == nil && a.setup == nil {
			a.scrollBy(-e.Wheel()*a.px(72), a.contentH())
		}
	case sdl.EventMouseButtonDown:
		x, y := e.MouseXY()
		a.mx, a.my = int32(float64(x)*a.ratio), int32(float64(y)*a.ratio)
		if e.Button() == 1 {
			a.click(e.Clicks() >= 2)
		}
	}
}

func (a *app) key(sc int32, mod uint16) {
	ctrl := mod&sdl.KmodCtrl != 0
	if a.popup != nil {
		if sc == scEscape {
			a.popup = nil
		}
		return
	}
	if a.setup != nil {
		a.setupKey(sc)
		return
	}
	switch {
	case ctrl && sc == sc1:
		a.show(pageGames)
		return
	case ctrl && sc == sc2:
		a.show(pageSystems)
		return
	case ctrl && (sc == sc3 || sc == scComma):
		a.show(pageSettings)
		return
	case sc == scF5 || ctrl && sc == scR:
		a.rescan()
		a.rescanSystems()
		return
	}
	if a.focus != nil && a.focus.key(sc, ctrl) {
		return
	}
	switch a.page {
	case pageGames:
		a.gamesKey(sc, ctrl)
	case pageSystems:
		a.systemsKey(sc)
	case pageSettings:
		if sc == scEscape {
			a.focus = nil
		}
	}
}

// show switches to page p.
func (a *app) show(p page) {
	a.page, a.popup = p, nil
	a.focus = nil
	if p == pageGames {
		a.focus = &a.search
	}
}

// contentH is the current page's content height, for scrolling.
func (a *app) contentH() int32 {
	switch a.page {
	case pageSystems:
		return a.systemsH
	case pageSettings:
		return a.settingsH
	}
	return a.gamesH()
}

func (a *app) draw() {
	a.render()
	sdl.RenderPresent(a.ren)
	if a.frame%300 == 0 {
		a.evict()
	}
}

// render draws a frame, without presenting it.
func (a *app) render() {
	a.frame++
	a.dirty = false
	a.hits = a.hits[:0]
	a.layer = layerPage
	c := colBackground
	sdl.SetRenderDrawColor(a.ren, c.r, c.g, c.b, 255)
	sdl.RenderClear(a.ren)

	ax, ay, aw, ah := a.area()
	a.pushClip(ax, ay, aw, ah)
	switch a.page {
	case pageGames:
		a.drawGames()
	case pageSystems:
		a.drawSystems()
	case pageSettings:
		a.drawSettings()
	}
	a.popClip()
	a.drawToolbar()
	a.drawStatus()
	a.drawPopup()
	a.drawSetup()
}

func (a *app) drawToolbar() {
	th := a.toolbarH()
	a.fill(0, 0, a.w, th, colToolbar)
	a.fill(0, th-a.px(1), a.w, a.px(1), colLine)
	p := a.pad()
	y := (th - a.px(32)) / 2
	x := p
	tab := func(label string, pg page) {
		style := btnNormal
		if a.page == pg {
			style = btnOn
		}
		x += a.button(label, x, y, style, func() { a.show(pg) }) + a.px(4)
	}
	tab("Games", pageGames)
	tab("Systems", pageSystems)
	tab("Settings", pageSettings)
	x += a.px(16)
	a.fill(x-a.px(8), y+a.px(6), a.px(1), a.px(20), colLine)
	switch a.page {
	case pageGames:
		a.gamesToolbar(x, y)
	case pageSystems:
		a.systemsToolbar(x, y)
	case pageSettings:
		a.settingsToolbar(x, y)
	}
}

func (a *app) drawStatus() {
	sh := a.statusH()
	y := a.h - sh
	a.fill(0, y, a.w, sh, colToolbar)
	a.fill(0, y, a.w, a.px(1), colLine)
	p := a.pad()
	lh := a.lineHeight(12, false)
	ty := y + (sh-lh)/2

	var left string
	switch a.page {
	case pageGames:
		left = a.gamesStatus()
	case pageSystems:
		left = a.systemsStatus()
	case pageSettings:
		left = a.settingsStatus()
	}
	x := p + a.text(a.ellipsize(left, a.w/2, 12, false), p, ty, 12, false, colMuted)

	right, rc := "", colFaint
	switch {
	case a.running != "":
		name := filepath.Base(a.running)
		if g := a.find(a.running); g != nil {
			name = g.Name
		}
		right, rc = "Playing "+name+spinner(a.frame), colAccent
	case a.message != "":
		right, rc = a.message, colMuted
		if a.msgErr {
			rc = colError
		}
	case a.scanning || a.sysScanning:
		right, rc = "Scanning"+spinner(a.frame), colAccent
	default:
		right = "doszer " + a.o.Version
		switch {
		case config.Portable():
			right += " (portable)"
		case !config.Configured():
			right += " (not set up: nothing is saved)"
		}
	}
	right = a.ellipsize(right, a.w-x-3*p, 12, false)
	a.textRight(right, a.w-p, ty, 12, false, rc)
}

// emptyMessage draws a centred title and lines in the page area.
func (a *app) emptyMessage(title string, lines ...string) {
	ax, ay, aw, ah := a.area()
	lh := a.lineHeight(14, false) + a.px(6)
	y := ay + (ah-int32(len(lines)+1)*lh)/2
	w := a.textWidth(title, 16, true)
	a.text(title, ax+(aw-w)/2, y, 16, true, colText)
	y += lh + a.px(4)
	for _, l := range lines {
		w := a.textWidth(l, 14, false)
		a.text(l, ax+(aw-w)/2, y, 14, false, colMuted)
		y += lh
	}
}

// heading draws a section title and returns the y below it.
func (a *app) heading(title string, x, y int32) int32 {
	a.text(title, x, y, 15, true, colText)
	return y + a.lineHeight(15, true) + a.px(8)
}

// guiState is what the window remembers between runs, beside the play
// time file.
type guiState struct {
	Width  int32  `json:"width,omitempty"`
	Height int32  `json:"height,omitempty"`
	Grid   bool   `json:"grid,omitempty"`
	Sort   string `json:"sort,omitempty"`
}

func statePath() string {
	p, err := config.StatsPath()
	if err != nil {
		return ""
	}
	return filepath.Join(filepath.Dir(p), "window.json")
}

func loadState() guiState {
	var st guiState
	if p := statePath(); p != "" {
		if data, err := os.ReadFile(p); err == nil {
			json.Unmarshal(data, &st)
		}
	}
	return st
}

// saveState writes the window's state, but only where doszer is set up:
// a user who chose not to save gets no files.
func (a *app) saveState() {
	if a.o.Screenshot != "" || !config.Configured() {
		return
	}
	p := statePath()
	if p == "" {
		return
	}
	var ww, wh int32
	sdl.GetWindowSize(a.win, &ww, &wh)
	data, _ := json.MarshalIndent(guiState{Width: ww, Height: wh, Grid: a.grid, Sort: a.sort.String()}, "", "  ")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, append(data, '\n'), 0o644)
}

// loadStats opens the play time file where the configuration now says;
// unconfigured, play time is kept for the session only.
func (a *app) loadStats() {
	p := a.o.StatsPath
	if p == "" && config.Configured() {
		var err error
		if p, err = config.StatsPath(); err != nil {
			a.say("play time: "+err.Error(), true)
		}
	}
	s, err := library.LoadStats(p, a.romDir.Path)
	if err != nil {
		a.say("play time: "+err.Error(), true)
	}
	a.stats = s
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
