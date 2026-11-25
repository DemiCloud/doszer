package fat

import (
	"bytes"
	"io"
	"io/fs"
	"strings"
	"testing"
	"time"
)

// memDisk is a Disk in memory.
type memDisk []byte

func (m memDisk) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(m)) {
		return 0, io.EOF
	}
	n := copy(p, m[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (m memDisk) WriteAt(p []byte, off int64) (int, error) {
	if off+int64(len(p)) > int64(len(m)) {
		return 0, io.ErrShortWrite
	}
	return copy(m[off:], p), nil
}

// rebuild reads img's tree, lets change edit it, and writes it into a
// fresh disk holding only img's reserved sectors.
func rebuild(t *testing.T, img []byte, change func(root *Node)) (*FS, []byte) {
	t.Helper()
	f, err := Open(bytes.NewReader(img))
	if err != nil {
		t.Fatal(err)
	}
	root, err := f.Tree()
	if err != nil {
		t.Fatal(err)
	}
	if change != nil {
		change(root)
	}
	l := f.Layout()
	out := make(memDisk, len(img))
	copy(out, img[:l.Base+int64(l.ReservedSectors*l.BytesPerSector)])
	if err := Write(out, l, root); err != nil {
		t.Fatal(err)
	}
	g, err := Open(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	return g, out
}

func readAll(t *testing.T, f fs.FS) map[string]string {
	t.Helper()
	got := map[string]string{}
	err := fs.WalkDir(f, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			got[p+"/"] = ""
			return nil
		}
		b, err := fs.ReadFile(f, p)
		got[p] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// sameEntries compares two trees' entries as stored, ignoring the cluster
// and size fields Write replaces.
func sameEntries(t *testing.T, path string, a, b *Node) {
	t.Helper()
	if len(a.Children) != len(b.Children) {
		t.Errorf("%s: %d entries, want %d", path, len(b.Children), len(a.Children))
		return
	}
	for i := range a.Children {
		x, y := a.Children[i].Short, b.Children[i].Short
		for _, off := range []int{20, 21, 26, 27, 28, 29, 30, 31} {
			x[off], y[off] = 0, 0
		}
		name := path + "/" + a.Children[i].Name()
		if x != y {
			t.Errorf("%s: short entry\n got %x\nwant %x", name, y, x)
		}
		if string(utf16s(a.Children[i].Long)) != string(utf16s(b.Children[i].Long)) {
			t.Errorf("%s: long name %q, want %q", name, b.Children[i].Name(), a.Children[i].Name())
		}
		if a.Children[i].IsDir() {
			sameEntries(t, name, a.Children[i], b.Children[i])
		}
	}
}

func utf16s(u []uint16) []byte {
	var b []byte
	for _, v := range u {
		b = append(b, byte(v), byte(v>>8))
	}
	return b
}

func TestWriteRoundTrip(t *testing.T) {
	for _, kind := range []Kind{FAT12, FAT16, FAT32} {
		t.Run(map[Kind]string{FAT12: "FAT12", FAT16: "FAT16", FAT32: "FAT32"}[kind], func(t *testing.T) {
			img := buildFAT(t, kind, testTree())
			f, _ := Open(bytes.NewReader(img))
			want := readAll(t, f)
			g, _ := rebuild(t, img, nil)
			got := readAll(t, g)
			for p, w := range want {
				if got[p] != w {
					t.Errorf("%s = %q, want %q", p, got[p], w)
				}
			}
			if len(got) != len(want) {
				t.Errorf("paths %q, want %q", keys(got), keys(want))
			}

			a, _ := f.Tree()
			b, err := g.Tree()
			if err != nil {
				t.Fatal(err)
			}
			sameEntries(t, "", a, b)
			if !b.Children[0].IsLabel() || string(b.Children[0].Short[0:11]) != "TESTVOL    " {
				t.Error("volume label not kept first in the root")
			}

			// Every chain is contiguous: FRAG.BIN was written fragmented.
			for _, p := range []string{"Program Files/FRAG.BIN", "Program Files", "AUTOEXEC.BAT"} {
				e, err := g.lookup(p)
				if err != nil {
					t.Fatal(err)
				}
				chain, err := g.chain(e.cluster)
				if err != nil {
					t.Fatal(err)
				}
				for i := 1; i < len(chain); i++ {
					if chain[i] != chain[i-1]+1 {
						t.Errorf("%s: chain %v not contiguous", p, chain)
						break
					}
				}
			}
			if kind != FAT12 {
				clean := map[Kind]uint32{FAT16: 0xC000, FAT32: 0x0C000000}[kind]
				if g.fat[1]&clean != clean {
					t.Errorf("FAT[1] = %#x: not marked clean", g.fat[1])
				}
			}
		})
	}
}

func TestWriteEdits(t *testing.T) {
	img := buildFAT(t, FAT32, testTree())
	when := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	g, _ := rebuild(t, img, func(root *Node) {
		var kept []*Node
		for _, c := range root.Children {
			if !c.Is("EMPTY.DAT") {
				kept = append(kept, c)
			}
		}
		root.Children = kept
		pf := root.Child("program files")
		for _, name := range []string{"My Notes.txt", "My Notebook.txt", "NOTES.TXT"} {
			data := []byte(name)
			n, err := NewFile(pf, name, int64(len(data)), when, func() (io.Reader, error) { return bytes.NewReader(data), nil })
			if err != nil {
				t.Fatal(err)
			}
			pf.Children = append(pf.Children, n)
		}
		if _, err := NewFile(pf, "notes.txt", 0, when, nil); err == nil {
			t.Error("NewFile allowed a second NOTES.TXT")
		}
		d, err := NewDir(root, "New Folder", when)
		if err != nil {
			t.Fatal(err)
		}
		root.Children = append(root.Children, d)
	})
	got := readAll(t, g)
	for p, w := range map[string]string{
		"Program Files/My Notes.txt":    "My Notes.txt",
		"Program Files/My Notebook.txt": "My Notebook.txt",
		"Program Files/NOTES.TXT":       "NOTES.TXT",
		"New Folder/":                   "",
	} {
		if got[p] != w {
			t.Errorf("%s = %q, want %q (have %q)", p, got[p], w, keys(got))
		}
	}
	if _, ok := got["EMPTY.DAT"]; ok {
		t.Error("EMPTY.DAT not removed")
	}
	for short, long := range map[string]string{"PROGRA~1/MYNOTE~1.TXT": "My Notes.txt", "PROGRA~1/MYNOTE~2.TXT": "My Notebook.txt", "NEWFOL~1": "New Folder"} {
		info, err := fs.Stat(g, short)
		if err != nil || info.Name() != long {
			t.Errorf("%s: %v, %v; want %s", short, info, err, long)
		}
	}
	info, _ := fs.Stat(g, "Program Files/NOTES.TXT")
	if !info.ModTime().Equal(when) {
		t.Errorf("NOTES.TXT ModTime = %v, want %v", info.ModTime(), when)
	}
}

func TestWriteTooBig(t *testing.T) {
	img := buildFAT(t, FAT12, testTree())
	f, _ := Open(bytes.NewReader(img))
	root, _ := f.Tree()
	big, _ := NewFile(root, "BIG.BIN", 4000*512, time.Now(), func() (io.Reader, error) { return strings.NewReader(""), nil })
	root.Children = append(root.Children, big)
	l := f.Layout()
	out := make(memDisk, len(img))
	if err := Write(out, l, root); err == nil || !strings.Contains(err.Error(), "clusters") {
		t.Errorf("Write = %v, want a doesn't-fit error", err)
	}
}

func TestShortNames(t *testing.T) {
	dir := &Node{}
	tests := []struct{ name, short string }{
		{"README.TXT", "README  TXT"},
		{"readme.txt", "README~1TXT"},
		{"Program Files", "PROGRA~1   "},
		{"a.b.c.d", "ABC~1   D  "},
		{".hidden", "HIDDEN~1   "},
		{"x+y=z.html", "X_Y_Z~1 HTM"},
	}
	for _, tt := range tests {
		s, long, err := shortFor(dir, tt.name)
		if err != nil {
			t.Errorf("%s: %v", tt.name, err)
			continue
		}
		if string(s) != tt.short {
			t.Errorf("%s: short %q, want %q", tt.name, s, tt.short)
		}
		if (long == nil) != (tt.name == "README.TXT") {
			t.Errorf("%s: long name %v", tt.name, long)
		}
	}
}
