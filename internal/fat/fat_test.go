package fat

import (
	"bytes"
	"encoding/binary"
	"io"
	"io/fs"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"
	"unicode/utf16"
)

// node is a file or directory in a test image.
type node struct {
	short    string // 11-byte 8.3 name, space padded, e.g. "README  TXT"
	long     string // VFAT long name, or "" for none
	nt       byte   // byte 12: lowercase flags
	data     []byte
	children []*node
	dir      bool
	frag     bool // leave a free cluster between each of this file's clusters
	badLFN   bool // write the long name with a wrong checksum
}

const testTime, testDate = 0x5AC0, 0x2697 // 11:22:00, 1999-04-23

// image builds a FAT volume: kind picks a cluster count in that variant's
// range, with 512-byte sectors and one sector per cluster.
type image struct {
	kind        Kind
	buf         []byte
	clusterSize int
	firstData   int // byte offset of cluster 2
	fat         []uint32
	next        uint32
	rootOff     int // FAT12/16 root directory byte offset
}

func buildFAT(t *testing.T, kind Kind, root []*node) []byte {
	t.Helper()
	clusters := map[Kind]int{FAT12: 2000, FAT16: 5000, FAT32: 70000}[kind]
	reserved, rootEntries := 1, 512
	if kind == FAT32 {
		reserved, rootEntries = 32, 0
	}
	entryBits := map[Kind]int{FAT12: 12, FAT16: 16, FAT32: 32}[kind]
	fatSectors := ((clusters+2)*entryBits/8 + 511) / 512
	rootSectors := rootEntries * 32 / 512
	firstData := reserved + 2*fatSectors + rootSectors
	total := firstData + clusters

	im := &image{kind: kind, buf: make([]byte, total*512), clusterSize: 512,
		firstData: firstData * 512, fat: make([]uint32, clusters+2), next: 2,
		rootOff: (reserved + 2*fatSectors) * 512}
	im.fat[0], im.fat[1] = 0x0FFFFFF8, 0x0FFFFFFF

	bs := im.buf[0:512]
	le := binary.LittleEndian
	bs[0], bs[1], bs[2] = 0xEB, 0x3C, 0x90
	copy(bs[3:], "MSWIN4.1")
	le.PutUint16(bs[11:], 512)
	bs[13] = 1
	le.PutUint16(bs[14:], uint16(reserved))
	bs[16] = 2
	le.PutUint16(bs[17:], uint16(rootEntries))
	if total < 0x10000 {
		le.PutUint16(bs[19:], uint16(total))
	} else {
		le.PutUint32(bs[32:], uint32(total))
	}
	bs[21] = 0xF8
	if kind == FAT32 {
		le.PutUint32(bs[36:], uint32(fatSectors))
	} else {
		le.PutUint16(bs[22:], uint16(fatSectors))
	}
	bs[510], bs[511] = 0x55, 0xAA

	label := &node{short: "TESTVOL    "}
	entries := append([]*node{label}, root...)
	if kind == FAT32 {
		rootCluster := im.writeDir(entries, 0, true)
		le.PutUint32(bs[44:], rootCluster)
	} else {
		copy(im.buf[im.rootOff:], im.dirBytes(entries, 0, 0, true))
	}

	// Write both FATs.
	for copyN := 0; copyN < 2; copyN++ {
		base := (reserved + copyN*fatSectors) * 512
		for c, v := range im.fat {
			switch kind {
			case FAT12:
				i := base + c*3/2
				cur := le.Uint16(im.buf[i:])
				if c&1 == 0 {
					cur = cur&0xF000 | uint16(v&0xFFF)
				} else {
					cur = cur&0x000F | uint16(v&0xFFF)<<4
				}
				le.PutUint16(im.buf[i:], cur)
			case FAT16:
				le.PutUint16(im.buf[base+c*2:], uint16(v))
			case FAT32:
				le.PutUint32(im.buf[base+c*4:], v)
			}
		}
	}
	return im.buf
}

func (im *image) eoc() uint32 {
	return map[Kind]uint32{FAT12: 0xFFF, FAT16: 0xFFFF, FAT32: 0x0FFFFFFF}[im.kind]
}

// alloc chains n clusters (at least one), optionally leaving gaps, writes
// data into them, and returns the first.
func (im *image) alloc(data []byte, frag bool) uint32 {
	n := max(1, (len(data)+im.clusterSize-1)/im.clusterSize)
	var chain []uint32
	for i := 0; i < n; i++ {
		chain = append(chain, im.next)
		im.next++
		if frag {
			im.next++
		}
	}
	for i, c := range chain {
		if i+1 < len(chain) {
			im.fat[c] = chain[i+1]
		} else {
			im.fat[c] = im.eoc()
		}
		off := im.firstData + int(c-2)*im.clusterSize
		end := min(len(data), (i+1)*im.clusterSize)
		if i*im.clusterSize < end {
			copy(im.buf[off:], data[i*im.clusterSize:end])
		}
	}
	return chain[0]
}

