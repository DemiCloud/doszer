package vdisk

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"io/fs"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/DemiCloud/doszer/internal/fat"
)

// The expected values below are worked out by hand from the DOSBox Pure
// source this package ports. The port as a whole was checked against a
// real archive and .sav file: it reproduced DOSBox Pure's save hash
// exactly, the save's changed directory sectors differed from the
// reconstruction only in access dates, and all 1236 files read back
// identical to the archive (AGENTS.md).

func TestMake8dot3(t *testing.T) {
	tests := []struct {
		in, want string
		changed  bool
	}{
		{"README.TXT", "README.TXT", false},
		{"readme.txt", "README.TXT", true},
		{"ANTARA", "ANTARA", false},
		{"Program Files", "PROGILES", true},     // first 4 + last 4 of the base
		{"LONGNAME.HTML", "LONGNAME.HTM", true}, // extension cut to 3
		{"A B.C", "A-B.C", true},                // invalid ASCII becomes '-'
		{".profile", "-.PRO", true},             // empty base
		{"archive.tar.gz", "ARCH-TAR.GZ", true}, // inner dot is an invalid character
		{"caf\x82.txt", "CAFE.TXT", true},       // DOS_ToUpper maps code page 437 é to E
	}
	for _, tt := range tests {
		got, changed := make8dot3(tt.in)
		if got != tt.want || changed != tt.changed {
			t.Errorf("make8dot3(%q) = %q, %v; want %q, %v", tt.in, got, changed, tt.want, tt.changed)
		}
	}
}

