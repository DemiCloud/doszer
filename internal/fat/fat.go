// Package fat reads FAT12, FAT16 and FAT32 file systems, with VFAT long
// file names, as a read-only io/fs.FS - the file systems in the OS images
// and disk images `install` extracts from (README "Install").
//
// Format: Microsoft "FAT: General Overview of On-Disk Format" (fatgen103).
// All fields are little-endian.
package fat

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
)

// Kind is a FAT variant, determined by cluster count (fatgen103 §3.5).
type Kind int

const (
	FAT12 Kind = 12
	FAT16 Kind = 16
	FAT32 Kind = 32
)

// MBR partition types holding a FAT file system.
var fatPartitionTypes = map[byte]bool{
	0x01: true, // FAT12
	0x04: true, // FAT16 < 32 MB
	0x06: true, // FAT16
	0x0B: true, // FAT32 (CHS)
	0x0C: true, // FAT32 (LBA)
	0x0E: true, // FAT16 (LBA)
}

const (
	attrReadOnly  = 0x01
	attrHidden    = 0x02
	attrSystem    = 0x04
	attrVolumeID  = 0x08
	attrDirectory = 0x10
	attrLFN       = 0x0F // read-only|hidden|system|volume ID
)

// FS is a FAT file system. It is safe for concurrent use: it only reads,
// and its directory cache is locked.
type FS struct {
	r    io.ReaderAt
	base int64 // byte offset of the volume within r
	kind Kind

	bytesPerSector    int64
	clusterSize       int64
	firstDataSector   int64
	rootDirSector     int64 // FAT12/16: first sector of the fixed root directory
	rootDirEntries    int64 // FAT12/16
	rootCluster       uint32
	clusterCount      uint32
	fat               []uint32 // the first FAT, decoded
	endOfChainAtLeast uint32

	// Every directory read, by first cluster (0: FAT12/16's fixed root).
	// Opening a path reads each directory on it, so walking a whole disk
	// would otherwise reread every ancestor for every file.
	mu   sync.Mutex
	dirs map[uint32][]*entry
}

// Open finds the FAT file system on a disk: the first FAT partition in an
// MBR partition table, or, failing that, a volume starting at sector 0 (an
// unpartitioned "superfloppy" disk, such as a floppy image).
func Open(r io.ReaderAt) (*FS, error) {
	sector := make([]byte, 512)
	if _, err := r.ReadAt(sector, 0); err != nil {
		return nil, fmt.Errorf("read sector 0: %w", err)
	}
	if sector[510] == 0x55 && sector[511] == 0xAA {
		for i := 0; i < 4; i++ {
			e := sector[446+i*16:]
			lba := binary.LittleEndian.Uint32(e[8:])
			if fatPartitionTypes[e[4]] && lba != 0 {
				fsys, err := New(r, int64(lba)*512)
				if err != nil {
					return nil, fmt.Errorf("partition %d: %w", i+1, err)
				}
				return fsys, nil
			}
		}
	}
	fsys, err := New(r, 0)
	if err != nil {
		return nil, fmt.Errorf("no FAT partition, and sector 0 is not a FAT volume: %w", err)
	}
	return fsys, nil
}

