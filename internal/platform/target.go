// Package platform tells what system a DOS or Windows program needs, from
// its executable headers, and what a game's programs need together: for
// `doszer info`, and for `doszer init` choosing a boot model.
package platform

import (
	"encoding/binary"
	"fmt"
	"io"
	"path"
	"strings"
)

// Platform is the kind of system an executable was built for.
type Platform int

const (
	NotProgram Platform = iota
	DOS                 // MZ, .COM, or a DOS extender's LE/LX (DOS/4GW)
	Win16               // NE for Windows
	Win32               // PE
	OS2                 // NE for OS/2
)

// Target is the oldest system an executable runs on: its platform and, for
// Windows, the version its header asks for (NE: expected Windows version;
// PE: subsystem version).
type Target struct {
	Platform     Platform
	Major, Minor int
}

// less orders targets by what they need: DOS programs run on Windows,
// Windows 3.x programs on Windows 9x, and a later version needs more.
// Less reports whether t needs less than u.
func (t Target) Less(u Target) bool {
	rank := func(p Platform) int {
		return map[Platform]int{DOS: 1, Win16: 2, Win32: 3}[p]
	}
	if rank(t.Platform) != rank(u.Platform) {
		return rank(t.Platform) < rank(u.Platform)
	}
	if t.Major != u.Major {
		return t.Major < u.Major
	}
	return t.Minor < u.Minor
}

func (t Target) String() string {
	switch t.Platform {
	case DOS:
		return "DOS"
	case OS2:
		return "OS/2"
	case Win16:
		if t.Major < 3 {
			return "Windows 3.0"
		}
		return fmt.Sprintf("Windows %d.%d", t.Major, t.Minor/10)
	case Win32:
		v := t.Major*100 + t.Minor
		switch {
		case v < 410:
			return "Windows 95"
		case v < 490:
			return "Windows 98"
		case v < 500:
			return "Windows ME"
		case v < 501:
			return "Windows 2000"
		case v < 600:
			return "Windows XP"
		}
		return fmt.Sprintf("Windows (subsystem %d.%d)", t.Major, t.Minor)
	}
	return "not a program"
}

// exeHeaderMax bounds how far into a file its new-style header may start:
// past it, the file is taken for a DOS program with an odd header field.
const exeHeaderMax = 1 << 20

// Classify reads an executable's headers from r (its start) and returns
// what it runs on. name decides only between .COM and the rest.
func Classify(name string, r io.Reader) Target {
	dos := Target{Platform: DOS}
	ext := strings.ToUpper(path.Ext(name))
	head := make([]byte, 64)
	n, _ := io.ReadFull(r, head)
	head = head[:n]
	isMZ := n >= 2 && (string(head[:2]) == "MZ" || string(head[:2]) == "ZM")
	switch {
	case ext == ".COM" && !isMZ:
		return dos
	case !isMZ:
		return Target{}
	case n < 64:
		return dos
	}
	le := binary.LittleEndian
	// A new-style header is announced by e_lfanew at 0x3C; a plain DOS
	// program's relocation table offset (0x18) is below 0x40.
	lfanew := int64(le.Uint32(head[0x3C:]))
	if le.Uint16(head[0x18:]) < 0x40 && lfanew == 0 || lfanew < 64 || lfanew > exeHeaderMax {
		return dos
	}
	if _, err := io.CopyN(io.Discard, r, lfanew-64); err != nil {
		return dos
	}
	hdr := make([]byte, 0x80)
	n, _ = io.ReadFull(r, hdr)
	hdr = hdr[:n]
	switch {
	case n >= 4 && string(hdr[:4]) == "PE\x00\x00":
		// COFF header (20 bytes), then the optional header: subsystem
		// version at 48 into it (PE32 and PE32+ alike).
		t := Target{Platform: Win32, Major: 4}
		if n >= 24+52 {
			t.Major, t.Minor = int(le.Uint16(hdr[24+48:])), int(le.Uint16(hdr[24+50:]))
		}
		return t
	case n >= 0x40 && string(hdr[:2]) == "NE":
		if hdr[0x36] == 1 { // target operating system: OS/2
			return Target{Platform: OS2}
		}
		// Expected Windows version: minor byte, then major.
		return Target{Platform: Win16, Major: int(hdr[0x3F]), Minor: int(hdr[0x3E])}
	case n >= 2 && (string(hdr[:2]) == "LE" || string(hdr[:2]) == "LX"):
		return dos
	}
	return dos
}

// IsProgramName reports a file name an executable is looked for in.
func IsProgramName(name string) bool {
	switch strings.ToUpper(path.Ext(name)) {
	case ".EXE", ".COM":
		return true
	}
	return false
}
