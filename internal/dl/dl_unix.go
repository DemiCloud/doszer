//go:build !windows

package dl

import "github.com/ebitengine/purego"

// Open loads the shared library at path.
func Open(path string) (uintptr, error) {
	return purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_LOCAL)
}

// Sym returns the address of name in the library h.
func Sym(h uintptr, name string) (uintptr, error) {
	return purego.Dlsym(h, name)
}