// writeDir allocates a directory's clusters and returns the first.
func (im *image) writeDir(entries []*node, parent uint32, root bool) uint32 {
	// Size the directory first, then fill it once its own cluster is known.
	size := len(im.dirBytes(entries, 0, parent, root))
	first := im.alloc(make([]byte, size), false)
	data := im.dirBytes(entries, first, parent, root)
	for i, c := 0, first; i < len(data); i += im.clusterSize {
		copy(im.buf[im.firstData+int(c-2)*im.clusterSize:], data[i:min(len(data), i+im.clusterSize)])
		c = im.fat[c]
	}
	return first
}

// dirBytes encodes a directory's entries. Subdirectories and file data are
// allocated as a side effect, so it must only be called once with self set.
func (im *image) dirBytes(entries []*node, self, parent uint32, root bool) []byte {
	var out []byte
	put := func(name string, attr, nt byte, cluster, size uint32) {
		e := make([]byte, 32)
		copy(e, name)
		e[11], e[12] = attr, nt
		le := binary.LittleEndian
		le.PutUint16(e[20:], uint16(cluster>>16))
		le.PutUint16(e[22:], testTime)
		le.PutUint16(e[24:], testDate)
		le.PutUint16(e[26:], uint16(cluster))
		le.PutUint32(e[28:], size)
		out = append(out, e...)
	}
	sizing := self == 0 && !root
	if !root {
		put(".          ", attrDirectory, 0, self, 0)
		put("..         ", attrDirectory, 0, parent, 0)
	}
	// A deleted entry, which must be skipped.
	del := make([]byte, 32)
	copy(del, "\xe5ELETED TXT")
	out = append(out, del...)

	for _, n := range entries {
		if n.long != "" {
			out = append(out, lfnEntries(n.long, n.short, n.badLFN)...)
		}
		switch {
		case n.short == "TESTVOL    ":
			put(n.short, attrVolumeID, 0, 0, 0)
		case sizing:
			put(n.short, 0, 0, 0, 0)
		case n.dir:
			c := im.writeDir(n.children, self, false)
			put(n.short, attrDirectory, n.nt, c, 0)
		case len(n.data) == 0:
			put(n.short, 0, n.nt, 0, 0)
		default:
			put(n.short, 0, n.nt, im.alloc(n.data, n.frag), uint32(len(n.data)))
		}
	}
	return out
}

func lfnEntries(long, short string, bad bool) []byte {
	units := utf16.Encode([]rune(long))
	units = append(units, 0)
	for len(units)%13 != 0 {
		units = append(units, 0xFFFF)
	}
	sum := shortNameChecksum([]byte(short))
	if bad {
		sum++
	}
	count := len(units) / 13
	var out []byte
	for seq := count; seq >= 1; seq-- {
		e := make([]byte, 32)
		e[0] = byte(seq)
		if seq == count {
			e[0] |= 0x40
		}
		e[11], e[13] = attrLFN, sum
		for j, off := range []int{1, 3, 5, 7, 9, 14, 16, 18, 20, 22, 24, 28, 30} {
			binary.LittleEndian.PutUint16(e[off:], units[(seq-1)*13+j])
		}
		out = append(out, e...)
	}
	return out
}

func testTree() []*node {
	big := bytes.Repeat([]byte("0123456789abcdef"), 200) // 3200 bytes, 7 clusters
	return []*node{
		{short: "AUTOEXECBAT", data: []byte("@ECHO OFF\r\n")},
		{short: "PROGRA~1   ", long: "Program Files", dir: true, children: []*node{
			{short: "LONGFI~1TXT", long: "A long file name.txt", data: []byte("long")},
			{short: "FRAG    BIN", data: big, frag: true},
		}},
		{short: "README  TXT", nt: 0x18, data: []byte("lower")},
		{short: "EMPTY   DAT"},
		{short: "ORPHAN  TXT", long: "Should Not Appear.txt", badLFN: true, data: []byte("x")},
		{short: "\x82T\x82     TXT", data: []byte("cp437")}, // é T é
	}
}

