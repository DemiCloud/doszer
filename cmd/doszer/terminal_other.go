//go:build !linux && !windows

package main

import "os"

// fromTerminal reports whether doszer was started from a terminal; where
// it can't tell, it assumes so.
func fromTerminal() bool { return true }

// isTerminal reports whether f is a character device: where doszer
// can't ask, the best guess.
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

var detached bool

func showError(error) {}
