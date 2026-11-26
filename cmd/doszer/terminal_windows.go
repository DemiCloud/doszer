package main

import (
	"syscall"
	"unsafe"
)

var (
	kernel32              = syscall.NewLazyDLL("kernel32.dll")
	getConsoleProcessList = kernel32.NewProc("GetConsoleProcessList")
	freeConsole           = kernel32.NewProc("FreeConsole")
	messageBox            = syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW")
)

// detached is set once doszer has freed a console of its own: errors are
// then shown in a message box (showError).
var detached bool

// fromTerminal reports whether doszer was started from a console it
// shares with its parent (cmd, PowerShell, Windows Terminal). Started
// from Explorer or a shortcut, Windows gave doszer a console of its own,
// with doszer its only process: that console is closed, since a window
// (doszer's, or the game's) replaces it.
func fromTerminal() bool {
	var pids [2]uint32
	n, _, _ := getConsoleProcessList.Call(uintptr(unsafe.Pointer(&pids[0])), 2)
	if n == 1 {
		freeConsole.Call()
		detached = true
		return false
	}
	return true
}

// showError shows err in a message box.
func showError(err error) {
	text, _ := syscall.UTF16PtrFromString(err.Error())
	title, _ := syscall.UTF16PtrFromString("doszer")
	const mbIconError = 0x10
	messageBox.Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), mbIconError)
}