func TestRead(t *testing.T) {
	want := map[string]string{
		"AUTOEXEC.BAT":                       "@ECHO OFF\r\n",
		"Program Files/A long file name.txt": "long",
		"Program Files/FRAG.BIN":             strings.Repeat("0123456789abcdef", 200),
		"readme.txt":                         "lower",
		"EMPTY.DAT":                          "",
		"ORPHAN.TXT":                         "x",
		"éTé.TXT":                            "cp437",
	}
	for _, kind := range []Kind{FAT12, FAT16, FAT32} {
		t.Run(map[Kind]string{FAT12: "FAT12", FAT16: "FAT16", FAT32: "FAT32"}[kind], func(t *testing.T) {
			img := buildFAT(t, kind, testTree())
			f, err := Open(bytes.NewReader(img))
			if err != nil {
				t.Fatal(err)
			}
			if f.Kind() != kind {
				t.Fatalf("kind = FAT%d, want FAT%d", f.Kind(), kind)
			}

			got := map[string]string{}
			err = fs.WalkDir(f, ".", func(p string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return err
				}
				data, err := fs.ReadFile(f, p)
				got[p] = string(data)
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(want) {
				t.Errorf("files = %q", keys(got))
			}
			for p, w := range want {
				if got[p] != w {
					t.Errorf("%s = %q, want %q (have %q)", p, got[p], w, keys(got))
				}
			}

			info, err := fs.Stat(f, "program files/a LONG file name.TXT")
			if err != nil {
				t.Fatalf("case-insensitive lookup: %v", err)
			}
			if want := time.Date(1999, 4, 23, 11, 22, 0, 0, time.UTC); !info.ModTime().Equal(want) {
				t.Errorf("ModTime = %v, want %v", info.ModTime(), want)
			}
			if data, err := fs.ReadFile(f, "progra~1/LONGFI~1.TXT"); err != nil || string(data) != "long" {
				t.Errorf("short-name lookup = %q, %v", data, err)
			}

			if err := fstest.TestFS(f, "AUTOEXEC.BAT", "Program Files/FRAG.BIN", "readme.txt"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestOpenPartitioned(t *testing.T) {
	vol := buildFAT(t, FAT16, []*node{{short: "HELLO   TXT", data: []byte("hi")}})
	disk := make([]byte, 63*512)
	disk[510], disk[511] = 0x55, 0xAA
	e := disk[446:]
	e[0], e[4] = 0x80, 0x06
	binary.LittleEndian.PutUint32(e[8:], 63)
	binary.LittleEndian.PutUint32(e[12:], uint32(len(vol)/512))
	disk = append(disk, vol...)

	f, err := Open(bytes.NewReader(disk))
	if err != nil {
		t.Fatal(err)
	}
	if data, err := fs.ReadFile(f, "HELLO.TXT"); err != nil || string(data) != "hi" {
		t.Fatalf("HELLO.TXT = %q, %v", data, err)
	}
}

func TestRejectBroken(t *testing.T) {
	img := buildFAT(t, FAT16, []*node{{short: "LOOP    BIN", data: bytes.Repeat([]byte{1}, 2048)}})
	f, err := Open(bytes.NewReader(img))
	if err != nil {
		t.Fatal(err)
	}
	// Point the file's last cluster back at its first.
	chain, err := f.chain(f.lookupCluster(t, "LOOP.BIN"))
	if err != nil {
		t.Fatal(err)
	}
	f.fat[chain[len(chain)-1]] = chain[0]
	if _, err := f.Open("LOOP.BIN"); err == nil || !strings.Contains(err.Error(), "loops") {
		t.Fatalf("err = %v, want a loop error", err)
	}

	if _, err := New(bytes.NewReader(make([]byte, 4096)), 0); err == nil {
		t.Fatal("zeroed sector accepted as a FAT volume")
	}
}

func (f *FS) lookupCluster(t *testing.T, name string) uint32 {
	t.Helper()
	e, err := f.lookup(name)
	if err != nil {
		t.Fatal(err)
	}
	return e.cluster
}

func TestReadAtPastEnd(t *testing.T) {
	f, err := Open(bytes.NewReader(buildFAT(t, FAT12, []*node{{short: "A       TXT", data: []byte("abc")}})))
	if err != nil {
		t.Fatal(err)
	}
	fl, _ := f.Open("A.TXT")
	ra := fl.(io.ReaderAt)
	p := make([]byte, 8)
	if n, err := ra.ReadAt(p, 1); n != 2 || err != io.EOF || string(p[:n]) != "bc" {
		t.Fatalf("ReadAt = %d %v %q", n, err, p[:n])
	}
}

// countingReaderAt counts ReadAt calls.
type countingReaderAt struct {
	r     io.ReaderAt
	calls atomic.Int64
}

func (c *countingReaderAt) ReadAt(p []byte, off int64) (int, error) {
	c.calls.Add(1)
	return c.r.ReadAt(p, off)
}

func TestDirectoriesReadOnce(t *testing.T) {
	for _, kind := range []Kind{FAT12, FAT16, FAT32} {
		r := &countingReaderAt{r: bytes.NewReader(buildFAT(t, kind, testTree()))}
		f, err := Open(r)
		if err != nil {
			t.Fatal(err)
		}
		// Opening a file reads no file data, only the directories on its
		// path; once those are cached, opening it again reads nothing.
		paths := []string{"Program Files/A long file name.txt", "Program Files/FRAG.BIN", "AUTOEXEC.BAT"}
		for _, p := range paths {
			if _, err := f.Open(p); err != nil {
				t.Fatal(err)
			}
		}
		before := r.calls.Load()
		for range 10 {
			for _, p := range paths {
				if _, err := f.Open(p); err != nil {
					t.Fatal(err)
				}
			}
		}
		if n := r.calls.Load() - before; n != 0 {
			t.Errorf("FAT%d: %d reads reopening cached paths, want 0", kind, n)
		}
	}
}

func TestConcurrentWalk(t *testing.T) {
	f, err := Open(bytes.NewReader(buildFAT(t, FAT32, testTree())))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := fstest.TestFS(f, "AUTOEXEC.BAT", "Program Files/FRAG.BIN"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}
