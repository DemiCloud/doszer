//go:build !linux && !windows

package status

import "os"

// termWidth is unknown here: draw falls back to a default.
func termWidth(*os.File) int { return 0 }
