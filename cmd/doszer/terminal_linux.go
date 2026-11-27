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
	return isTerminal(os.Stdin) || isTerminal(os.Stdout)
}

// isTerminal reports whether f is a terminal (not merely a character
// device, as /dev/null is).
func isTerminal(f *os.File) bool {
	var t syscall.Termios
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&t)))
	return e == 0
}

// detached is never set off Windows: there is no console to free.
var detached bool

// showError does nothing off Windows: doszer has no console of its own
// to lose there.
func showError(error) {}
