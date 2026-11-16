package iso9660

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"
	"time"
)

// dirRecord builds one Directory Record (ECMA-119 §9.1).
func dirRecord(id string, isDir bool, lba, size uint32) []byte {
	idLen := len(id)
	total := 33 + idLen
	if total%2 != 0 {
		total++
	}
	rec := make([]byte, total)
	rec[0] = byte(total)
	binary.LittleEndian.PutUint32(rec[2:6], lba)
	binary.BigEndian.PutUint32(rec[6:10], lba)
	binary.LittleEndian.PutUint32(rec[10:14], size)
	binary.BigEndian.PutUint32(rec[14:18], size)
	if isDir {
		rec[25] = dirFlag
	}
	rec[32] = byte(idLen)
	copy(rec[33:], id)
	return rec
}

// isoBuilder assembles a minimal ISO 9660 image, one entry per sector, for
// testing the reader.
type isoBuilder struct {
	sectors [][]byte
}

func newISOBuilder() *isoBuilder {
	b := &isoBuilder{}
	for i := 0; i < 16; i++ {
		b.sectors = append(b.sectors, make([]byte, sectorSize)) // System Area
	}
	b.sectors = append(b.sectors, nil) // 16: PVD, filled in by build()
	b.sectors = append(b.sectors, nil) // 17: terminator, filled in by build()
	return b
}

// addSector appends content as a new sector (zero-padded to sectorSize)
// and returns its LBA.
func (b *isoBuilder) addSector(content []byte) uint32 {
	sector := make([]byte, sectorSize)
	copy(sector, content)
	b.sectors = append(b.sectors, sector)
	return uint32(len(b.sectors) - 1)
}

// build finalizes the image: writes the PVD (with the given root directory
// record) and terminator at their fixed sectors, then concatenates.
func (b *isoBuilder) build(rootRecord []byte) []byte {
	pvd := make([]byte, sectorSize)
	pvd[0] = 1
	copy(pvd[1:6], "CD001")
	pvd[6] = 1
	copy(pvd[156:190], rootRecord)
	b.sectors[16] = pvd

	term := make([]byte, sectorSize)
	term[0] = 255
	copy(term[1:6], "CD001")
	b.sectors[17] = term

	var out bytes.Buffer
	for _, s := range b.sectors {
		out.Write(s)
	}
	return out.Bytes()
}

// buildFixture lays out:
//
//	/FILE1.TXT;1
//	/SUBDIR/FILE2.TXT;1
func buildFixture(t *testing.T) *Image {
	t.Helper()
	b := newISOBuilder()

	file1 := []byte("hello from file1")
	file1LBA := b.addSector(file1)

	file2 := []byte("hello from file2, in a subdirectory")
	file2LBA := b.addSector(file2)

	subdirContent := bytes.Join([][]byte{
		dirRecord("\x00", true, 0, 0), // "."
		dirRecord("\x01", true, 0, 0), // ".."
		dirRecord("FILE2.TXT;1", false, file2LBA, uint32(len(file2))),
	}, nil)
	subdirLBA := b.addSector(subdirContent)

	rootContent := bytes.Join([][]byte{
		dirRecord("\x00", true, 0, 0),
		dirRecord("\x01", true, 0, 0),
		dirRecord("FILE1.TXT;1", false, file1LBA, uint32(len(file1))),
		dirRecord("SUBDIR", true, subdirLBA, uint32(len(subdirContent))),
	}, nil)
	rootLBA := b.addSector(rootContent)

	root := dirRecord("\x00", true, rootLBA, uint32(len(rootContent)))
	data := b.build(root)

	img, err := Open(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return img
}

func TestOpenAndReadRootDir(t *testing.T) {
	img := buildFixture(t)

	entries, err := img.ReadDir(img.Root())
	if err != nil {
		t.Fatalf("ReadDir(root): %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2 (dot-entries must be excluded)", len(entries))
	}

	names := map[string]bool{}
	for _, e := range entries {
		names[e.Name] = true
	}
	if !names["FILE1.TXT"] || !names["SUBDIR"] {
		t.Errorf("entries = %+v, want FILE1.TXT and SUBDIR", entries)
	}
}

func TestLookupFile(t *testing.T) {
	img := buildFixture(t)

	e, err := img.Lookup("file1.txt") // case-insensitive, no version suffix
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if e.IsDir {
		t.Fatal("FILE1.TXT should not be a directory")
	}

	r, err := img.OpenFile(e)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != "hello from file1" {
		t.Errorf("content = %q", got)
	}
}

func TestLookupNestedFile(t *testing.T) {
	img := buildFixture(t)

	e, err := img.Lookup("SUBDIR/FILE2.TXT")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}

	r, err := img.OpenFile(e)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != "hello from file2, in a subdirectory" {
		t.Errorf("content = %q", got)
	}
}

