package libretro

import (
	"fmt"
	"unsafe"

	"github.com/ebitengine/purego"

	"github.com/DemiCloud/doszer/internal/sdl"
)

// probeSwapAt is a frame after which to swap discs, and probeDisc the
// disc then in the drive, for TestProbe.
var (
	probeSwapAt int
	probeDisc   string
	// probeKeys, if set, presses keys after a frame (TestProbe).
	probeKeys func(frame int, press func(key uint32, down bool))
)

// diskControl is the core's struct retro_disk_control_ext_callback
// (libretro.h), the part used: DOSBox Pure lists every disc and floppy
// image in the content (dbp_images), the one in the drive by index
// (dosbox_pure_libretro.cpp, retro_init's disk_control_callback).
type diskControl struct {
	setEject   func(ejected bool) bool
	getEject   func() bool
	getIndex   func() uint32
	setIndex   func(index uint32) bool
	getNum     func() uint32
	labelFn    uintptr // get_image_label, or 0
	registered bool
}

// setDiskControl registers the core's callbacks: data points to the
// struct, ext when it is the _EXT one (with get_image_label).
func (h *host) setDiskControl(data unsafe.Pointer, ext bool) {
	fns := (*[10]uintptr)(data)
	d := &h.disk
	purego.RegisterFunc(&d.setEject, fns[0])
	purego.RegisterFunc(&d.getEject, fns[1])
	purego.RegisterFunc(&d.getIndex, fns[2])
	purego.RegisterFunc(&d.setIndex, fns[3])
	purego.RegisterFunc(&d.getNum, fns[4])
	if ext {
		d.labelFn = fns[9]
	}
	d.registered = true
}

// label is image i's label, as the core names it (DBP_Image_Label).
func (d *diskControl) label(i uint32) string {
	if d.labelFn == 0 {
		return ""
	}
	buf := make([]byte, 256)
	var get func(index uint32, label *byte, n uintptr) bool
	purego.RegisterFunc(&get, d.labelFn)
	if !get(i, &buf[0], uintptr(len(buf))) {
		return ""
	}
	return goString(&buf[0])
}

// nextDisc puts the next image in the drive (Ctrl+F4, DOSBox's swap
// key): eject, select, insert, as a frontend's disc menu does; the core
// tells the game the media changed (DBP_SetMountSwappingRequested). The
// window's title names the disc in the drive.
func (h *host) nextDisc() {
	d := &h.disk
	if !d.registered {
		return
	}
	n := d.getNum()
	if n < 2 {
		return
	}
	i := (d.getIndex() + 1) % n
	d.setEject(true)
	d.setIndex(i)
	d.setEject(false)
	h.disc = fmt.Sprintf("Disc %d of %d", i+1, n)
	if l := d.label(i); l != "" {
		h.disc += ": " + l
	}
	probeDisc = h.disc
	h.setTitle()
}

// setTitle shows the window's title, the disc swapped in and pausing.
func (h *host) setTitle() {
	t := h.title
	if h.disc != "" {
		t += " - " + h.disc
	}
	if h.paused {
		t += " (paused)"
	}
	sdl.SetWindowTitle(h.window, t)
}
