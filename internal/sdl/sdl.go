// Package sdl is the small part of SDL2 doszer's built-in frontend uses
// (internal/libretro): a window and renderer for the core's frames,
// keyboard and mouse events, and queued audio. It loads SDL2 at run time
// through purego, so doszer itself builds without cgo: the system's
// libSDL2-2.0.so.0 on Linux, a given SDL2.dll on Windows.
package sdl

import (
	"errors"
	"fmt"
	"unsafe"

	"github.com/ebitengine/purego"
)

// Subsystems, window and renderer flags, pixel and audio formats (SDL.h,
// SDL_video.h, SDL_render.h, SDL_pixels.h, SDL_audio.h).
const (
	InitVideo  = 0x00000020
	InitAudio  = 0x00000010
	InitEvents = 0x00004000

	WindowPosCentered  = 0x2FFF0000
	WindowShown        = 0x00000004
	WindowHidden       = 0x00000008
	WindowResizable    = 0x00000020
	WindowAllowHighDPI = 0x00002000

	RendererAccelerated = 0x00000002

	WindowFullscreenDesktop = 0x00001001

	ScaleModeNearest = 0
	ScaleModeLinear  = 1

	PixelFormatRGB888      = 0x16161804 // XRGB8888, libretro's RETRO_PIXEL_FORMAT_XRGB8888
	TextureAccessStreaming = 1
	TextureAccessTarget    = 2

	AudioS16LSB = 0x8010
)

// Event types (SDL_events.h).
const (
	EventQuit            = 0x100
	EventWindow          = 0x200
	EventKeyDown         = 0x300
	EventKeyUp           = 0x301
	EventMouseMotion     = 0x400
	EventMouseButtonDown = 0x401
	EventMouseButtonUp   = 0x402
	EventMouseWheel      = 0x403

	WindowEventFocusLost = 13
)

// Modifier bits (SDL_keycode.h, SDL_Keymod).
const (
	KmodShift  = 0x0003
	KmodCtrl   = 0x00C0
	KmodAlt    = 0x0300
	KmodGUI    = 0x0C00
	KmodNum    = 0x1000
	KmodCaps   = 0x2000
	KmodScroll = 0x8000
)

// Event is SDL_Event: a 56-byte union, read through its accessors.
type Event [56]byte

func (e *Event) u32(off int) uint32 { return *(*uint32)(unsafe.Pointer(&e[off])) }
func (e *Event) i32(off int) int32  { return *(*int32)(unsafe.Pointer(&e[off])) }

// Type is the event's type.
func (e *Event) Type() uint32 { return e.u32(0) }

// WindowEvent is a window event's kind (SDL_WindowEvent.event).
func (e *Event) WindowEvent() uint8 { return e[12] }

// Key is a keyboard event's scancode, modifiers and whether it repeats
// (SDL_KeyboardEvent).
func (e *Event) Key() (scancode int32, mod uint16, repeat bool) {
	return e.i32(16), *(*uint16)(unsafe.Pointer(&e[24])), e[13] != 0
}

// Button is a mouse button event's button: 1 left, 2 middle, 3 right
// (SDL_MouseButtonEvent).
func (e *Event) Button() uint8 { return e[16] }

// Motion is a mouse motion event's relative movement (SDL_MouseMotionEvent).
func (e *Event) Motion() (xrel, yrel int32) { return e.i32(28), e.i32(32) }

// Wheel is a mouse wheel event's vertical movement (SDL_MouseWheelEvent).
func (e *Event) Wheel() int32 { return e.i32(20) }

// Rect is SDL_Rect.
type Rect struct{ X, Y, W, H int32 }

// AudioSpec is SDL_AudioSpec.
type AudioSpec struct {
	Freq     int32
	Format   uint16
	Channels uint8
	Silence  uint8
	Samples  uint16
	_        uint16
	Size     uint32
	Callback uintptr
	Userdata uintptr
}

