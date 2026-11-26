package main

import (
	"os"
	"syscall"
	"unsafe"
)

// fromTerminal reports whether doszer was started from a terminal: its
// standard input or output is one. Started from a desktop file manager
// or menu, neither is. Without a display, there is no window to open
// either.
func fromTerminal() bool {
	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		return true
	}
	for _, f := range []*os.File{os.Stdin, os.Stdout} {
		var t syscall.Termios
		if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&t))); e == 0 {
			return true
		}
	}
	return false
}

// detached is never set off Windows: there is no console to free.
var detached bool

// showError does nothing off Windows: doszer has no console of its own
// to lose there.
func showError(error) {}
