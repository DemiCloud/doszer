// Package gui is doszer's window. Dashboard: the collection at a glance.
// Library: the games under the ROM directory as a list or grid, with
// their icons (DOSZ.ICO), how they boot and how long they've been played
// (choosing one runs it with `doszer run` in a process of its own,
// timed); the parents games share; the shells and OS images in the
// system directory, which games use them and which are missing. Logs:
// the runs doszer recorded, each one's report and history.
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
	"strings"
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
	LogsDir    string // run logs instead of config.LogsDir's (tests)
	Exe        string // the doszer that runs games and commands, instead of this one (tests)
}

// exe is the doszer program games and commands run with.
func (a *app) exe() (string, error) {
	if a.o.Exe != "" {
		return a.o.Exe, nil
	}
	return os.Executable()
}

// App is the window's state, for Options.Setup.
type App = app

type page int

const (
	pageGames page = iota
	pageParents
	pageSystems
	pageDashboard
	pageLogs
	pages
)

// pageNames name the pages, for window.json and Show.
var pageNames = map[page]string{pageDashboard: "dashboard", pageGames: "games", pageParents: "parents", pageSystems: "systems", pageLogs: "logs"}

// inLibrary reports whether p is one of the Library tab's views.
func inLibrary(p page) bool { return p == pageGames || p == pageParents || p == pageSystems }

