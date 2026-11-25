package platform

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// exe builds an executable's first bytes: an MZ header, and at 0x80 a
// new-style header with sig and fields set by edit.
func exe(sig string, edit func(h []byte)) []byte {
	b := make([]byte, 0x200)
	copy(b, "MZ")
	if sig == "" {
		binary.LittleEndian.PutUint16(b[0x18:], 0x1C)
		return b
	}
	binary.LittleEndian.PutUint16(b[0x18:], 0x40)
	binary.LittleEndian.PutUint32(b[0x3C:], 0x80)
	copy(b[0x80:], sig)
	if edit != nil {
		edit(b[0x80:])
	}
	return b
}

func ne(major, minor byte) []byte {
	return exe("NE", func(h []byte) { h[0x36], h[0x3E], h[0x3F] = 2, minor, major })
}

func pe(major, minor uint16) []byte {
	return exe("PE\x00\x00", func(h []byte) {
		binary.LittleEndian.PutUint16(h[24+48:], major)
		binary.LittleEndian.PutUint16(h[24+50:], minor)
	})
}

// TestMinOS: the OS line is the oldest system every program runs on.
func TestMinOS(t *testing.T) {
	tests := []struct {
		progs [][]byte
		want  string
	}{
		{[][]byte{exe("", nil), exe("", nil)}, "DOS"},
		{[][]byte{exe("", nil), ne(3, 10)}, "Windows 3.1"},
		{[][]byte{ne(3, 10), pe(4, 0), exe("", nil)}, "Windows 95"},
		{[][]byte{pe(4, 10), pe(4, 0)}, "Windows 98"},
	}
	for _, tt := range tests {
		var min Target
		for i, p := range tt.progs {
			c := Classify(fmt.Sprintf("P%d.EXE", i), bytes.NewReader(p))
			if min.Platform == NotProgram || min.Less(c) {
				min = c
			}
		}
		if min.String() != tt.want {
			t.Errorf("%v: %s, want %s", tt.progs != nil, min, tt.want)
		}
	}
}

