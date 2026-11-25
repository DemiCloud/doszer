package fat

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf16"
)

// Layout is a FAT volume's geometry, as its boot sector gives it.
type Layout struct {
	Kind              Kind
	Base              int64 // byte offset of the volume on its disk
	BytesPerSector    int
	SectorsPerCluster int
	ReservedSectors   int
	NumFATs           int
	FATSectors        int64 // sectors per FAT
	RootEntries       int   // FAT12/16's fixed root directory
	TotalSectors      int64
	ClusterCount      uint32
	RootCluster       uint32 // FAT32
	FSInfoSector      int    // FAT32, in sectors from Base
	BackupBootSector  int    // FAT32, in sectors from Base; 0: none
	FAT0, FAT1        uint32 // the first two FAT entries: media descriptor, flags
}

// Layout returns the volume's geometry.
func (f *FS) Layout() Layout { return f.layout }

func (l Layout) clusterSize() int64 { return int64(l.BytesPerSector) * int64(l.SectorsPerCluster) }

func (l Layout) rootDirSectors() int64 {
	return (int64(l.RootEntries)*32 + int64(l.BytesPerSector) - 1) / int64(l.BytesPerSector)
}

// clusterOffset is the byte offset of cluster c on the disk.
func (l Layout) clusterOffset(c uint32) int64 {
	first := int64(l.ReservedSectors) + int64(l.NumFATs)*l.FATSectors + l.rootDirSectors()
	return l.Base + (first+int64(c-2)*int64(l.SectorsPerCluster))*int64(l.BytesPerSector)
}

// Node is one directory entry of a file system tree as stored, for
// rebuilding the file system with Write.
type Node struct {
	// Short is the short (8.3) entry as stored: name, attributes, case
	// flags and times. Write replaces its cluster and size fields.
	Short [32]byte
	// Long is the long name as stored, nil if there is none.
	Long     []uint16
	Children []*Node // a directory's entries, in directory order
	Size     int64   // a file's size
	// Open returns a file's content, Size bytes.
	Open func() (io.Reader, error)
}

// IsDir reports a directory.
func (n *Node) IsDir() bool { return n.Short[11]&attrDirectory != 0 && !n.IsLabel() }

// IsLabel reports the volume label entry.
func (n *Node) IsLabel() bool { return n.Short[11]&attrVolumeID != 0 }

// ShortName returns the 8.3 name.
func (n *Node) ShortName() string { return shortName(n.Short[:]) }

// Name returns the long name if there is one, else the short name.
func (n *Node) Name() string {
	if n.Long != nil {
		return string(utf16.Decode(n.Long))
	}
	return n.ShortName()
}

// Is reports whether name names n, matching its long or short name
// case-insensitively.
func (n *Node) Is(name string) bool {
	return !n.IsLabel() && (strings.EqualFold(n.Name(), name) || strings.EqualFold(n.ShortName(), name))
}

// Child returns the entry of directory n that name names, or nil.
func (n *Node) Child(name string) *Node {
	for _, c := range n.Children {
		if c.Is(name) {
			return c
		}
	}
	return nil
}

// Tree reads the whole file system: a root node, with no short entry of
// its own, and every entry under it in directory order, the volume label
// included.
func (f *FS) Tree() (*Node, error) {
	root := &Node{}
	root.Short[11] = attrDirectory
	seen := map[uint32]bool{0: true}
	if f.kind == FAT32 {
		seen[f.rootCluster] = true
	}
	return root, f.tree(nil, root, "", seen)
}

func (f *FS) tree(dir *entry, n *Node, path string, seen map[uint32]bool) error {
	entries, err := f.readDir(dir)
	if err != nil {
		return fmt.Errorf("%s: %w", strings.TrimPrefix(path, "/"), err)
	}
	for _, e := range entries {
		c := &Node{Short: e.raw, Long: e.long}
		n.Children = append(n.Children, c)
		switch {
		case e.isLabel():
		case e.isDir():
			if seen[e.cluster] {
				return fmt.Errorf("%s/%s: directory loops back to cluster %d (a damaged file system)", path, e.name, e.cluster)
			}
			seen[e.cluster] = true
			if err := f.tree(e, c, path+"/"+e.name, seen); err != nil {
				return err
			}
			delete(seen, e.cluster)
		default:
			c.Size = int64(e.size)
			clusters, err := f.chain(e.cluster)
			if err != nil && e.size > 0 {
				return fmt.Errorf("%s/%s: %w", path, e.name, err)
			}
			if int64(len(clusters))*f.clusterSize < int64(e.size) {
				return fmt.Errorf("%s/%s: cluster chain shorter than file size %d", path, e.name, e.size)
			}
			fl := &file{fsys: f, e: e, clusters: clusters}
			c.Open = func() (io.Reader, error) { return io.NewSectionReader(fl, 0, int64(fl.e.size)), nil }
		}
	}
	return nil
}

