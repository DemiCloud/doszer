// Package libretro hosts a libretro core in doszer's own process: the
// built-in frontend `install` runs DOSBox Pure under. It loads the core
// and SDL2 at run time through purego (no cgo), answers the core's
// environment calls itself - directories, core options, a directory-only
// VFS - and shows the core's frames in an SDL window, with keyboard,
// mouse and audio.
//
// Only what DOSBox Pure asks of a frontend is implemented (DOSBox Pure
// 1.0 Preview 8, dosbox_pure_libretro.cpp; libretro.h from its
// libretro-common). One core runs per process: libretro's callbacks carry
// no context, so the running core's state is a package variable.
package libretro

import (
	"errors"
	"fmt"
	"math"
	"os"
	"runtime"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"

	"github.com/DemiCloud/doszer/internal/dl"
	"github.com/DemiCloud/doszer/internal/sdl"
)

// Config is one run of a core.
type Config struct {
	Core      string            // the core library
	SDL       []string          // SDL2 libraries to load, in order, the last SDL2 itself; empty for the system's
	Content   string            // content to load
	SystemDir string            // RETRO_ENVIRONMENT_GET_SYSTEM_DIRECTORY
	SaveDir   string            // RETRO_ENVIRONMENT_GET_SAVE_DIRECTORY
	Options   map[string]string // core option values; any other option takes the core's default
	Title     string            // window title, followed by the mouse capture state
	Frames    int               // stop after this many frames, unpaced (tests); 0 runs until closed
	// OnFrame, if set, is called with each frame the core draws: XRGB8888
	// pixels, valid only during the call (tests: screenshots).
	OnFrame func(frame int, pix []byte, width, height, pitch int)
}

// libretro.h constants used.
const (
	apiVersion = 1

	envShutdown                = 7
	envGetSystemDirectory      = 9
	envSetPixelFormat          = 10
	envSetInputDescriptors     = 11
	envSetKeyboardCallback     = 12
	envSetDiskControlInterface = 13
	envSetHWRender             = 14
	envGetVariable             = 15
	envSetVariables            = 16
	envGetVariableUpdate       = 17
	envSetSupportNoGame        = 18
	envGetLogInterface         = 27
	envGetSaveDirectory        = 31
	envSetSystemAVInfo         = 32
	envSetControllerInfo       = 35
	envSetMemoryMaps           = 36 | experimental
	envSetGeometry             = 37
	envSetSupportAchievements  = 42 | experimental
	envGetVFSInterface         = 45 | experimental
	envGetCoreOptionsVersion   = 52
	envSetCoreOptions          = 53
	envSetCoreOptionsDisplay   = 55
	envSetDiskControlExt       = 58
	envSetMessageExt           = 60
	envSetCoreOptionsV2        = 67
	experimental               = 0x10000

	pixelFormatXRGB8888 = 1

	deviceMouse    = 2
	deviceKeyboard = 3

	mouseX, mouseY, mouseLeft, mouseRight, mouseWheelUp, mouseWheelDown, mouseMiddle = 0, 1, 2, 3, 4, 5, 6

	logWarn = 2
)

// avInfo is struct retro_system_av_info.
type avInfo struct {
	geometry
	_          uint32
	FPS        float64
	SampleRate float64
}

// geometry is struct retro_game_geometry.
type geometry struct {
	BaseWidth, BaseHeight, MaxWidth, MaxHeight uint32
	Aspect                                     float32
}

// gameInfo is struct retro_game_info.
type gameInfo struct {
	Path *byte
	Data unsafe.Pointer
	Size uintptr
	Meta *byte
}

// core is the loaded core's API.
var core struct {
	apiVersion          func() uint32
	setEnvironment      func(uintptr)
	setVideoRefresh     func(uintptr)
	setAudioSample      func(uintptr)
	setAudioSampleBatch func(uintptr)
	setInputPoll        func(uintptr)
	setInputState       func(uintptr)
	init                func()
	deinit              func()
	getSystemAVInfo     func(*avInfo)
	loadGame            func(*gameInfo) bool
	unloadGame          func()
	run                 func()
}

