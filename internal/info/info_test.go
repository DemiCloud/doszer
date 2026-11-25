package info

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DemiCloud/doszer/internal/build"
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

func TestClassify(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want Platform
	}{
		{"GAME.COM", []byte{0xB4, 0x4C, 0xCD, 0x21}, DOS},
		{"GAME.EXE", exe("", nil), DOS},
		{"DOS4GW.EXE", exe("LE", nil), DOS},
		{"WIN16.EXE", exe("NE", func(h []byte) { h[0x36] = 2 }), Win16},
		{"OS2.EXE", exe("NE", func(h []byte) { h[0x36] = 1 }), OS2},
		{"WIN95.EXE", exe("PE\x00\x00", func(h []byte) { binary.LittleEndian.PutUint16(h[24+48:], 4) }), Win32},
		{"XP.EXE", exe("PE\x00\x00", func(h []byte) { binary.LittleEndian.PutUint16(h[24+48:], 5) }), WinNT5},
		{"README.EXE", []byte("not a program"), NotProgram},
		{"TINY.EXE", []byte("MZ"), DOS},
	}
	for _, tt := range tests {
		if got := classify(tt.name, bytes.NewReader(tt.data)); got != tt.want {
			t.Errorf("%s: %v, want %v", tt.name, got, tt.want)
		}
	}
}

func writeFile(t *testing.T, dir, name string, data []byte) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(name))
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func buildArchive(t *testing.T, out, manifest string, files map[string][]byte, built time.Time) {
	t.Helper()
	src := t.TempDir()
	writeFile(t, src, "DOSZ.TOML", []byte(manifest))
	for n, d := range files {
		writeFile(t, src, n, d)
	}
	prov := &build.Provenance{Generator: "doszer test", Built: built}
	if _, err := build.Run(build.Options{SourceDir: src, OutputPath: out, Provenance: prov}); err != nil {
		t.Fatal(err)
	}
}

func TestRead(t *testing.T) {
	dir := t.TempDir()
	files := map[string][]byte{
		"GAME.EXE":  exe("", nil),
		"SETUP.EXE": exe("NE", func(h []byte) { h[0x36] = 2 }),
		"WIN.EXE":   exe("PE\x00\x00", func(h []byte) { binary.LittleEndian.PutUint16(h[24+48:], 4) }),
	}
	child := `format = 1
name = "Child Game"
notes = "First line.\nSecond line."
[boot]
exec = 'C:\GAME.EXE'
parent = "Base.dosz"
`
	a := filepath.Join(dir, "Child.dosz")
	buildArchive(t, a, child, files, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	r, err := Read(a)
	if err != nil {
		t.Fatal(err)
	}
	if r.Manifest == nil || r.Manifest.Name != "Child Game" {
		t.Fatalf("manifest %+v", r.Manifest)
	}
	if r.Programs[DOS] != 1 || r.Programs[Win16] != 1 || r.Programs[Win32] != 1 {
		t.Errorf("programs %v", r.Programs)
	}
	if len(r.Parents) != 1 || r.Parents[0].Name != "Base.dosz" || r.Parents[0].Found {
		t.Errorf("parents %+v, want Base.dosz not found", r.Parents)
	}

	// With the parent beside it, which has a parent of its own.
	buildArchive(t, filepath.Join(dir, "Base.dosz"), "format = 1\nname = \"Base\"\n[boot]\nparent = \"Shared.dosz\"\n", map[string][]byte{"BASE.DAT": []byte("x")}, time.Now())
	r, _ = Read(a)
	if len(r.Parents) != 2 || !r.Parents[0].Found || r.Parents[1].Name != "Shared.dosz" || r.Parents[1].Found {
		t.Errorf("parents %+v, want Base.dosz found, then Shared.dosz not", r.Parents)
	}

	// The content hash ignores the archive comment; the whole-file one doesn't.
	b := filepath.Join(t.TempDir(), "Child.dosz")
	buildArchive(t, b, child, files, time.Date(2027, 6, 1, 0, 0, 0, 0, time.UTC))
	r2, _ := Read(b)
	if r2.Content != r.Content {
		t.Error("content hash differs between two builds of one source")
	}
	if r2.SHA256 == r.SHA256 {
		t.Error("whole-file hash ignores the comment")
	}

	var out bytes.Buffer
	Print(&out, r)
	for _, want := range []string{"Name:      Child Game", "Boot:      DOS, starts C:\\GAME.EXE", "Parent:    Base.dosz (found)", "             Shared.dosz (not found beside it)", "1 DOS, 1 Windows 3.x, 1 Windows 95 or later", "  First line.\n  Second line.", "Built by doszer test on 2026-01-01"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}

func TestReadLegacy(t *testing.T) {
	p := filepath.Join(t.TempDir(), "old.zip")
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("GAME/GAME.EXE")
	w.Write(exe("", nil))
	w, _ = zw.Create("A.parent")
	zw.Create("B.parent")
	zw.Close()
	os.WriteFile(p, buf.Bytes(), 0o644)
	r, err := Read(p)
	if err != nil {
		t.Fatal(err)
	}
	if r.Manifest != nil || r.Files != 3 {
		t.Errorf("manifest %v, files %d", r.Manifest, r.Files)
	}
	if len(r.Parents) != 1 || !strings.Contains(r.Parents[0].Err, "more than one") {
		t.Errorf("parents %+v, want the two-marker error", r.Parents)
	}
}
