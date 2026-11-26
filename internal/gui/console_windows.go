package gui

import (
	"os/exec"
	"syscall"
)

// hideConsole keeps a game's `doszer run` from opening a console window:
// doszer is a console program, and the window has none to share.
func hideConsole(cmd *exec.Cmd) {
	const createNoWindow = 0x08000000
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
}