// host is the running core's frontend state.
type host struct {
	sysDir, saveDir []byte
	options         map[string][]byte

	keyboard func(down bool, keycode, character uint32, mods uint16)
	quit     bool

	av       avInfo
	window   uintptr
	title    string
	renderer uintptr
	texture  uintptr
	texW     uint32
	texH     uint32

	// prescale is the frame scaled up by whole pixels (nearest), drawn
	// to the window smoothed (linear) for the remaining fraction: sharp
	// pixels without uneven ones at any window size.
	prescale   uintptr
	preW, preH int32
	fullscreen bool
	audio      uint32

	keys                  [retrokLast]bool
	captured              bool
	accX, accY, accWheel  int32
	mouseX, mouseY, wheel int32
	buttons               [8]bool

	vfs vfsState

	onFrame func(frame int, pix []byte, width, height, pitch int)
	frame   int
}

var h *host

// callbacks are created once: purego never frees them.
var callbacks struct {
	environment, videoRefresh, audioSample, audioSampleBatch, inputPoll, inputState, log uintptr
}

// Run loads cfg.Core, runs cfg.Content in a window until it is closed or
// the core shuts down, then unloads the content - which writes DOSBox
// Pure's save files - and the core.
func Run(cfg Config) error {
	if h != nil {
		return errors.New("libretro: a core is already running")
	}
	// The core and SDL are driven from one OS thread throughout.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if err := loadSDL(cfg.SDL); err != nil {
		return err
	}
	if err := loadCore(cfg.Core); err != nil {
		return err
	}
	if v := core.apiVersion(); v != apiVersion {
		return fmt.Errorf("%s: libretro API version %d, want %d", cfg.Core, v, apiVersion)
	}

	h = &host{
		sysDir:  cString(cfg.SystemDir),
		saveDir: cString(cfg.SaveDir),
		options: map[string][]byte{},
		onFrame: cfg.OnFrame,
	}
	defer func() { h = nil }()
	for k, v := range cfg.Options {
		h.options[k] = cString(v)
	}
	registerCallbacks()

	core.init()
	defer core.deinit()
	path := cString(cfg.Content)
	if !core.loadGame(&gameInfo{Path: &path[0]}) {
		return fmt.Errorf("DOSBox Pure could not load %s", cfg.Content)
	}
	defer core.unloadGame()
	core.getSystemAVInfo(&h.av)

	// Per-monitor DPI aware, so Windows doesn't stretch (and blur) the
	// window on a scaled display.
	sdl.SetHint("SDL_WINDOWS_DPI_AWARENESS", "permonitorv2")
	if sdl.Init(sdl.InitVideo|sdl.InitAudio|sdl.InitEvents) != 0 {
		return sdl.Error("SDL_Init")
	}
	defer sdl.Quit()
	var usable sdl.Rect
	if sdl.GetDisplayUsableBounds(0, &usable) != 0 {
		usable = sdl.Rect{W: 1600, H: 1000}
	}
	w, ht := windowSize(h.av.geometry, usable)
	h.title = cfg.Title
	// Hidden until the renderer exists: an OpenGL renderer recreates a
	// window that wasn't made for it, which showed it twice.
	window := sdl.CreateWindow(h.title, sdl.WindowPosCentered, sdl.WindowPosCentered, w, ht, sdl.WindowHidden|sdl.WindowResizable|sdl.WindowAllowHighDPI)
	if window == 0 {
		return sdl.Error("SDL_CreateWindow")
	}
	defer sdl.DestroyWindow(window)
	h.window = window
	setIcon(window)
	if h.renderer = sdl.CreateRenderer(window, -1, sdl.RendererAccelerated); h.renderer == 0 {
		if h.renderer = sdl.CreateRenderer(window, -1, 0); h.renderer == 0 {
			return sdl.Error("SDL_CreateRenderer")
		}
	}
	defer sdl.DestroyRenderer(h.renderer)
	defer func() {
		for _, t := range []uintptr{h.texture, h.prescale} {
			if t != 0 {
				sdl.DestroyTexture(t)
			}
		}
	}()
	// The host cursor is never shown over the window, as in RetroArch and
	// standalone DOSBox Pure: the guest draws its own. A transparent
	// cursor, not SDL_ShowCursor: WSLg ignored the latter.
	var blank [8]byte
	if c := sdl.CreateCursor(&blank[0], &blank[0], 8, 8, 0, 0); c != 0 {
		sdl.SetCursor(c)
		defer sdl.FreeCursor(c)
	} else {
		sdl.ShowCursor(0)
	}
	h.draw()
	sdl.ShowWindow(window)
	h.openAudio()
	defer func() {
		if h.audio != 0 {
			sdl.CloseAudioDevice(h.audio)
		}
	}()

	next := time.Now()
	for frame := 1; !h.quit; frame++ {
		h.frame = frame
		var e sdl.Event
		for sdl.PollEvent(&e) != 0 {
			h.event(&e, window)
		}
		if h.quit {
			break
		}
		core.run()
		h.draw()
		if cfg.Frames > 0 {
			if frame >= cfg.Frames {
				break
			}
			continue
		}
		next = h.pace(next)
	}
	return nil
}

