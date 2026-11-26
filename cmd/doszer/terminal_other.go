//go:build !linux && !windows

package main

// fromTerminal reports whether doszer was started from a terminal; where
// it can't tell, it assumes so.
func fromTerminal() bool { return true }

var detached bool

func showError(error) {}
