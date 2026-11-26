package dump

import (
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DemiCloud/doszer/internal/fat"
)

type memDisk []byte

func (d memDisk) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(d)) {
		return 0, io.EOF
	}
	return copy(p, d[off:]), nil
}
func (d memDisk) WriteAt(p []byte, off int64) (int, error) { return copy(d[off:], p), nil }

// floppy builds a 1.44 MB FAT12 floppy image holding files (one level of
// directories), dated when.
func floppy(t *testing.T, files map[string]string, when time.Time) []byte {
	t.Helper()
	img := make(memDisk, 2880*512)
	bs, le := img[:512], binary.LittleEndian
	bs[0], bs[1], bs[2] = 0xEB, 0x3C, 0x90
	copy(bs[3:], "MSDOS5.0")
	le.PutUint16(bs[11:], 512)
	bs[13] = 1
	le.PutUint16(bs[14:], 1)
	bs[16] = 2
	le.PutUint16(bs[17:], 224)
	le.PutUint16(bs[19:], 2880)
	bs[21] = 0xF0
	le.PutUint16(bs[22:], 9)
	bs[510], bs[511] = 0x55, 0xAA
	vol, err := fat.Open(bytes.NewReader(img))
	if err != nil {
		t.Fatal(err)
	}
	root, _ := vol.Tree()
	for p, data := range files {
		parent := root
		if d := filepath.Dir(p); d != "." {
			if parent = root.Child(d); parent == nil {
				parent, _ = fat.NewDir(root, d, when)
				root.Children = append(root.Children, parent)
			}
		}
		b := []byte(data)
		n, err := fat.NewFile(parent, filepath.Base(p), int64(len(b)), when, func() (io.Reader, error) { return bytes.NewReader(b), nil })
		if err != nil {
			t.Fatal(err)
		}
		parent.Children = append(parent.Children, n)
	}
	if err := fat.Write(img, vol.Layout(), root); err != nil {
		t.Fatal(err)
	}
	return img
}

// TestImportDisk: a floppy image is copied into the source as it is (a
// .vfd as .img); an image no FAT volume or floppy size fits is refused.
func TestImportDisk(t *testing.T) {
	when := time.Date(1993, 4, 1, 10, 0, 0, 0, time.Local)
	dir := t.TempDir()
	good := floppy(t, map[string]string{"SETUP.EXE": "MZ", "GAMES/SKI.EXE": "MZ1"}, when)
	odd := append(append([]byte{}, good...), make([]byte, 512)...)
	tests := []struct {
		name, file string
		data       []byte
		want       string // the file written, "" for an error
	}{
		{"floppy", "disk1.img", good, "disk1.img"},
		{"vfd", "disk2.vfd", good, "disk2.img"},
		{"not FAT", "blank.ima", make([]byte, 1440*1024), ""},
		{"odd size", "odd.img", odd, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !IsDiskImage(tt.file) {
				t.Fatalf("%s: not a disk image", tt.file)
			}
			p := filepath.Join(dir, tt.file)
			os.WriteFile(p, tt.data, 0o644)
			src := t.TempDir()
			got, err := ImportDisk(p, src)
			if tt.want == "" {
				if err == nil {
					t.Fatalf("imported %s, want an error", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if filepath.Base(got) != tt.want {
				t.Errorf("wrote %s, want %s", filepath.Base(got), tt.want)
			}
			if b, _ := os.ReadFile(got); !bytes.Equal(b, tt.data) {
				t.Error("copy differs from the image")
			}
		})
	}
}