func loadSDL(libs []string) error {
	if len(libs) == 0 {
		libs = []string{"libSDL2-2.0.so.0"}
	}
	var handle uintptr
	for _, lib := range libs {
		var err error
		if handle, err = dl.Open(lib); err != nil {
			return fmt.Errorf("SDL2 (needed for the built-in frontend): %w", err)
		}
	}
	return sdl.Load(handle, dl.Sym)
}

func loadCore(path string) error {
	handle, err := dl.Open(path)
	if err != nil {
		return fmt.Errorf("DOSBox Pure core: %w", err)
	}
	for _, f := range []struct {
		ptr  any
		name string
	}{
		{&core.apiVersion, "retro_api_version"},
		{&core.setEnvironment, "retro_set_environment"},
		{&core.setVideoRefresh, "retro_set_video_refresh"},
		{&core.setAudioSample, "retro_set_audio_sample"},
		{&core.setAudioSampleBatch, "retro_set_audio_sample_batch"},
		{&core.setInputPoll, "retro_set_input_poll"},
		{&core.setInputState, "retro_set_input_state"},
		{&core.init, "retro_init"},
		{&core.deinit, "retro_deinit"},
		{&core.getSystemAVInfo, "retro_get_system_av_info"},
		{&core.loadGame, "retro_load_game"},
		{&core.unloadGame, "retro_unload_game"},
		{&core.run, "retro_run"},
	} {
		p, err := dl.Sym(handle, f.name)
		if err != nil {
			return fmt.Errorf("%s: %s: %w", path, f.name, err)
		}
		purego.RegisterFunc(f.ptr, p)
	}
	return nil
}

func registerCallbacks() {
	c := &callbacks
	if c.environment == 0 {
		c.environment = purego.NewCallback(environment)
		c.videoRefresh = purego.NewCallback(videoRefresh)
		c.audioSample = purego.NewCallback(audioSample)
		c.audioSampleBatch = purego.NewCallback(audioSampleBatch)
		c.inputPoll = purego.NewCallback(inputPoll)
		c.inputState = purego.NewCallback(inputState)
		c.log = purego.NewCallback(logMessage)
		registerVFS()
	}
	core.setEnvironment(c.environment)
	core.setVideoRefresh(c.videoRefresh)
	core.setAudioSample(c.audioSample)
	core.setAudioSampleBatch(c.audioSampleBatch)
	core.setInputPoll(c.inputPoll)
	core.setInputState(c.inputState)
}

// windowSize is the window's starting size: the core's frame at its
// aspect ratio, at the largest whole multiple that fits 95% of the
// display's usable area, or shrunk to fit it.
func windowSize(g geometry, usable sdl.Rect) (int32, int32) {
	ht := int32(g.BaseHeight)
	if ht == 0 {
		ht = 480
	}
	w := int32(math.Round(float64(ht) * aspect(g, uint32(ht))))
	maxW, maxH := usable.W*95/100, usable.H*95/100
	if maxW <= 0 || maxH <= 0 {
		return w, ht
	}
	n := int32(1)
	for w*(n+1) <= maxW && ht*(n+1) <= maxH {
		n++
	}
	w, ht = w*n, ht*n
	if w > maxW || ht > maxH {
		r := fit(maxW, maxH, float64(w)/float64(ht))
		w, ht = r.W, r.H
	}
	return w, ht
}

