// Package gamepad reads game controllers through SDL's GameController
// API, for the built-in player (libretro's RetroPad, by port) and the
// window's gamepad editor (which control a press means). Buttons are
// positional - SDL's A is the bottom face button on any controller,
// which is the RetroPad's B (libretro.h, RETRO_DEVICE_ID_JOYPAD_*) and
// [[input.pad]]'s b (spec §5.9) - so a Nintendo controller's labels
// don't swap them (SDL_GAMECONTROLLER_USE_BUTTON_LABELS off).
package gamepad

import (
	"github.com/DemiCloud/doszer/internal/sdl"
)

// SDL_GameControllerButton and SDL_GameControllerAxis values.
const (
	buttonA, buttonB, buttonX, buttonY                            = 0, 1, 2, 3
	buttonBack, buttonGuide, buttonStart                          = 4, 5, 6
	buttonLeftStick, buttonRightStick                             = 7, 8
	buttonLeftShoulder, buttonRightShoulder                       = 9, 10
	buttonDpadUp, buttonDpadDown, buttonDpadLeft, buttonDpadRight = 11, 12, 13, 14

	axisLeftX, axisLeftY, axisRightX, axisRightY, axisTriggerLeft, axisTriggerRight = 0, 1, 2, 3, 4, 5
)

// TriggerDown is how far a trigger (0 to 32767) goes before it counts as
// pressed: the RetroPad's L2 and R2 are buttons too.
const TriggerDown = 8192

// retroButtons are the RetroPad's buttons by ID (B, Y, Select, Start, Up,
// Down, Left, Right, A, X, L, R, L2, R2, L3, R3) as SDL buttons; -1 for
// L2 and R2, which are SDL's triggers.
var retroButtons = [16]int32{
	buttonA, buttonX, buttonBack, buttonStart,
	buttonDpadUp, buttonDpadDown, buttonDpadLeft, buttonDpadRight,
	buttonB, buttonY, buttonLeftShoulder, buttonRightShoulder,
	-1, -1, buttonLeftStick, buttonRightStick,
}

// controlButtons are [[input.pad]]'s controls by SDL button.
var controlButtons = map[uint8]string{
	buttonA: "b", buttonB: "a", buttonX: "y", buttonY: "x",
	buttonBack: "select", buttonStart: "start",
	buttonLeftStick: "l3", buttonRightStick: "r3",
	buttonLeftShoulder: "l", buttonRightShoulder: "r",
	buttonDpadUp: "up", buttonDpadDown: "down", buttonDpadLeft: "left", buttonDpadRight: "right",
}

// controlAxes are [[input.pad]]'s controls by SDL axis: the negative
// direction's, then the positive's (a trigger has only the latter).
var controlAxes = map[uint8][2]string{
	axisLeftX: {"lstick_left", "lstick_right"}, axisLeftY: {"lstick_up", "lstick_down"},
	axisRightX: {"rstick_left", "rstick_right"}, axisRightY: {"rstick_up", "rstick_down"},
	axisTriggerLeft: {"", "l2"}, axisTriggerRight: {"", "r2"},
}

// Pad is an open controller.
type Pad struct {
	ID   int32 // its joystick instance ID, as events name it
	Name string
	gc   uintptr
	zone [6]int8 // per axis, the side it last crossed half-way on: -1, 0, 1
}

// Set is the controllers connected, in the order they were: port 1
// first.
type Set struct {
	Pads []*Pad
}

// Open starts SDL's game controller subsystem (SDL must be initialized
// with InitEvents) and opens the controllers already connected; more
// arrive through Event. A system without one is no error: no pads.
func Open() *Set {
	s := &Set{}
	if sdl.InitSubSystem == nil {
		return s
	}
	sdl.SetHint("SDL_GAMECONTROLLER_USE_BUTTON_LABELS", "0")
	if sdl.InitSubSystem(sdl.InitJoystick|sdl.InitGameController) != 0 {
		return s
	}
	for i := int32(0); i < sdl.NumJoysticks(); i++ {
		s.add(i)
	}
	return s
}

func (s *Set) add(index int32) *Pad {
	if sdl.IsGameController(index) == 0 {
		return nil
	}
	gc := sdl.GameControllerOpen(index)
	if gc == 0 {
		return nil
	}
	id := sdl.JoystickInstanceID(sdl.GameControllerGetJoystick(gc))
	for _, p := range s.Pads {
		if p.ID == id {
			sdl.GameControllerClose(gc) // opened again: SDL counts opens
			return p
		}
	}
	p := &Pad{ID: id, Name: sdl.GameControllerName(gc), gc: gc}
	if p.Name == "" {
		p.Name = "Controller"
	}
	s.Pads = append(s.Pads, p)
	return p
}

