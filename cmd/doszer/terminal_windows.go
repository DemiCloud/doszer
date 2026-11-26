package main

import (
	"syscall"
	"unsafe"
)

var (
	kernel32              = syscall.NewLazyDLL("kernel32.dll")
	getConsoleProcessList = kernel32.NewProc("GetConsoleProcessList")
	freeConsole           = kernel32.NewProc("FreeConsole")
)

// fromTerminal reports whether doszer was started from a console it
// shares with its parent (cmd, PowerShell, Windows Terminal). Started
// from Explorer or a shortcut, Windows gave doszer a console of its own,
// with doszer its only process: that console is closed, since the window
// replaces it.
func fromTerminal() bool {
	var pids [2]uint32
	n, _, _ := getConsoleProcessList.Call(uintptr(unsafe.Pointer(&pids[0])), 2)
	if n == 1 {
		freeConsole.Call()
		return false
	}
	return true
}
