package info

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
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

func ne(major, minor byte) []byte {
	return exe("NE", func(h []byte) { h[0x36], h[0x3E], h[0x3F] = 2, minor, major })
}

func pe(major, minor uint16) []byte {
	return exe("PE\x00\x00", func(h []byte) {
		binary.LittleEndian.PutUint16(h[24+48:], major)
		binary.LittleEndian.PutUint16(h[24+50:], minor)
	})
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
	prov := &build.Provenance{Generator: "doszer test"}
	if _, err := build.Run(build.Options{SourceDir: src, OutputPath: out, Provenance: prov}); err != nil {
		t.Fatal(err)
	}
}

func TestRead(t *testing.T) {
	dir := t.TempDir()
	files := map[string][]byte{
		"GAME.EXE":  exe("", nil),
		"SETUP.EXE": ne(3, 10),
		"WIN.EXE":   pe(4, 0),
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
	if r.Programs != 3 || r.Need.String() != "DOS or Windows 95" {
		t.Errorf("programs %d, OS %s", r.Programs, r.Need)
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

	// A rebuild is the same file; a notes edit changes the file but not
	// its content hash (§9 leaves DOSZ.TOML out); a file edit changes both.
	same := filepath.Join(t.TempDir(), "Child.dosz")
	buildArchive(t, same, child, files, time.Time{})
	rSame, _ := Read(same)
	if rSame.SHA256 != r.SHA256 {
		t.Error("two builds of one source differ")
	}
	notes := filepath.Join(t.TempDir(), "Child.dosz")
	buildArchive(t, notes, strings.Replace(child, "First line.", "First line, fixed.", 1), files, time.Time{})
	rNotes, _ := Read(notes)
	if rNotes.SHA256 == r.SHA256 || rNotes.Content != r.Content {
		t.Errorf("notes edit: file hash changed %v (want true), content hash changed %v (want false)", rNotes.SHA256 != r.SHA256, rNotes.Content != r.Content)
	}
	edited := map[string][]byte{"EXTRA.TXT": []byte("x\r\n")}
	for k, v := range files {
		edited[k] = v
	}
	other := filepath.Join(t.TempDir(), "Child.dosz")
	buildArchive(t, other, child, edited, time.Time{})
	rOther, _ := Read(other)
	if rOther.Content == r.Content {
		t.Error("a file edit left the content hash alone")
	}

	// The content hash is sha256sum's output over the files, hashed.
	zr, _ := zip.OpenReader(a)
	var lines bytes.Buffer
	var names []string
	for _, e := range zr.File {
		if e.Name != "DOSZ.TOML" {
			names = append(names, e.Name)
		}
	}
	sort.Strings(names)
	for _, n := range names {
		for _, e := range zr.File {
			if e.Name == n {
				rc, _ := e.Open()
				b, _ := io.ReadAll(rc)
				rc.Close()
				fmt.Fprintf(&lines, "%x  %s\n", sha256.Sum256(b), n)
			}
		}
	}
	zr.Close()
	if want := fmt.Sprintf("%x", sha256.Sum256(lines.Bytes())); r.Content != want {
		t.Errorf("content hash %s, want %s", r.Content, want)
	}

	var out bytes.Buffer
	Print(&out, r)
	for _, want := range []string{"Name:      Child Game", "Boot:      DOS\nStarts:    C:\\GAME.EXE", "Parent:    Base.dosz (found)", "             Shared.dosz (not found beside it)", "Programs:  3\nOS:        DOS or Windows 95", "  First line.\n  Second line.", "Built by doszer test (DOSZ manifest format 1)"} {
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