// fit is the largest rectangle of aspect a centred in w x h.
func fit(w, h int32, a float64) sdl.Rect {
	fw, fh := w, int32(math.Round(float64(w)/a))
	if fh > h {
		fw, fh = int32(math.Round(float64(h)*a)), h
	}
	return sdl.Rect{X: (w - fw) / 2, Y: (h - fh) / 2, W: fw, H: fh}
}

// draw shows the current frame centred at the core's aspect ratio on
// black: scaled up by whole pixels into prescale, then to the window.
func (h *host) draw() {
	r := h.renderer
	sdl.SetRenderTarget(r, 0)
	sdl.SetRenderDrawColor(r, 0, 0, 0, 255)
	sdl.RenderClear(r)
	if h.texture != 0 {
		var ow, oh int32
		sdl.GetRendererOutputSize(r, &ow, &oh)
		dst := fit(ow, oh, aspect(h.av.geometry, h.texH))
		kx, ky := max(1, dst.W/int32(h.texW)), max(1, dst.H/int32(h.texH))
		pw, ph := int32(h.texW)*kx, int32(h.texH)*ky
		if h.prescale == 0 || pw != h.preW || ph != h.preH {
			if h.prescale != 0 {
				sdl.DestroyTexture(h.prescale)
			}
			h.prescale = sdl.CreateTexture(r, sdl.PixelFormatRGB888, sdl.TextureAccessTarget, pw, ph)
			if h.prescale != 0 {
				sdl.SetTextureScaleMode(h.prescale, sdl.ScaleModeLinear)
			}
			h.preW, h.preH = pw, ph
		}
		src := h.texture
		if h.prescale != 0 && sdl.SetRenderTarget(r, h.prescale) == 0 {
			sdl.RenderCopy(r, h.texture, nil, nil)
			sdl.SetRenderTarget(r, 0)
			src = h.prescale
		}
		sdl.RenderCopy(r, src, nil, unsafe.Pointer(&dst))
	}
	sdl.RenderPresent(r)
}

func aspect(g geometry, height uint32) float64 {
	if g.Aspect > 0 {
		return float64(g.Aspect)
	}
	if g.BaseWidth > 0 && height > 0 {
		return float64(g.BaseWidth) / float64(height)
	}
	return 4.0 / 3
}

// openAudio opens an audio device at the core's sample rate; without one
// the session runs silent.
func (h *host) openAudio() {
	if h.av.SampleRate <= 0 {
		return
	}
	want := sdl.AudioSpec{Freq: int32(math.Round(h.av.SampleRate)), Format: sdl.AudioS16LSB, Channels: 2, Samples: 1024}
	var got sdl.AudioSpec
	if h.audio = sdl.OpenAudioDevice(nil, 0, &want, &got, 0); h.audio != 0 {
		sdl.PauseAudioDevice(h.audio, 0)
	}
}

// pace holds each frame to the core's frame rate: by the audio queue when
// there is audio (the device consumes it at exactly the core's sample
// rate), else by the clock.
func (h *host) pace(next time.Time) time.Time {
	fps := h.av.FPS
	if fps <= 0 {
		fps = 60
	}
	frame := time.Duration(float64(time.Second) / fps)
	if h.audio != 0 {
		// About three frames of audio queued: enough not to underrun.
		target := uint32(h.av.SampleRate * 4 * 3 / fps)
		for !h.quit && sdl.GetQueuedAudioSize(h.audio) > target {
			time.Sleep(time.Millisecond)
		}
		return time.Now()
	}
	next = next.Add(frame)
	if d := time.Until(next); d > 0 {
		time.Sleep(d)
	} else if d < -frame*5 {
		next = time.Now() // fell far behind: don't rush to catch up
	}
	return next
}

