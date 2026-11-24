//go:build windows

package dl

import "syscall"

// Open loads the DLL at path.
func Open(path string) (uintptr, error) {
	h, err := syscall.LoadLibrary(path)
	return uintptr(h), err
}

// Sym returns the address of name in the DLL h.
func Sym(h uintptr, name string) (uintptr, error) {
	return syscall.GetProcAddress(syscall.Handle(h), name)
}
