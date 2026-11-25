package status

import (
	"os"
	"syscall"
	"unsafe"
)

// termWidth is f's terminal width in columns, or 0 if unknown.
func termWidth(f *os.File) int {
	var ws struct{ row, col, x, y uint16 }
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(&ws))); e != 0 {
		return 0
	}
	return int(ws.col)
}