func TestLookupDirectory(t *testing.T) {
	img := buildFixture(t)

	e, err := img.Lookup("SUBDIR")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if !e.IsDir {
		t.Fatal("SUBDIR should be a directory")
	}

	entries, err := img.ReadDir(e)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "FILE2.TXT" {
		t.Errorf("entries = %+v", entries)
	}
}

func TestLookupNotFound(t *testing.T) {
	img := buildFixture(t)
	if _, err := img.Lookup("NOPE.TXT"); err == nil {
		t.Fatal("Lookup should fail for a missing path")
	}
}

func TestReadDirSkipsZeroPaddingToSectorBoundary(t *testing.T) {
	// A directory extent that declares a full-sector size, with real
	// records followed by zero bytes: the zero-length-record convention
	// must be treated as padding, not a parse error.
	b := newISOBuilder()
	file1 := []byte("x")
	file1LBA := b.addSector(file1)

	rootContent := make([]byte, sectorSize) // size says a whole sector
	copy(rootContent, bytes.Join([][]byte{
		dirRecord("\x00", true, 0, 0),
		dirRecord("\x01", true, 0, 0),
		dirRecord("A.TXT;1", false, file1LBA, uint32(len(file1))),
	}, nil))
	rootLBA := b.addSector(rootContent)

	root := dirRecord("\x00", true, rootLBA, sectorSize)
	img, err := Open(bytes.NewReader(b.build(root)))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	entries, err := img.ReadDir(img.Root())
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "A.TXT" {
		t.Errorf("entries = %+v, want just A.TXT", entries)
	}
}

func TestParseRecordingTime(t *testing.T) {
	// 1994-03-15 12:30:45, recorded at GMT+2 (8 intervals of 15 minutes).
	b := []byte{94, 3, 15, 12, 30, 45, 8}
	got := parseRecordingTime(b)
	want := time.Date(1994, 3, 15, 12, 30, 45, 0, time.FixedZone("", 2*60*60)).UTC()
	if !got.Equal(want) {
		t.Errorf("parseRecordingTime(%v) = %v, want %v", b, got, want)
	}
}

func TestParseRecordingTimeUnset(t *testing.T) {
	if got := parseRecordingTime(make([]byte, 7)); !got.IsZero() {
		t.Errorf("parseRecordingTime(zero bytes) = %v, want zero time", got)
	}
}

func TestParseRecordingTimeInvalid(t *testing.T) {
	// Month 13 is out of range.
	b := []byte{94, 13, 40, 25, 70, 70, 0}
	if got := parseRecordingTime(b); !got.IsZero() {
		t.Errorf("parseRecordingTime(invalid) = %v, want zero time", got)
	}
}

func TestDirRecordIncludesModified(t *testing.T) {
	dir := buildFixture(t)
	e, err := dir.Lookup("FILE1.TXT")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if !e.Modified.IsZero() {
		t.Errorf("Modified = %v, want zero (fixture never sets a date)", e.Modified)
	}
}

func TestFileName(t *testing.T) {
	tests := []struct {
		id    string
		isDir bool
		want  string
	}{
		{"FILE1.TXT;1", false, "FILE1.TXT"},
		{"README.;1", false, "README"},
		{"README.", false, "README"},
		{"README;1", false, "README"},
		{"A.B.C;2", false, "A.B.C"},
		{"SUBDIR", true, "SUBDIR"},
		{"\x00", true, "\x00"},
	}
	for _, tt := range tests {
		if got := fileName(tt.id, tt.isDir); got != tt.want {
			t.Errorf("fileName(%q, %v) = %q, want %q", tt.id, tt.isDir, got, tt.want)
		}
	}
}

func TestLookupFileWithoutExtension(t *testing.T) {
	b := newISOBuilder()
	content := []byte("no extension")
	lba := b.addSector(content)
	rootContent := bytes.Join([][]byte{
		dirRecord("\x00", true, 0, 0),
		dirRecord("\x01", true, 0, 0),
		dirRecord("README.;1", false, lba, uint32(len(content))),
	}, nil)
	rootLBA := b.addSector(rootContent)
	img, err := Open(bytes.NewReader(b.build(dirRecord("\x00", true, rootLBA, uint32(len(rootContent))))))
	if err != nil {
		t.Fatal(err)
	}
	e, err := img.Lookup("readme")
	if err != nil {
		t.Fatal(err)
	}
	if e.Name != "README" {
		t.Errorf("Name = %q, want README", e.Name)
	}
}