// environment is retro_environment_t.
func environment(cmd uint32, data unsafe.Pointer) uintptr {
	switch cmd {
	case envGetSystemDirectory:
		*(**byte)(data) = &h.sysDir[0]
	case envGetSaveDirectory:
		*(**byte)(data) = &h.saveDir[0]
	case envSetPixelFormat:
		return boolResult(*(*int32)(data) == pixelFormatXRGB8888)
	case envGetVariable:
		v := (*[2]*byte)(data) // struct retro_variable { key, value }
		val, ok := h.options[goString(v[0])]
		if !ok {
			v[1] = nil
			return 0
		}
		v[1] = &val[0]
	case envGetVariableUpdate:
		*(*bool)(data) = false
	case envGetCoreOptionsVersion:
		*(*uint32)(data) = 2
	case envSetVariables, envSetCoreOptions, envSetCoreOptionsV2, envSetCoreOptionsDisplay:
		// Accepted and ignored: an option not in Config.Options reads
		// as unset, and the core uses its default.
	case envSetKeyboardCallback:
		fn := *(*uintptr)(data) // struct retro_keyboard_callback { callback }
		purego.RegisterFunc(&h.keyboard, fn)
	case envShutdown:
		h.quit = true
	case envGetLogInterface:
		*(*uintptr)(data) = callbacks.log
	case envGetVFSInterface:
		info := (*struct {
			Version uint32
			Iface   unsafe.Pointer
		})(data)
		if info.Version > 3 {
			return 0
		}
		info.Version, info.Iface = 3, unsafe.Pointer(&vfsTable[0])
	case envSetSystemAVInfo:
		h.av = *(*avInfo)(data)
	case envSetGeometry:
		h.av.geometry = *(*geometry)(data)
		h.texW, h.texH = 0, 0 // re-derive the logical size
	case envSetSupportNoGame, envSetSupportAchievements, envSetInputDescriptors, envSetControllerInfo,
		envSetMemoryMaps, envSetDiskControlInterface, envSetDiskControlExt, envSetMessageExt:
		// Accepted, nothing to do.
	case envSetHWRender:
		return 0 // software rendering only
	default:
		return 0
	}
	return 1
}

// logMessage is retro_log_printf_t. It is variadic in C, but the level and
// format arrive like any first two arguments; the format's own arguments
// are not read. Only warnings and errors are shown.
func logMessage(level uint32, format unsafe.Pointer) uintptr {
	if level >= logWarn {
		fmt.Fprintf(os.Stderr, "[DOSBox Pure] %s", goString((*byte)(format)))
	}
	return 0
}

// videoRefresh is retro_video_refresh_t: frames are XRGB8888, SDL's RGB888.
func videoRefresh(data unsafe.Pointer, width, height uint32, pitch uintptr) uintptr {
	if data == nil || width == 0 || height == 0 {
		return 0 // the previous frame again
	}
	if width != h.texW || height != h.texH {
		if h.texture != 0 {
			sdl.DestroyTexture(h.texture)
		}
		h.texture = sdl.CreateTexture(h.renderer, sdl.PixelFormatRGB888, sdl.TextureAccessStreaming, int32(width), int32(height))
		if h.texture != 0 {
			sdl.SetTextureScaleMode(h.texture, sdl.ScaleModeNearest)
		}
		h.texW, h.texH = width, height
	}
	if h.texture != 0 {
		sdl.UpdateTexture(h.texture, nil, data, int32(pitch))
	}
	if h.onFrame != nil {
		h.onFrame(h.frame, unsafe.Slice((*byte)(data), int(pitch)*int(height)), int(width), int(height), int(pitch))
	}
	return 0
}

func audioSample(left, right int16) uintptr {
	s := [2]int16{left, right}
	audioSampleBatch(unsafe.Pointer(&s[0]), 1)
	return 0
}

// audioSampleBatch is retro_audio_sample_batch_t: interleaved stereo
// 16-bit frames. Audio far ahead of the device (the core can run faster
// than real time when the window can't keep up) is dropped.
func audioSampleBatch(data unsafe.Pointer, frames uintptr) uintptr {
	if h.audio != 0 && frames > 0 && sdl.GetQueuedAudioSize(h.audio) < uint32(h.av.SampleRate*4/2) {
		sdl.QueueAudio(h.audio, data, uint32(frames*4))
	}
	return frames
}

// inputPoll is retro_input_poll_t: the frame's mouse movement is what
// accumulated since the last poll.
func inputPoll() uintptr {
	h.mouseX, h.mouseY, h.wheel = h.accX, h.accY, h.accWheel
	h.accX, h.accY, h.accWheel = 0, 0, 0
	return 0
}