// New reads the FAT volume starting at byte offset base of r.
func New(r io.ReaderAt, base int64) (*FS, error) {
	bs := make([]byte, 512)
	if _, err := r.ReadAt(bs, base); err != nil {
		return nil, fmt.Errorf("read boot sector: %w", err)
	}
	le := binary.LittleEndian
	bytesPerSector := int64(le.Uint16(bs[11:]))
	sectorsPerCluster := int64(bs[13])
	reserved := int64(le.Uint16(bs[14:]))
	numFATs := int64(bs[16])
	rootEntries := int64(le.Uint16(bs[17:]))
	totalSectors := int64(le.Uint16(bs[19:]))
	if totalSectors == 0 {
		totalSectors = int64(le.Uint32(bs[32:]))
	}
	fatSize := int64(le.Uint16(bs[22:]))
	if fatSize == 0 {
		fatSize = int64(le.Uint32(bs[36:]))
	}

	switch {
	case bytesPerSector != 512 && bytesPerSector != 1024 && bytesPerSector != 2048 && bytesPerSector != 4096:
		return nil, fmt.Errorf("not a FAT boot sector (bytes per sector %d)", bytesPerSector)
	case sectorsPerCluster == 0 || sectorsPerCluster&(sectorsPerCluster-1) != 0:
		return nil, fmt.Errorf("not a FAT boot sector (sectors per cluster %d)", sectorsPerCluster)
	case reserved == 0 || numFATs == 0 || fatSize == 0 || totalSectors == 0:
		return nil, errors.New("not a FAT boot sector (zero reserved sectors, FATs, FAT size or total sectors)")
	}

	rootDirSectors := (rootEntries*32 + bytesPerSector - 1) / bytesPerSector
	firstData := reserved + numFATs*fatSize + rootDirSectors
	if firstData >= totalSectors {
		return nil, errors.New("FAT boot sector: no data region")
	}
	clusterCount := (totalSectors - firstData) / sectorsPerCluster

	f := &FS{
		r:               r,
		base:            base,
		bytesPerSector:  bytesPerSector,
		clusterSize:     bytesPerSector * sectorsPerCluster,
		firstDataSector: firstData,
		rootDirSector:   reserved + numFATs*fatSize,
		rootDirEntries:  rootEntries,
		clusterCount:    uint32(clusterCount),
		dirs:            map[uint32][]*entry{},
	}
	switch {
	case clusterCount < 4085:
		f.kind, f.endOfChainAtLeast = FAT12, 0xFF8
	case clusterCount < 65525:
		f.kind, f.endOfChainAtLeast = FAT16, 0xFFF8
	default:
		f.kind, f.endOfChainAtLeast = FAT32, 0x0FFFFFF8
		f.rootCluster = le.Uint32(bs[44:])
	}

	if err := f.readFAT(reserved*bytesPerSector, fatSize*bytesPerSector); err != nil {
		return nil, err
	}
	return f, nil
}

// Kind returns the FAT variant.
func (f *FS) Kind() Kind { return f.kind }

// readFAT decodes the first FAT, covering clusters 0 to clusterCount+1.
func (f *FS) readFAT(off, size int64) error {
	raw := make([]byte, size)
	if _, err := f.r.ReadAt(raw, f.base+off); err != nil {
		return fmt.Errorf("read FAT: %w", err)
	}
	n := int(f.clusterCount) + 2
	f.fat = make([]uint32, n)
	le := binary.LittleEndian
	for c := 0; c < n; c++ {
		switch f.kind {
		case FAT12:
			i := c * 3 / 2
			if i+1 >= len(raw) {
				return errors.New("FAT too small for its cluster count")
			}
			v := uint32(le.Uint16(raw[i:]))
			if c&1 == 1 {
				v >>= 4
			}
			f.fat[c] = v & 0xFFF
		case FAT16:
			if c*2+2 > len(raw) {
				return errors.New("FAT too small for its cluster count")
			}
			f.fat[c] = uint32(le.Uint16(raw[c*2:]))
		case FAT32:
			if c*4+4 > len(raw) {
				return errors.New("FAT too small for its cluster count")
			}
			f.fat[c] = le.Uint32(raw[c*4:]) & 0x0FFFFFFF
		}
	}
	return nil
}

// chain returns a cluster chain starting at first, rejecting loops and
// out-of-range clusters.
func (f *FS) chain(first uint32) ([]uint32, error) {
	var out []uint32
	for c := first; c < f.endOfChainAtLeast; c = f.fat[c] {
		if c < 2 || c >= f.clusterCount+2 {
			return nil, fmt.Errorf("cluster chain from %d: invalid cluster %d", first, c)
		}
		if len(out) > int(f.clusterCount) {
			return nil, fmt.Errorf("cluster chain from %d loops", first)
		}
		out = append(out, c)
	}
	return out, nil
}