// NewFile returns a file node named name in dir, its short name generated
// as Windows 9x does when name isn't a valid 8.3 name (a basis name and
// "~N", unique in dir) and a long name kept alongside it then.
func NewFile(dir *Node, name string, size int64, modified time.Time, open func() (io.Reader, error)) (*Node, error) {
	n := &Node{Size: size, Open: open}
	short, long, err := shortFor(dir, name)
	if err != nil {
		return nil, err
	}
	copy(n.Short[0:11], short)
	n.Long = long
	n.Short[11] = 0x20 // archive
	d, t := toDOS(modified)
	for _, off := range [][2]int{{14, 16}, {22, 24}} { // created, written
		putLE16(n.Short[off[0]:], t)
		putLE16(n.Short[off[1]:], d)
	}
	putLE16(n.Short[18:], d) // accessed
	return n, nil
}

// SetModified sets the entry's last-written date and time.
func (n *Node) SetModified(t time.Time) {
	d, tm := toDOS(t)
	putLE16(n.Short[22:], tm)
	putLE16(n.Short[24:], d)
}

// NewDir returns an empty directory node named name in dir.
func NewDir(dir *Node, name string, modified time.Time) (*Node, error) {
	n, err := NewFile(dir, name, 0, modified, nil)
	if err != nil {
		return nil, err
	}
	n.Short[11] = attrDirectory
	return n, nil
}

// shortFor returns the 11-byte short name for name in dir, and the long
// name to store with it (nil when the short name is name itself).
func shortFor(dir *Node, name string) ([]byte, []uint16, error) {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return nil, nil, fmt.Errorf("invalid file name %q", name)
	}
	taken := func(s []byte) bool {
		for _, c := range dir.Children {
			if !c.IsLabel() && string(c.Short[0:11]) == string(s) {
				return true
			}
		}
		return false
	}
	if s, ok := exact83(name); ok {
		if taken(s) {
			return nil, nil, fmt.Errorf("%s already exists", name)
		}
		return s, nil, nil
	}
	for _, c := range dir.Children {
		if c.Is(name) {
			return nil, nil, fmt.Errorf("%s already exists", name)
		}
	}
	long := utf16.Encode([]rune(name))
	if len(long) > 255 {
		return nil, nil, fmt.Errorf("%s: name longer than 255 characters", name)
	}
	base, ext := basis(name)
	for i := 1; i < 1000000; i++ {
		tail := fmt.Sprintf("~%d", i)
		b := base
		if len(b)+len(tail) > 8 {
			b = b[:8-len(tail)]
		}
		s := []byte(fmt.Sprintf("%-8s%-3s", b+tail, ext))
		if !taken(s) {
			return s, long, nil
		}
	}
	return nil, nil, errors.New("no free short name")
}

// exact83 returns name as an 11-byte short name if it is already a valid,
// upper-case 8.3 name.
func exact83(name string) ([]byte, bool) {
	base, ext, _ := strings.Cut(name, ".")
	if base == "" || len(base) > 8 || len(ext) > 3 || strings.Contains(ext, ".") {
		return nil, false
	}
	for _, r := range base + ext {
		if r >= 0x80 || r <= ' ' || strings.ContainsRune(`"*+,/:;<=>?[\]|.`, r) || (r >= 'a' && r <= 'z') {
			return nil, false
		}
	}
	return []byte(fmt.Sprintf("%-8s%-3s", base, ext)), true
}

// basis is Windows 9x's basis name: upper case, spaces and periods
// removed (but the last, which starts the extension), and characters not
// allowed in a short name replaced by "_".
func basis(name string) (base, ext string) {
	clean := func(s string, n int) string {
		var b strings.Builder
		for _, r := range strings.ToUpper(s) {
			switch {
			case r == ' ' || r == '.':
				continue
			case r >= 0x80 || r < ' ' || strings.ContainsRune(`"*+,/:;<=>?[\]|`, r):
				r = '_'
			}
			if b.Len() < n {
				b.WriteRune(r)
			}
		}
		return b.String()
	}
	name = strings.TrimLeft(name, ".")
	b, e := name, ""
	if i := strings.LastIndexByte(name, '.'); i > 0 {
		b, e = name[:i], name[i+1:]
	}
	base, ext = clean(b, 6), clean(e, 3)
	if base == "" {
		base = "_"
	}
	return base, ext
}

func toDOS(t time.Time) (date, tm uint16) {
	if t.Year() < 1980 {
		t = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	if t.Year() > 2107 {
		t = time.Date(2107, 12, 31, 23, 59, 58, 0, time.UTC)
	}
	return uint16(t.Day() | int(t.Month())<<5 | (t.Year()-1980)<<9), uint16(t.Second()/2 | t.Minute()<<5 | t.Hour()<<11)
}

func putLE16(b []byte, v uint16) { b[0], b[1] = byte(v), byte(v>>8) }