// inputState is retro_input_state_t: the keyboard and the mouse; no
// gamepad.
func inputState(port, device, index, id uint32) uintptr {
	if port != 0 {
		return 0
	}
	var v int16
	switch device & 0xFF {
	case deviceKeyboard:
		if id < retrokLast && h.keys[id] {
			v = 1
		}
	case deviceMouse:
		switch id {
		case mouseX:
			v = int16(h.mouseX)
		case mouseY:
			v = int16(h.mouseY)
		case mouseLeft:
			v = b2i(h.buttons[1])
		case mouseMiddle:
			v = b2i(h.buttons[2])
		case mouseRight:
			v = b2i(h.buttons[3])
		case mouseWheelUp:
			v = b2i(h.wheel > 0)
		case mouseWheelDown:
			v = b2i(h.wheel < 0)
		}
	}
	return uintptr(uint16(v))
}

// event handles one SDL event. Clicking the window captures the mouse
// (relative mode); Ctrl+F10 or leaving the window releases it.
func (h *host) event(e *sdl.Event, window uintptr) {
	switch e.Type() {
	case sdl.EventQuit:
		h.quit = true
	case sdl.EventWindow:
		if e.WindowEvent() == sdl.WindowEventFocusLost {
			h.capture(false)
		}
	case sdl.EventKeyDown, sdl.EventKeyUp:
		scancode, mod, repeat := e.Key()
		down := e.Type() == sdl.EventKeyDown
		if repeat {
			return
		}
		key := retrokFromScancode(scancode)
		if key == retrokReturn && mod&sdl.KmodAlt != 0 {
			if down {
				h.toggleFullscreen()
			}
			return
		}
		if key == retrokF10 && mod&sdl.KmodCtrl != 0 {
			if down {
				h.capture(false)
			}
			return
		}
		if key == 0 || key >= retrokLast {
			return
		}
		h.keys[key] = down
		if h.keyboard != nil {
			h.keyboard(down, key, 0, retroMods(mod))
		}
	case sdl.EventMouseMotion:
		if h.captured {
			x, y := e.Motion()
			h.accX += x
			h.accY += y
		}
	case sdl.EventMouseButtonDown, sdl.EventMouseButtonUp:
		down := e.Type() == sdl.EventMouseButtonDown
		if down && !h.captured {
			h.capture(true)
			return
		}
		if b := e.Button(); int(b) < len(h.buttons) {
			h.buttons[b] = down
		}
	case sdl.EventMouseWheel:
		h.accWheel += e.Wheel()
	}
}

// toggleFullscreen switches between the window and borderless
// fullscreen on its display (Alt+Enter).
func (h *host) toggleFullscreen() {
	var flags uint32
	if !h.fullscreen {
		flags = sdl.WindowFullscreenDesktop
	}
	if sdl.SetWindowFullscreen(h.window, flags) == 0 {
		h.fullscreen = !h.fullscreen
	}
}

func (h *host) capture(on bool) {
	if on == h.captured {
		return
	}
	h.captured = on
	sdl.SetRelativeMouseMode(on)
	if !on {
		h.buttons = [8]bool{}
	}
}

// retroMods converts SDL modifier bits to libretro's RETROKMOD_*.
func retroMods(m uint16) uint16 {
	var r uint16
	for _, b := range []struct{ sdl, retro uint16 }{
		{sdl.KmodShift, 0x01}, {sdl.KmodCtrl, 0x02}, {sdl.KmodAlt, 0x04}, {sdl.KmodGUI, 0x08},
		{sdl.KmodNum, 0x10}, {sdl.KmodCaps, 0x20}, {sdl.KmodScroll, 0x40},
	} {
		if m&b.sdl != 0 {
			r |= b.retro
		}
	}
	return r
}

func boolResult(b bool) uintptr {
	if b {
		return 1
	}
	return 0
}

func b2i(b bool) int16 {
	if b {
		return 1
	}
	return 0
}

// cString is s NUL-terminated, for the core. It must stay referenced while
// the core may read it.
func cString(s string) []byte {
	return append([]byte(s), 0)
}

// goString copies a NUL-terminated string from the core.
func goString(p *byte) string {
	if p == nil {
		return ""
	}
	n := 0
	for *(*byte)(unsafe.Add(unsafe.Pointer(p), n)) != 0 {
		n++
	}
	return string(unsafe.Slice(p, n))
}