// The SDL functions used, bound by Load.
var (
	Init                   func(flags uint32) int32
	Quit                   func()
	GetError               func() string
	SetHint                func(name, value string) bool
	CreateWindow           func(title string, x, y, w, h int32, flags uint32) uintptr
	DestroyWindow          func(w uintptr)
	SetWindowTitle         func(w uintptr, title string)
	ShowWindow             func(w uintptr)
	SetWindowFullscreen    func(w uintptr, flags uint32) int32
	GetWindowFlags         func(w uintptr) uint32
	GetDisplayUsableBounds func(display int32, r *Rect) int32
	GetRendererOutputSize  func(r uintptr, w, h *int32) int32
	SetRenderTarget        func(r uintptr, t uintptr) int32
	SetRenderDrawColor     func(r uintptr, red, green, blue, alpha uint8) int32
	SetTextureScaleMode    func(t uintptr, mode int32) int32
	CreateRenderer         func(w uintptr, index int32, flags uint32) uintptr
	DestroyRenderer        func(r uintptr)
	RenderClear            func(r uintptr) int32
	RenderCopy             func(r uintptr, t uintptr, src, dst unsafe.Pointer) int32
	RenderPresent          func(r uintptr)
	CreateTexture          func(r uintptr, format uint32, access, w, h int32) uintptr
	DestroyTexture         func(t uintptr)
	UpdateTexture          func(t uintptr, rect unsafe.Pointer, pixels unsafe.Pointer, pitch int32) int32
	PollEvent              func(e *Event) int32
	SetRelativeMouseMode   func(enabled bool) int32
	ShowCursor             func(toggle int32) int32
	CreateCursor           func(data, mask *byte, w, h, hotX, hotY int32) uintptr
	SetCursor              func(c uintptr)
	FreeCursor             func(c uintptr)
	OpenAudioDevice        func(device unsafe.Pointer, iscapture int32, desired, obtained *AudioSpec, allowedChanges int32) uint32
	CloseAudioDevice       func(dev uint32)
	PauseAudioDevice       func(dev uint32, pause int32)
	QueueAudio             func(dev uint32, data unsafe.Pointer, len uint32) int32
	GetQueuedAudioSize     func(dev uint32) uint32
	ClearQueuedAudio       func(dev uint32)
)

var loaded bool

// Load binds SDL2's functions from the library open returns a handle to.
func Load(handle uintptr, sym func(uintptr, string) (uintptr, error)) (err error) {
	if loaded {
		return nil
	}
	bind := func(fptr any, name string) {
		if err != nil {
			return
		}
		var p uintptr
		if p, err = sym(handle, name); err != nil {
			err = fmt.Errorf("SDL2: %s: %w", name, err)
			return
		}
		purego.RegisterFunc(fptr, p)
	}
	bind(&Init, "SDL_Init")
	bind(&Quit, "SDL_Quit")
	bind(&GetError, "SDL_GetError")
	bind(&SetHint, "SDL_SetHint")
	bind(&CreateWindow, "SDL_CreateWindow")
	bind(&DestroyWindow, "SDL_DestroyWindow")
	bind(&SetWindowTitle, "SDL_SetWindowTitle")
	bind(&ShowWindow, "SDL_ShowWindow")
	bind(&SetWindowFullscreen, "SDL_SetWindowFullscreen")
	bind(&GetWindowFlags, "SDL_GetWindowFlags")
	bind(&GetDisplayUsableBounds, "SDL_GetDisplayUsableBounds")
	bind(&GetRendererOutputSize, "SDL_GetRendererOutputSize")
	bind(&SetRenderTarget, "SDL_SetRenderTarget")
	bind(&SetRenderDrawColor, "SDL_SetRenderDrawColor")
	bind(&SetTextureScaleMode, "SDL_SetTextureScaleMode")
	bind(&CreateRenderer, "SDL_CreateRenderer")
	bind(&DestroyRenderer, "SDL_DestroyRenderer")
	bind(&RenderClear, "SDL_RenderClear")
	bind(&RenderCopy, "SDL_RenderCopy")
	bind(&RenderPresent, "SDL_RenderPresent")
	bind(&CreateTexture, "SDL_CreateTexture")
	bind(&DestroyTexture, "SDL_DestroyTexture")
	bind(&UpdateTexture, "SDL_UpdateTexture")
	bind(&PollEvent, "SDL_PollEvent")
	bind(&SetRelativeMouseMode, "SDL_SetRelativeMouseMode")
	bind(&ShowCursor, "SDL_ShowCursor")
	bind(&CreateCursor, "SDL_CreateCursor")
	bind(&SetCursor, "SDL_SetCursor")
	bind(&FreeCursor, "SDL_FreeCursor")
	bind(&OpenAudioDevice, "SDL_OpenAudioDevice")
	bind(&CloseAudioDevice, "SDL_CloseAudioDevice")
	bind(&PauseAudioDevice, "SDL_PauseAudioDevice")
	bind(&QueueAudio, "SDL_QueueAudio")
	bind(&GetQueuedAudioSize, "SDL_GetQueuedAudioSize")
	bind(&ClearQueuedAudio, "SDL_ClearQueuedAudio")
	if err == nil {
		loaded = true
	}
	return err
}

// Error wraps SDL's last error message.
func Error(what string) error {
	msg := GetError()
	if msg == "" {
		return errors.New(what)
	}
	return fmt.Errorf("%s: %s", what, msg)
}