func (f *FS) clusterOffset(c uint32) int64 {
	return f.base + (f.firstDataSector*f.bytesPerSector + int64(c-2)*f.clusterSize)
}

// entry is one directory entry, long name resolved.
type entry struct {
	name     string // long name if present, else the short name
	short    string // 8.3 name, always matched by lookup too
	attr     byte
	cluster  uint32
	size     uint32
	modified time.Time
}

func (e *entry) isDir() bool { return e.attr&attrDirectory != 0 }

// readDir returns a directory's entries: the fixed root (dir == nil on
// FAT12/16) or a cluster chain. Entries are shared between callers and
// never modified.
func (f *FS) readDir(dir *entry) ([]*entry, error) {
	var key uint32 // the fixed root
	if dir != nil {
		key = dir.cluster // 0 for ".." pointing at FAT12/16's root: the same
	} else if f.kind == FAT32 {
		key = f.rootCluster
	}
	f.mu.Lock()
	entries, ok := f.dirs[key]
	f.mu.Unlock()
	if ok {
		return entries, nil
	}
	entries, err := f.readDirUncached(dir)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.dirs[key] = entries
	f.mu.Unlock()
	return entries, nil
}

func (f *FS) readDirUncached(dir *entry) ([]*entry, error) {
	var data []byte
	if dir == nil && f.kind != FAT32 {
		data = make([]byte, f.rootDirEntries*32)
		if _, err := f.r.ReadAt(data, f.base+f.rootDirSector*f.bytesPerSector); err != nil {
			return nil, fmt.Errorf("read root directory: %w", err)
		}
	} else {
		first := f.rootCluster
		if dir != nil {
			first = dir.cluster
		}
		if first == 0 { // ".." pointing at the root on FAT12/16
			return f.readDir(nil)
		}
		clusters, err := f.chain(first)
		if err != nil {
			return nil, err
		}
		data = make([]byte, int64(len(clusters))*f.clusterSize)
		for i, c := range clusters {
			if _, err := f.r.ReadAt(data[int64(i)*f.clusterSize:int64(i+1)*f.clusterSize], f.clusterOffset(c)); err != nil {
				return nil, fmt.Errorf("read directory cluster %d: %w", c, err)
			}
		}
	}
	return parseDir(data), nil
}

// parseDir decodes 32-byte directory entries, joining VFAT long-name
// entries onto the short entry they precede. A long name whose checksum
// doesn't match its short entry (left behind by a non-VFAT tool) is
// ignored in favour of the short name.
func parseDir(data []byte) []*entry {
	var out []*entry
	var lfn []uint16
	var lfnSum byte
	lfnNext := 0 // expected next sequence number counting down; 0: none in progress

	for i := 0; i+32 <= len(data); i += 32 {
		d := data[i : i+32]
		switch d[0] {
		case 0x00:
			return out // end of directory
		case 0xE5:
			lfn, lfnNext = nil, 0
			continue // deleted
		}
		attr := d[11]

		if attr&0x3F == attrLFN {
			seq := int(d[0] & 0x1F)
			if d[0]&0x40 != 0 { // last (first stored) entry of a long name
				lfn = make([]uint16, seq*13)
				lfnSum, lfnNext = d[13], seq
			}
			if lfn == nil || seq == 0 || seq != lfnNext || d[13] != lfnSum {
				lfn, lfnNext = nil, 0 // broken sequence: fall back to the short name
				continue
			}
			units := lfn[(seq-1)*13 : seq*13]
			for j, off := range []int{1, 3, 5, 7, 9, 14, 16, 18, 20, 22, 24, 28, 30} {
				units[j] = binary.LittleEndian.Uint16(d[off:])
			}
			lfnNext--
			continue
		}

		haveLFN := lfnNext == 0 && lfn != nil && lfnSum == shortNameChecksum(d[0:11])
		var long []uint16
		if haveLFN {
			long = lfn
		}
		lfn, lfnNext = nil, 0

		if attr&attrVolumeID != 0 {
			continue
		}
		short := shortName(d)
		if short == "." || short == ".." {
			continue
		}
		name := short
		if long != nil {
			if end := indexUnit(long, 0x0000); end >= 0 {
				long = long[:end]
			}
			name = string(utf16.Decode(long))
		}

		le := binary.LittleEndian
		cluster := uint32(le.Uint16(d[26:])) | uint32(le.Uint16(d[20:]))<<16
		out = append(out, &entry{
			name:     name,
			short:    short,
			attr:     attr,
			cluster:  cluster,
			size:     le.Uint32(d[28:]),
			modified: dosTime(le.Uint16(d[24:]), le.Uint16(d[22:])),
		})
	}
	return out
}