type app struct {
	o        Options
	win, ren uintptr
	w, h     int32   // output size in pixels
	ratio    float64 // output pixels per window coordinate (mouse)
	scale    float64 // logical pixel size in output pixels
	frame    int
	dirty    bool
	quit     bool

	faces    map[faceKey]font.Face
	texts    map[textKey]*texture
	icons    map[string]*texture // by archive path; nil: no icon
	aboutTex *texture
	checkTex *texture
	circle   *texture

	// What the configuration says now (reload).
	cfg     config.Config
	romDirs []config.Dir
	sysDir  config.Dir

	page  page
	layer int
	hits  []hit
	clips []sdl.Rect
	focus *field
	popup *popup
	setup *setupDialog
	add   *addGame
	edit  *editDialog
	// The text area typing goes to (the editor's), and the one under the
	// mouse (the wheel scrolls it).
	focusArea, areaUnderMouse *textArea
	dialog                    *dialog
	jobs                      []*dialog // dialogs whose command is still running
	menuAt                    func()    // the right-click menu of what was clicked last

	tsel    textSel
	selHits []selHit
	selText map[string][]string
	selSeps map[string][]string // per region, what goes before each line when copied (selAdd)

	setupDone       bool   // Options.Setup ran
	exportDir       string // the RetroDECK root Export last used (window.json)
	exportZipDir    string // the folder its last ZIP went in
	exportFlat      bool   // its last ZIP had one roms folder
	exp             *exportDialog
	child           *childDialog
	selectAfterScan string // an archive to select once the list is read again
	mx, my          int32
	scroll          [pages]int32   // per page, pixels
	libView         page           // the Library view shown last
	dashboardH      int32          // the Dashboard's height, as last drawn
	parentList      []library.Game // the .dosp archives
	parentSel       int

	games
	systems
	settings
	logs

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
	// A click that focuses the window also clicks: after a drop from
	// Explorer, the first click on Next is Next.
	sdl.SetHint("SDL_MOUSE_FOCUS_CLICKTHROUGH", "1")
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
		runCh: make(chan runResult, 1), selText: map[string][]string{}, selSeps: map[string][]string{},
	}
	a.initGames(st)
	a.initSystems()
	a.initLogs()
	a.libView = pageGames
	// The page shown last, else the Dashboard; screenshots start at Games.
	if o.Screenshot == "" {
		a.page = pageDashboard
		for p, n := range pageNames {
			if n == st.Page {
				a.page = p
			}
		}
		a.focus = nil
		if a.page == pageGames {
			a.focus = &a.search
		}
	}
	a.initSettings()
	defer a.destroy()
	a.makeCircle()
	a.measure()
	a.reload()
	if !config.Configured() && o.Screenshot == "" {
		a.openSetup()
	}
	a.draw()
	if o.Screenshot == "" {
		sdl.ShowWindow(win) // a screenshot is drawn in the hidden window
	}
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
		case r := <-a.logsCh:
			a.logsRead(r)
		default:
		}
		a.pollJobs()
		if a.busy() {
			a.dirty = true // the status bar's activity
		}
		if a.message != "" && !a.msgErr && time.Since(a.msgAt) > 6*time.Second {
			a.message, a.dirty = "", true
		}
		if a.dirty {
			a.draw()
		}
		if a.o.Screenshot != "" && !a.scanning && !a.sysScanning && !a.logsReading {
			if a.o.Setup != nil && !a.setupDone {
				a.o.Setup(a)
				a.setupDone = true
			}
			if len(a.jobs) > 0 {
				continue // the shot shows a command's whole output
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

func (a *app) busy() bool { return a.scanning || a.sysScanning || a.running != "" || len(a.jobs) > 0 }

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
	if a.aboutTex != nil {
		sdl.DestroyTexture(a.aboutTex.t)
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
	rom, sys := config.ROMDirsOf(cfg, a.o.ROMDir), config.SystemDirOf(cfg, a.o.SystemDir)
	romChanged := strings.Join(config.DirPaths(rom), "\x00") != strings.Join(config.DirPaths(a.romDirs), "\x00") || a.games.list == nil
	sysChanged := sys.Path != a.sysDir.Path || a.systems.files == nil
	a.romDirs, a.sysDir = rom, sys
	a.loadStats()
	if romChanged {
		a.rescan()
	}
	if sysChanged || romChanged {
		a.rescanSystems()
	}
	a.readLogs()
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

func (a *app) menuH() int32 { return a.px(30) }

// area is the page's scrolled rectangle.
func (a *app) area() (x, y, w, h int32) {
	top := a.menuH() + a.toolbarH()
	return 0, top, a.w, a.h - top - a.statusH()
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
		switch e.WindowEvent() {
		case sdl.WindowEventSizeChanged:
			a.measure()
		case sdl.WindowEventFocusGained:
			// Back from another program (a download, a file manager,
			// a game): the folders may have changed.
			if a.o.Screenshot == "" {
				a.refresh()
			}
		}
	case sdl.EventDropFile:
		a.drop(e.DropFile())
		sdl.RaiseWindow(a.win)
	case sdl.EventTextInput:
		if a.focusArea != nil && a.popup == nil && a.dialog == nil {
			a.focusArea.insert(e.Text())
		} else if a.focus != nil && a.popup == nil && (a.dialog == nil || a.focus == a.dialog.field) {
			a.focus.insert(e.Text())
		}
	case sdl.EventKeyDown:
		sc, mod, _ := e.Key()
		a.key(sc, mod)
	case sdl.EventMouseMotion:
		x, y := e.MouseXY()
		a.mx, a.my = int32(float64(x)*a.ratio), int32(float64(y)*a.ratio)
		a.selDrag()
	case sdl.EventMouseButtonUp:
		a.tsel.drag = false
	case sdl.EventMouseWheel:
		if a.dialog != nil {
			a.dialog.scroll = max(0, a.dialog.scroll+int(e.Wheel())*3)
		} else if t := a.areaUnderMouse; t != nil && a.edit != nil {
			t.top = max(0, min(len(t.lines)-1, t.top-int(e.Wheel())*3))
			if t.row < t.top {
				t.row = t.top
			}
		} else if a.add != nil && a.add.page == 2 {
			a.add.scroll = max(0, a.add.scroll+int(e.Wheel())*3)
		} else if a.page == pageLogs && a.mx >= a.logSplit && a.popup == nil && a.setup == nil && a.add == nil {
			a.scrollLog(-e.Wheel() * a.px(72))
		} else if a.settingsOpen && a.popup == nil {
			a.scrollSettings(-e.Wheel() * a.px(72))
		} else if a.popup == nil && a.setup == nil && a.add == nil {
			a.scrollBy(-e.Wheel()*a.px(72), a.contentH())
		}
	case sdl.EventMouseButtonDown:
		x, y := e.MouseXY()
		a.mx, a.my = int32(float64(x)*a.ratio), int32(float64(y)*a.ratio)
		switch e.Button() {
		case 1:
			if h := a.selAt(); h != nil && a.popup == nil {
				a.selStart(h, e.Clicks())
				break
			}
			if a.popup == nil { // a menu over the selection keeps it
				a.tsel.active = false
			}
			a.click(e.Clicks() >= 2)
		case 3:
			if h := a.selAt(); h != nil && a.popup == nil {
				a.selMenu(h)
				break
			}
			a.rightClick()
		}
	}
}

func (a *app) key(sc int32, mod uint16) {
	ctrl := mod&sdl.KmodCtrl != 0
	if ctrl && sc == scC && a.tsel.active {
		a.copyText(a.selected_())
		return
	}
	if a.popup != nil {
		if sc == scEscape {
			a.popup = nil
		}
		return
	}
	if a.dialog != nil {
		if f := a.dialog.field; f != nil && a.focus == f && sc != scEscape && f.key(sc, ctrl) {
			return
		}
		a.dialogKey(sc)
		return
	}
	if a.setup != nil {
		a.setupKey(sc)
		return
	}
	if a.edit != nil && a.dialog == nil {
		a.editKey(sc, ctrl)
		return
	}
	if a.child != nil && a.dialog == nil {
		if a.popup == nil {
			a.childKey(sc, ctrl)
		}
		return
	}
	if a.exp != nil && a.dialog == nil {
		if a.popup == nil {
			a.exportKey(sc, ctrl)
		}
		return
	}
	if a.settingsOpen {
		if a.focus != nil && sc != scEscape && a.focus.key(sc, ctrl) {
			return
		}
		if sc == scEscape {
			if a.focus != nil {
				a.focus = nil
			} else {
				a.closeSettings()
			}
		}
		return
	}
	if a.add != nil {
		if a.focus != nil && sc != scEscape && a.focus.key(sc, ctrl) {
			return
		}
		a.addGameKey(sc)
		return
	}
	switch {
	case ctrl && sc == scN:
		a.openAddGame()
		return
	case ctrl && sc == scQ:
		a.quit = true
		return
	case ctrl && sc == sc1:
		a.show(pageDashboard)
		return
	case ctrl && sc == sc2:
		a.show(a.libView)
		return
	case ctrl && sc == sc3:
		a.show(pageLogs)
		return
	case ctrl && sc == scComma:
		a.openSettings()
		return
	case ctrl && sc == scO:
		a.openFile()
		return
	case ctrl && sc == scB:
		a.openBuildFolder()
		return
	case sc == scF5 || ctrl && sc == scR:
		a.refresh()
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
	case pageParents:
		a.parentsKey(sc)
	case pageLogs:
		a.logsKey(sc)
	}
}

// refresh rescans the game folders and the system directory, and reads
// the logs again.
func (a *app) refresh() {
	a.rescan()
	a.rescanSystems()
	a.readLogs()
}

// show switches to page p.
func (a *app) show(p page) {
	a.page, a.popup = p, nil
	if inLibrary(p) {
		a.libView = p
	}
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
	case pageParents:
		return a.parentsH()
	case pageDashboard:
		return a.dashboardH
	case pageLogs:
		return a.logsH()
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
	a.selHits = a.selHits[:0]
	a.areaUnderMouse = nil
	clear(a.selText)
	clear(a.selSeps)
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
	case pageParents:
		a.drawParents()
	case pageDashboard:
		a.drawDashboard()
	case pageLogs:
		a.drawLogs()
	}
	a.popClip()
	a.drawToolbar()
	a.drawStatus()
	a.drawSettingsDialog()
	a.drawEditor()
	a.drawExport()
	a.drawChild()
	a.drawSetup()
	a.drawAddGame()
	a.drawDialog()
	a.drawPopup()
}

func (a *app) drawToolbar() {
	a.drawMenuBar()
	mh, th := a.menuH(), a.toolbarH()
	a.fill(0, mh, a.w, th, colToolbar)
	a.fill(0, mh+th-a.px(1), a.w, a.px(1), colLine)
	p := a.pad()
	y := mh + (th-a.px(32))/2
	x := p
	tab := func(label string, on bool, pg page) {
		style := btnNormal
		if on {
			style = btnOn
		}
		x += a.button(label, x, y, style, func() { a.show(pg); a.refresh() }) + a.px(4)
	}
	sep := func() {
		x += a.px(16)
		a.fill(x-a.px(8), y+a.px(6), a.px(1), a.px(20), colLine)
	}
	tab("Dashboard", a.page == pageDashboard, pageDashboard)
	tab("Library", inLibrary(a.page), a.libView)
	tab("Logs", a.page == pageLogs, pageLogs)
	sep()
	if inLibrary(a.page) {
		// The Library's views, as one control: labels of fixed width.
		for _, v := range []struct {
			label string
			pg    page
		}{{"Games", pageGames}, {"Parents", pageParents}, {"Systems", pageSystems}} {
			style := btnNormal
			if a.page == v.pg {
				style = btnOn
			}
			pg := v.pg
			x += a.button(v.label, x, y, style, func() { a.show(pg) }) + a.px(2)
		}
		x += a.px(2)
		sep()
	}
	switch a.page {
	case pageGames:
		a.gamesToolbar(x, y)
	case pageSystems:
		a.systemsToolbar(x, y)
	case pageParents:
		a.parentsToolbar(x, y)
	case pageLogs:
		a.logsToolbar(x, y)
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
	case pageParents:
		left = plural(len(a.parentList), "parent")
	case pageDashboard:
		left = a.dashboardStatus()
	case pageLogs:
		left = a.logsStatus()
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
	Width      int32  `json:"width,omitempty"`
	Height     int32  `json:"height,omitempty"`
	Grid       bool   `json:"grid,omitempty"`
	Sort       string `json:"sort,omitempty"`
	Export     string `json:"export,omitempty"`      // the RetroDECK root Export last used
	ExportZip  string `json:"export_zip,omitempty"`  // the folder its last ZIP went in
	ExportFlat bool   `json:"export_flat,omitempty"` // its last ZIP had one roms folder
	Page       string `json:"page,omitempty"`        // the page shown last
}

func statePath() string {
	dir, err := config.StateDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "window.json")
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
	data, _ := json.MarshalIndent(guiState{Width: ww, Height: wh, Grid: a.grid, Sort: a.sort.String(), Export: a.exportDir, ExportZip: a.exportZipDir, ExportFlat: a.exportFlat, Page: pageNames[a.page]}, "", "  ")
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
	s, err := library.LoadStats(p, config.DirPaths(a.romDirs))
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