func TestIncrementName(t *testing.T) {
	tests := []struct{ in, want string }{
		{"PROGILES", "PROGJLES"}, // base 8: j = 4
		{"GAME.EXE", "GANE.EXE"}, // base 4: j = 2
		{"A.TXT", "B.TXT"},
		{"AB~~~", "AB~~~"}, // no candidate below '~'
	}
	for _, tt := range tests {
		got, _ := incrementName(tt.in)
		if got != tt.want {
			t.Errorf("incrementName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestVolumeLabel(t *testing.T) {
	tests := []struct{ in, want string }{
		{"/roms/Betrayal in Antara.dosz", "BETRAYA-.DOS"},
		{"Game.dosz", "GAME-DOS.Z"},
		{"Example Game (USA).dosz", "EXAMPLE-.DOS"},
		{"Ab (1).zip", "AB-ZIP"},
	}
	for _, tt := range tests {
		if got := volumeLabel(tt.in); got != tt.want {
			t.Errorf("volumeLabel(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestHashMapOrder(t *testing.T) {
	// Slot order, not insertion order, is what FindNext returns.
	var m hashMap[string]
	names := []string{"WINDOWS.BAT", "ANTARA", "AUTOBOOT.DBP", "README.TXT"}
	for _, n := range names {
		m.put(fnvHash(n), n)
	}
	got := m.inOrder()
	var want []string
	type slot struct {
		idx  uint32
		name string
	}
	var slots []slot
	for _, n := range names {
		h := fnvHash(n)
		for i := h; ; i++ {
			if m.keys[i&m.maxlen] == h {
				slots = append(slots, slot{i & m.maxlen, n})
				break
			}
		}
	}
	sort.Slice(slots, func(a, b int) bool { return slots[a].idx < slots[b].idx })
	for _, s := range slots {
		want = append(want, s.name)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("order %v, want %v", got, want)
	}
	if v, ok := m.get(fnvHash("ANTARA")); !ok || v != "ANTARA" {
		t.Fatal("get failed")
	}
	// Bytes >= 0x80 are sign-extended, as with a signed C char.
	basis, prime := uint32(0x811c9dc5), uint32(0x01000193)
	if fnvHash("\x82") != (basis*prime)^0xFFFFFF82 {
		t.Fatal("fnvHash must sign-extend high bytes")
	}
}

// content builds a content archive in memory.
func content(t *testing.T, files map[string]string) *zip.Reader {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	var names []string
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: n, Method: zip.Deflate, Modified: time.Date(1996, 12, 24, 23, 32, 0, 0, time.UTC)})
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(files[n]))
	}
	zw.Close()
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	return zr
}

var testFiles = map[string]string{
	"AUTOBOOT.DBP":                "hidden runtime file",
	"WINDOWS.BAT":                 "@ECHO OFF\r\n",
	"GAME/GAME.EXE":               strings.Repeat("MZ", 40000), // several clusters
	"GAME/readme.txt":             "lower case name",
	"Program Files/Example/A.TXT": "nested long directory names",
	"Program Files/Example/B.TXT": "",
	"A long file name.txt":        "long",
	"CD/DISC.ISO":                 "an image: hidden",
	"CD2/Disc.cue":                "FILE \"Disc.iso\" BINARY\r\n",
	"CD2/Disc.iso":                "beside a CUE of the same name: visible",
	"GAME/SEEK.SKC":               "seek cache: hidden",
}

func readTree(t *testing.T, fsys fs.FS) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(fsys, p)
		out[p] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestBuildRoundTrip(t *testing.T) {
	want := map[string]string{
		"WINDOWS.BAT":                 testFiles["WINDOWS.BAT"],
		"GAME/GAME.EXE":               testFiles["GAME/GAME.EXE"],
		"GAME/readme.txt":             testFiles["GAME/readme.txt"],
		"Program Files/Example/A.TXT": testFiles["Program Files/Example/A.TXT"],
		"Program Files/Example/B.TXT": "",
		"A long file name.txt":        "long",
		"CD2/Disc.iso":                testFiles["CD2/Disc.iso"],
	}
	tests := []struct {
		name      string
		freeSpace int
		kind      fat.Kind
	}{
		{"FAT12, read-only (no free space)", 0, fat.FAT12},
		{"FAT16, default free space", DefaultFreeSpaceMB, fat.FAT16},
		{"FAT32", 3000, fat.FAT32},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := Build(content(t, testFiles), "Example Game.dosz", tt.freeSpace)
			if err != nil {
				t.Fatal(err)
			}
			fsys, err := fat.Open(d)
			if err != nil {
				t.Fatal(err)
			}
			if fsys.Kind() != tt.kind {
				t.Fatalf("FAT%d, want FAT%d", fsys.Kind(), tt.kind)
			}
			got := readTree(t, fsys)
			if len(got) != len(want) {
				t.Errorf("files %v", keys(got))
			}
			for p, w := range want {
				if got[p] != w {
					t.Errorf("%s = %.30q, want %.30q", p, got[p], w)
				}
			}
			info, err := fs.Stat(fsys, "WINDOWS.BAT")
			if err != nil || !info.ModTime().Equal(time.Date(1996, 12, 24, 23, 32, 0, 0, time.UTC)) {
				t.Errorf("WINDOWS.BAT time = %v, %v", info.ModTime(), err)
			}
			// Short names are generated from long names as Windows would.
			for _, short := range []string{"PROGRA~1/EXAMPLE/A.TXT", "ALONGF~1.TXT", "GAME/README.TXT"} {
				if _, err := fs.Stat(fsys, short); err != nil {
					t.Errorf("short name %s: %v", short, err)
				}
			}
		})
	}
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestSaveHashTracksContent(t *testing.T) {
	a, _ := Build(content(t, testFiles), "x.dosz", DefaultFreeSpaceMB)
	b, _ := Build(content(t, testFiles), "x.dosz", DefaultFreeSpaceMB)
	if a.Hash != b.Hash {
		t.Fatal("hash not deterministic")
	}
	changed := map[string]string{}
	for k, v := range testFiles {
		changed[k] = v
	}
	changed["WINDOWS.BAT"] += "REM\r\n" // size changes the hash
	c, _ := Build(content(t, changed), "x.dosz", DefaultFreeSpaceMB)
	d, _ := Build(content(t, testFiles), "x.dosz", 512)
	if c.Hash == a.Hash || d.Hash == a.Hash {
		t.Fatal("hash ignores content or free space")
	}
	hidden := map[string]string{}
	for k, v := range testFiles {
		hidden[k] = v
	}
	hidden["AUTOBOOT.DBP"] = "different hidden content, different size"
	if e, _ := Build(content(t, hidden), "x.dosz", DefaultFreeSpaceMB); e.Hash != a.Hash {
		t.Fatal("hash covers a filtered file")
	}
	if got := a.SaveName("Example Game"); !strings.HasPrefix(got, "Example Game-") || !strings.HasSuffix(got, ".sav") || len(got) != len("Example Game-12345678.sav") {
		t.Fatalf("SaveName = %q", got)
	}
}

// ffdd builds a .sav file from sector records; a negative sector writes a
// freed slot.
func ffdd(records map[int64][]byte) []byte {
	out := []byte("FFDD\x01")
	var sects []int64
	for s := range records {
		sects = append(sects, s)
	}
	sort.Slice(sects, func(i, j int) bool { return sects[i] < sects[j] })
	for _, s := range sects {
		num := make([]byte, 4)
		if s < 0 {
			binary.LittleEndian.PutUint32(num, 0xFFFFFFFF)
		} else {
			binary.LittleEndian.PutUint32(num, uint32(s))
		}
		data := make([]byte, 512)
		copy(data, records[s])
		out = append(append(out, num...), data...)
	}
	return out
}

func TestApplySave(t *testing.T) {
	d, err := Build(content(t, testFiles), "Example Game.dosz", DefaultFreeSpaceMB)
	if err != nil {
		t.Fatal(err)
	}
	fsys, _ := fat.Open(d)
	// Find WINDOWS.BAT's first sector through the FAT reader, then
	// overwrite it in a save.
	var sect int64 = -1
	for s := int64(d.sectFiles); s < int64(d.sectFilesEnd); s++ {
		buf := make([]byte, 512)
		d.ReadAt(buf, s*512)
		if bytes.HasPrefix(buf, []byte("@ECHO OFF\r\n")) {
			sect = s
			break
		}
	}
	if sect < 0 {
		t.Fatal("WINDOWS.BAT data not found")
	}
	sav := ffdd(map[int64][]byte{sect: []byte("@ECHO ON!\r\n"), -1: []byte("freed slot")})
	if err := d.ApplySave(bytes.NewReader(sav), int64(len(sav))); err != nil {
		t.Fatal(err)
	}
	if d.ChangedSectors() != 1 {
		t.Errorf("ChangedSectors = %d, want 1", d.ChangedSectors())
	}
	fsys, _ = fat.Open(d)
	if b, _ := fs.ReadFile(fsys, "WINDOWS.BAT"); string(b) != "@ECHO ON!\r\n" {
		t.Errorf("WINDOWS.BAT = %q after save", b)
	}

	bad := []struct {
		name string
		data []byte
	}{
		{"bad header", []byte("NOPE\x01")},
		{"sector past the end", ffdd(map[int64][]byte{int64(d.sectEnd): nil})},
	}
	for _, tt := range bad {
		if err := d.ApplySave(bytes.NewReader(tt.data), int64(len(tt.data))); err == nil {
			t.Errorf("%s: accepted", tt.name)
		}
	}
}

func TestBuildRejectsUnsupported(t *testing.T) {
	tests := map[string]map[string]string{
		"parent archive":   {"Base.parent": "", "GAME.EXE": "x"},
		"large disk image": {"HDD.IMG": strings.Repeat("\x00", 3*1024*1024)},
	}
	for name, files := range tests {
		if _, err := Build(content(t, files), "x.dosz", DefaultFreeSpaceMB); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestOverlay(t *testing.T) {
	base := bytes.Repeat([]byte{0xAA}, 8*512)
	sav := ffdd(map[int64][]byte{2: []byte("changed"), -1: nil})
	o, err := NewOverlay(bytes.NewReader(base), int64(len(base)), bytes.NewReader(sav), int64(len(sav)))
	if err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 3*512)
	if _, err := o.ReadAt(got, 512); err != nil {
		t.Fatal(err)
	}
	want := append(append(bytes.Repeat([]byte{0xAA}, 512), append([]byte("changed"), make([]byte, 512-7)...)...), bytes.Repeat([]byte{0xAA}, 512)...)
	if !bytes.Equal(got, want) || o.ChangedSectors() != 1 {
		t.Fatal("overlay content differs")
	}
	big := ffdd(map[int64][]byte{8: nil})
	if _, err := NewOverlay(bytes.NewReader(base), int64(len(base)), bytes.NewReader(big), int64(len(big))); err == nil {
		t.Fatal("sector past the end accepted")
	}
}