// Close closes every controller.
func (s *Set) Close() {
	if s == nil {
		return
	}
	for _, p := range s.Pads {
		sdl.GameControllerClose(p.gc)
	}
	s.Pads = nil
}

// Event follows controllers connected and disconnected, reporting
// whether the set changed.
func (s *Set) Event(e *sdl.Event) bool {
	if s == nil {
		return false
	}
	switch e.Type() {
	case sdl.EventControllerDeviceAdded:
		return s.add(e.Which()) != nil
	case sdl.EventControllerDeviceRemove:
		for i, p := range s.Pads {
			if p.ID == e.Which() {
				sdl.GameControllerClose(p.gc)
				s.Pads = append(s.Pads[:i], s.Pads[i+1:]...)
				return true
			}
		}
	}
	return false
}

// Port is the controller on port n (0 for port 1), or nil.
func (s *Set) Port(n int) *Pad {
	if s == nil || n < 0 || n >= len(s.Pads) {
		return nil
	}
	return s.Pads[n]
}

// RetroButton reports whether the RetroPad button id is pressed.
func (p *Pad) RetroButton(id uint32) bool {
	if id >= 16 {
		return false
	}
	switch id {
	case 12:
		return sdl.GameControllerGetAxis(p.gc, axisTriggerLeft) >= TriggerDown
	case 13:
		return sdl.GameControllerGetAxis(p.gc, axisTriggerRight) >= TriggerDown
	}
	return sdl.GameControllerGetButton(p.gc, retroButtons[id]) != 0
}

// RetroAnalog is the RetroPad analog value libretro asks for: a stick's
// axis (index 0 left, 1 right; id 0 X, 1 Y), or an analog button's
// pressure (index 2: the triggers, L2 and R2; 0 for any other, which
// makes a core fall back to the button).
func (p *Pad) RetroAnalog(index, id uint32) int16 {
	switch index {
	case 0, 1:
		if id > 1 {
			return 0
		}
		return sdl.GameControllerGetAxis(p.gc, int32(index*2+id))
	case 2:
		switch id {
		case 12:
			return sdl.GameControllerGetAxis(p.gc, axisTriggerLeft)
		case 13:
			return sdl.GameControllerGetAxis(p.gc, axisTriggerRight)
		}
	}
	return 0
}

// Pressed reports whether [[input.pad]]'s control is held now: a button,
// a trigger past TriggerDown, or a stick half-way in its direction.
func (p *Pad) Pressed(control string) bool {
	for b, c := range controlButtons {
		if c == control {
			return sdl.GameControllerGetButton(p.gc, int32(b)) != 0
		}
	}
	for axis, cs := range controlAxes {
		v := sdl.GameControllerGetAxis(p.gc, int32(axis))
		switch control {
		case cs[0]:
			return v <= -16384
		case cs[1]:
			if axis == axisTriggerLeft || axis == axisTriggerRight {
				return v >= TriggerDown
			}
			return v >= 16384
		}
	}
	return false
}

// Control is the [[input.pad]] control an event presses, or "": a button
// going down, a stick crossing half-way into a direction, a trigger
// crossing TriggerDown. A stick or trigger is reported once per
// crossing, not for every movement past it.
func (s *Set) Control(e *sdl.Event) (pad *Pad, control string) {
	if s == nil {
		return nil, ""
	}
	switch e.Type() {
	case sdl.EventControllerButtonDown:
		p := s.byID(e.Which())
		if c, ok := controlButtons[e.ControllerButton()]; ok && p != nil {
			return p, c
		}
	case sdl.EventControllerAxisMotion:
		p := s.byID(e.Which())
		axis, v := e.ControllerAxis()
		cs, ok := controlAxes[axis]
		if p == nil || !ok || int(axis) >= len(p.zone) {
			return nil, ""
		}
		limit := int16(16384)
		if axis == axisTriggerLeft || axis == axisTriggerRight {
			limit = TriggerDown
		}
		zone := int8(0)
		switch {
		case v >= limit:
			zone = 1
		case v <= -limit:
			zone = -1
		}
		was := p.zone[axis]
		p.zone[axis] = zone
		if zone == was || zone == 0 {
			return nil, ""
		}
		if zone < 0 {
			return p, cs[0]
		}
		return p, cs[1]
	}
	return nil, ""
}

func (s *Set) byID(id int32) *Pad {
	for _, p := range s.Pads {
		if p.ID == id {
			return p
		}
	}
	return nil
}