func indexUnit(s []uint16, u uint16) int {
	for i, v := range s {
		if v == u {
			return i
		}
	}
	return -1
}

// shortName decodes an 8.3 name, applying the lowercase flags Windows NT
// stores in byte 12 (0x08: base, 0x10: extension). Bytes are code page 437.
func shortName(d []byte) string {
	raw := append([]byte{}, d[0:11]...)
	if raw[0] == 0x05 {
		raw[0] = 0xE5 // 0xE5 as a real first character
	}
	base := decode437(strings.TrimRight(string(raw[0:8]), " "))
	ext := decode437(strings.TrimRight(string(raw[8:11]), " "))
	if d[12]&0x08 != 0 {
		base = strings.ToLower(base)
	}
	if d[12]&0x10 != 0 {
		ext = strings.ToLower(ext)
	}
	if ext == "" {
		return base
	}
	return base + "." + ext
}

// shortNameChecksum is the VFAT checksum of an 11-byte short name.
func shortNameChecksum(name []byte) byte {
	var sum byte
	for _, c := range name {
		sum = (sum>>1 | sum<<7) + c
	}
	return sum
}

// dosTime converts FAT date/time fields to a time.Time. FAT records local
// wall-clock time with no zone; it is returned as that wall clock in UTC,
// so it converts back to the same DOS fields in an archive (spec §8.2).
func dosTime(date, tm uint16) time.Time {
	if date == 0 {
		return time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	return time.Date(1980+int(date>>9), time.Month(date>>5&0x0F), int(date&0x1F),
		int(tm>>11), int(tm>>5&0x3F), int(tm&0x1F)*2, 0, time.UTC)
}

// lookup resolves a slash-separated path, matching each component
// case-insensitively against long names first, then short names (DOS
// semantics: PROGRA~1 and "Program Files" name the same directory).
func (f *FS) lookup(name string) (*entry, error) {
	if name == "." {
		return nil, nil // the root
	}
	var cur *entry
	// The directories passed through, by first cluster (0: FAT12/16's
	// fixed root; "." and ".." are never entries). A damaged directory
	// pointing back at one of them would make a walk of the file system
	// descend forever.
	seen := map[uint32]bool{0: true}
	if f.kind == FAT32 {
		seen[f.rootCluster] = true
	}
	for _, part := range strings.Split(name, "/") {
		if cur != nil && !cur.isDir() {
			return nil, fs.ErrNotExist
		}
		entries, err := f.readDir(cur)
		if err != nil {
			return nil, err
		}
		var found *entry
		for _, e := range entries {
			if strings.EqualFold(e.name, part) {
				found = e
				break
			}
		}
		for _, e := range entries {
			if found == nil && strings.EqualFold(e.short, part) {
				found = e
			}
		}
		if found == nil {
			return nil, fs.ErrNotExist
		}
		if found.isDir() {
			if seen[found.cluster] {
				return nil, fmt.Errorf("directory %s loops back to cluster %d (a damaged file system)", part, found.cluster)
			}
			seen[found.cluster] = true
		}
		cur = found
	}
	return cur, nil
}

// Open implements fs.FS.
func (f *FS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	e, err := f.lookup(name)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: err}
	}
	if e == nil || e.isDir() {
		return &dir{fsys: f, e: e, name: name}, nil
	}
	clusters, err := f.chain(e.cluster)
	if err != nil && e.size > 0 {
		return nil, &fs.PathError{Op: "open", Path: name, Err: err}
	}
	if int64(len(clusters))*f.clusterSize < int64(e.size) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fmt.Errorf("cluster chain shorter than file size %d", e.size)}
	}
	return &file{fsys: f, e: e, clusters: clusters}, nil
}

