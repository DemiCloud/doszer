//go:build !windows

package main

import "errors"

// associate is Windows only: elsewhere, a desktop file opens .dosz files
// with `doszer %f`.
func associate(string, bool, bool) ([]string, error) {
	return nil, errors.New("associate: Windows only (elsewhere, a desktop file can open .dosz files with `doszer %f`)")
}