func TestClassify(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{"GAME.COM", []byte{0xB4, 0x4C, 0xCD, 0x21}, "DOS"},
		{"GAME.EXE", exe("", nil), "DOS"},
		{"DOS4GW.EXE", exe("LE", nil), "DOS"},
		{"WIN30.EXE", ne(3, 0), "Windows 3.0"},
		{"WIN31.EXE", ne(3, 10), "Windows 3.1"},
		{"OLD.EXE", ne(0, 0), "Windows 3.0"},
		{"OS2.EXE", exe("NE", func(h []byte) { h[0x36] = 1 }), "OS/2"},
		{"WIN95.EXE", pe(4, 0), "Windows 95"},
		{"WIN98.EXE", pe(4, 10), "Windows 98"},
		{"XP.EXE", pe(5, 1), "Windows XP"},
		{"README.EXE", []byte("not a program"), "not a program"},
		{"TINY.EXE", []byte("MZ"), "DOS"},
	}
	for _, tt := range tests {
		if got := Classify(tt.name, bytes.NewReader(tt.data)).String(); got != tt.want {
			t.Errorf("%s: %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestNeeds(t *testing.T) {
	dos, w31, w95, w98 := Target{Platform: DOS}, Target{Win16, 3, 10}, Target{Win32, 4, 0}, Target{Win32, 4, 10}
	tests := []struct {
		progs []Program
		want  string // Need, then " / setup: X" if any
	}{
		{[]Program{{"GAME.EXE", dos}, {"SETUP.EXE", w31}}, "DOS / setup: Windows 3.1"},
		{[]Program{{"GAME.EXE", w95}, {"INSTALL.EXE", dos}}, "Windows 95"},
		{[]Program{{"SETUP.EXE", w95}}, "Windows 95"},
		{[]Program{{"GAME.EXE", w95}, {"TOOLS/EDIT.EXE", w98}}, "Windows 98"},
		{[]Program{{"GAME.EXE", w31}, {"DIRECTX/DXSETUP.EXE", w98}, {"DX5/FOO.EXE", w98}}, "Windows 3.1 / setup: Windows 98"},
		// Installed programs decide over the disc's.
		{[]Program{{"DESCENT2/DESCENT2.EXE", dos}, {"cd.iso:WINSETUP/RESUME.EXE", w95}}, "DOS"},
		// A disc with a DOS and a Windows version.
		{[]Program{{"cd.cue:SIERRA.EXE", dos}, {"cd.cue:SIERRAW.EXE", w31}}, "DOS or Windows 3.1"},
		{nil, "not a program"},
	}
	for _, tt := range tests {
		n := Needs(tt.progs)
		got := n.String()
		if n.Setup.Platform != NotProgram {
			got += " / setup: " + n.Setup.String()
		}
		if got != tt.want {
			t.Errorf("%v: %s, want %s", tt.progs, got, tt.want)
		}
	}
}

// TestScan: programs in the source and on an ISO image in it, in its
// files' layout and in a raw MODE1/2352 track a CUE sheet names.
func TestScan(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "GAME.EXE"), exe("", nil), 0o644)
	os.WriteFile(filepath.Join(dir, "README.TXT"), []byte("x"), 0o644)
	iso := isoWith(t, "SETUP.EXE", ne(3, 10))
	os.WriteFile(filepath.Join(dir, "disc.iso"), iso, 0o644)
	var raw bytes.Buffer
	for s := 0; s*2048 < len(iso); s++ {
		sec := make([]byte, 2352)
		copy(sec[16:], iso[s*2048:min(len(iso), (s+1)*2048)])
		raw.Write(sec)
	}
	os.MkdirAll(filepath.Join(dir, "cd"), 0o755)
	os.WriteFile(filepath.Join(dir, "cd", "track.bin"), raw.Bytes(), 0o644)
	os.WriteFile(filepath.Join(dir, "cd", "game.cue"), []byte("FILE \"track.bin\" BINARY\r\n  TRACK 01 MODE1/2352\r\n    INDEX 01 00:00:00\r\n"), 0o644)

	progs, err := Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, p := range progs {
		got[p.Path] = p.Target.String()
	}
	want := map[string]string{"GAME.EXE": "DOS", "disc.iso:SETUP.EXE": "Windows 3.1", "cd/game.cue:SETUP.EXE": "Windows 3.1"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("Scan = %v, want %v", got, want)
	}
}

// isoWith builds a minimal ISO 9660 image holding one file in its root.
func isoWith(t *testing.T, name string, data []byte) []byte {
	t.Helper()
	const ss = 2048
	img := make([]byte, 20*ss)
	le, be := binary.LittleEndian, binary.BigEndian
	both32 := func(b []byte, v uint32) { le.PutUint32(b, v); be.PutUint32(b[4:], v) }
	record := func(lba, size uint32, flags byte, id []byte) []byte {
		r := make([]byte, 33+len(id)+(1-len(id)%2))
		r[0] = byte(len(r))
		both32(r[2:], lba)
		both32(r[10:], size)
		r[25] = flags
		r[32] = byte(len(id))
		copy(r[33:], id)
		return r
	}
	pvd := img[16*ss:]
	pvd[0] = 1
	copy(pvd[1:], "CD001")
	pvd[6] = 1
	both32(pvd[80:], 20)
	le.PutUint16(pvd[128:], ss)
	be.PutUint16(pvd[130:], ss)
	copy(pvd[156:], record(18, ss, 2, []byte{0}))
	img[17*ss] = 255
	copy(img[17*ss+1:], "CD001")
	dir := img[18*ss:]
	n := copy(dir, record(18, ss, 2, []byte{0}))
	n += copy(dir[n:], record(18, ss, 2, []byte{1}))
	copy(dir[n:], record(19, uint32(len(data)), 0, []byte(name+";1")))
	copy(img[19*ss:], data)
	return img
}