// ReadDir implements fs.ReadDirFS, sorted by name.
func (f *FS) ReadDir(name string) ([]fs.DirEntry, error) {
	file, err := f.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	d, ok := file.(*dir)
	if !ok {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: errors.New("not a directory")}
	}
	entries, err := d.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	sortDirEntries(entries)
	return entries, nil
}

// fileInfo implements fs.FileInfo and fs.DirEntry.
type fileInfo struct{ e *entry }

func (i fileInfo) Name() string {
	if i.e == nil {
		return "."
	}
	return i.e.name
}
func (i fileInfo) Size() int64 {
	if i.e == nil || i.e.isDir() {
		return 0
	}
	return int64(i.e.size)
}
func (i fileInfo) Mode() fs.FileMode {
	if i.e == nil || i.e.isDir() {
		return fs.ModeDir | 0o555
	}
	if i.e.attr&attrReadOnly != 0 {
		return 0o444
	}
	return 0o644
}
func (i fileInfo) ModTime() time.Time {
	if i.e == nil {
		return time.Time{}
	}
	return i.e.modified
}
func (i fileInfo) IsDir() bool                { return i.e == nil || i.e.isDir() }
func (i fileInfo) Sys() any                   { return nil }
func (i fileInfo) Type() fs.FileMode          { return i.Mode().Type() }
func (i fileInfo) Info() (fs.FileInfo, error) { return i, nil }

// file is an open regular file.
type file struct {
	fsys     *FS
	e        *entry
	clusters []uint32
	off      int64
}

func (fl *file) Stat() (fs.FileInfo, error) { return fileInfo{fl.e}, nil }
func (fl *file) Close() error               { return nil }

func (fl *file) Read(p []byte) (int, error) {
	n, err := fl.ReadAt(p, fl.off)
	fl.off += int64(n)
	return n, err
}

// ReadAt reads the file's content through its cluster chain.
func (fl *file) ReadAt(p []byte, off int64) (int, error) {
	size := int64(fl.e.size)
	if off >= size {
		return 0, io.EOF
	}
	want := len(p)
	if int64(want) > size-off {
		p = p[:size-off]
	}
	cs := fl.fsys.clusterSize
	n := 0
	for n < len(p) {
		pos := off + int64(n)
		c := fl.clusters[pos/cs]
		chunk := min(int64(len(p)-n), cs-pos%cs)
		if _, err := fl.fsys.r.ReadAt(p[n:n+int(chunk)], fl.fsys.clusterOffset(c)+pos%cs); err != nil {
			return n, err
		}
		n += int(chunk)
	}
	if n < want {
		return n, io.EOF
	}
	return n, nil
}

// dir is an open directory.
type dir struct {
	fsys    *FS
	e       *entry // nil: the root
	name    string
	entries []fs.DirEntry
	read    bool
}

func (d *dir) Stat() (fs.FileInfo, error) { return fileInfo{d.e}, nil }
func (d *dir) Close() error               { return nil }
func (d *dir) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: d.name, Err: errors.New("is a directory")}
}

// ReadDir implements fs.ReadDirFile.
func (d *dir) ReadDir(n int) ([]fs.DirEntry, error) {
	if !d.read {
		entries, err := d.fsys.readDir(d.e)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			d.entries = append(d.entries, fileInfo{e})
		}
		d.read = true
	}
	if n <= 0 {
		out := d.entries
		d.entries = nil
		return out, nil
	}
	if len(d.entries) == 0 {
		return nil, io.EOF
	}
	n = min(n, len(d.entries))
	out := d.entries[:n]
	d.entries = d.entries[n:]
	return out, nil
}

func sortDirEntries(entries []fs.DirEntry) {
	slices.SortFunc(entries, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
}

var _ fs.ReadDirFS = (*FS)(nil)
