package info

import (
	"encoding/binary"
	"io"
	"path"
	"strings"
)

// Platform is what an executable was built to run on.
type Platform int

const (
	NotProgram Platform = iota
	DOS                 // MZ, .COM, or a DOS extender's LE/LX (DOS/4GW)
	Win16               // NE for Windows: Windows 3.x (and later)
	Win32               // PE, subsystem 4.x: Windows 95 or later
	WinNT5              // PE, subsystem 5.0 or later: Windows 2000/XP or later
	OS2                 // NE for OS/2
)

func (p Platform) String() string {
	return [...]string{"not a program", "DOS", "Windows 3.x", "Windows 95 or later", "Windows 2000 or later", "OS/2"}[p]
}

// exeHeaderMax bounds how far into a file its new-style header may start:
// past it, the file is taken for a DOS program with an odd header field.
const exeHeaderMax = 1 << 20

// classify reads an executable's headers from r (its start) and returns
// what it runs on. name decides only between .COM and the rest.
func classify(name string, r io.Reader) Platform {
	ext := strings.ToUpper(path.Ext(name))
	head := make([]byte, 64)
	n, _ := io.ReadFull(r, head)
	head = head[:n]
	isMZ := n >= 2 && (string(head[:2]) == "MZ" || string(head[:2]) == "ZM")
	switch {
	case ext == ".COM" && !isMZ:
		return DOS
	case !isMZ:
		return NotProgram
	case n < 64:
		return DOS
	}
	le := binary.LittleEndian
	// A new-style header is announced by e_lfanew at 0x3C; a plain DOS
	// program's relocation table offset (0x18) is below 0x40.
	lfanew := int64(le.Uint32(head[0x3C:]))
	if le.Uint16(head[0x18:]) < 0x40 && lfanew == 0 || lfanew < 64 || lfanew > exeHeaderMax {
		return DOS
	}
	if _, err := io.CopyN(io.Discard, r, lfanew-64); err != nil {
		return DOS
	}
	hdr := make([]byte, 0x60)
	n, _ = io.ReadFull(r, hdr)
	hdr = hdr[:n]
	switch {
	case n >= 4 && string(hdr[:4]) == "PE\x00\x00":
		// COFF header (20 bytes), then the optional header: subsystem
		// version at 48 into it, subsystem at 68 (PE32 and PE32+ alike).
		if n < 24+70 {
			return Win32
		}
		opt := hdr[24:]
		if le.Uint16(opt[48:]) >= 5 {
			return WinNT5
		}
		return Win32
	case n >= 0x37 && string(hdr[:2]) == "NE":
		switch hdr[0x36] { // target operating system
		case 1:
			return OS2
		case 2, 4, 0: // Windows, Windows 386, unknown (early Windows)
			return Win16
		}
		return Win16
	case n >= 2 && (string(hdr[:2]) == "LE" || string(hdr[:2]) == "LX"):
		return DOS
	}
	return DOS
}

// isProgramName reports a file name an executable is looked for in.
func isProgramName(name string) bool {
	switch strings.ToUpper(path.Ext(name)) {
	case ".EXE", ".COM":
		return true
	}
	return false
}
