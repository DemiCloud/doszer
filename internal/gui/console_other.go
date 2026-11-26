//go:build !windows

package gui

import "os/exec"

// hideConsole does nothing: only Windows opens a console for a child.
func hideConsole(*exec.Cmd) {}
