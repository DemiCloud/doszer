package status

import (
	"os"
	"syscall"
	"unsafe"
)

var getConsoleScreenBufferInfo = syscall.NewLazyDLL("kernel32.dll").NewProc("GetConsoleScreenBufferInfo")

// termWidth is f's console width in columns, or 0 if unknown (e.g. not a
// console, as when run from a WSL shell).
func termWidth(f *os.File) int {
	var info struct {
		size, cursor          [2]int16
		attributes            uint16
		left, top, right, bot int16
		maxSize               [2]int16
	}
	if r, _, _ := getConsoleScreenBufferInfo.Call(f.Fd(), uintptr(unsafe.Pointer(&info))); r == 0 {
		return 0
	}
	return int(info.right-info.left) + 1
}
