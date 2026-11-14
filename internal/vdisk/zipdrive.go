package vdisk

import (
	"archive/zip"
	"fmt"
	"strings"
)

// DOS attributes.
const (
	attrReadOnly  = 0x01
	attrVolume    = 0x08
	attrDirectory = 0x10
	attrArchive   = 0x20
	attrLongName  = 0x0F
	attrLFNMask   = 0x3F
	attrPending   = 0x80 // DOS_ATTR_PENDING_SHORT_NAME
)

// zipEntry is a file or directory as DOSBox Pure's zipDrive presents it
// (src/dos/drive_zip.cpp, Zip_Entry/Zip_File/Zip_Directory).
type zipEntry struct {
	name       string // DOS 8.3 name within its directory
	attr       byte
	date, time uint16
	long       string // long name: the original path component, if the DOS name differs
	haslfn     bool
	size       uint32
	file       *zip.File          // files only
	entries    hashMap[*zipEntry] // directories only
}

// zipDrive is the content archive's DOS view.
type zipDrive struct {
	root        *zipEntry
	directories map[uint32]*zipEntry // fnvHash(full DOS path) -> directory, compared by hash like the original
}

// dosPathMax is DOS_PATHLENGTH: zipDriveImpl stops building a path there.
const dosPathMax = 80

// newZipDrive ports zipDriveImpl's constructor: every central directory
// entry, in order, is placed into the tree under DOS 8.3 names. It does
// not support parent archives (.parent markers), which DOSBox Pure layers
// underneath with a union drive.
func newZipDrive(zr *zip.Reader) (*zipDrive, error) {
	d := &zipDrive{
		root:        &zipEntry{attr: attrVolume | attrDirectory},
		directories: map[uint32]*zipEntry{},
	}
	lfnDirs := map[uint32]*zipEntry{}

	for _, f := range zr.File {
		if f.Method != zip.Store && f.Method != zip.Deflate {
			return nil, fmt.Errorf("%s: compression method %d is not supported here", f.Name, f.Method)
		}
		if f.Flags&(1|32) != 0 {
			continue // encrypted or patch data: zipDrive skips these
		}
		if (f.Method == zip.Store && f.UncompressedSize64 != f.CompressedSize64) ||
			(f.UncompressedSize64 != 0 && f.CompressedSize64 == 0) ||
			f.UncompressedSize64 > 0xFFFFFFFF {
			continue
		}

		name := f.Name
		isDir := strings.HasSuffix(name, "/") || strings.HasSuffix(name, `\`) || f.ExternalAttrs&0x10 != 0
		date, tm := f.ModifiedDate, f.ModifiedTime //nolint:staticcheck // the raw DOS fields are exactly what zipDrive reads

		parent := d.root
		dosPath := ""  // full DOS path so far, components joined with '\'
		compStart := 0 // start of the current component in name
		for n := 0; n <= len(name) && len(dosPath) < dosPathMax; n++ {
			if n != len(name) && name[n] != '/' && name[n] != '\\' {
				continue
			}
			if n == compStart {
				compStart++
				continue
			}
			comp := name[compStart:n]
			if compStart == 0 && len(comp) > 7 && f.UncompressedSize64 == 0 && strings.EqualFold(comp[len(comp)-7:], ".parent") {
				return nil, fmt.Errorf("%s: parent archives are not supported here", name)
			}

			dos, changed := make8dot3(comp)
			last := n == len(name)

			if last && !isDir {
				for {
					if _, taken := parent.entries.get(fnvHash(dos)); !taken {
						break
					}
					var ok bool
					if dos, ok = incrementName(dos); !ok {
						goto skip
					}
				}
				parent.entries.put(fnvHash(dos), &zipEntry{
					name: dos, attr: attrArchive, date: date, time: tm,
					long: comp, haslfn: changed, size: uint32(f.UncompressedSize64), file: f,
				})
				break
			}

			{
				full := joinDOS(dosPath, dos)
				var zdir *zipEntry
				lfnHash := uint32(0)
				if changed {
					lfnHash = fnvHash(name[:n])
					zdir = lfnDirs[lfnHash]
				} else {
					zdir = d.directories[fnvHash(full)]
				}
				if zdir == nil {
					for {
						if _, taken := parent.entries.get(fnvHash(dos)); !taken {
							break
						}
						var ok bool
						if dos, ok = incrementName(dos); !ok {
							goto skip
						}
					}
					full = joinDOS(dosPath, dos)
					zdir = &zipEntry{name: dos, attr: attrDirectory, date: date, time: tm, long: comp, haslfn: changed}
					parent.entries.put(fnvHash(dos), zdir)
					d.directories[fnvHash(full)] = zdir
					if changed {
						lfnDirs[lfnHash] = zdir
					}
				} else if n+1 >= len(name) {
					zdir.date, zdir.time = date, tm // the directory's own entry
				}
				if n+1 >= len(name) {
					break
				}
				parent = zdir
				// The original extends its path buffer with the name computed in
				// this iteration (p_dos += dos_len), not zdir's stored name, which
				// differs if an earlier collision incremented it; later
				// directories.Get lookups depend on exactly this path.
				dosPath = joinDOS(dosPath, dos)
				compStart = n + 1
			}
		}
	skip:
	}
	return d, nil
}

func joinDOS(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + `\` + name
}

// lookup returns the entry at a DOS path ("" is the root).
func (d *zipDrive) lookup(path string) *zipEntry {
	if path == "" {
		return d.root
	}
	i := strings.LastIndexByte(path, '\\')
	if i < 0 {
		e, _ := d.root.entries.get(fnvHash(path))
		return e
	}
	dir := d.directories[fnvHash(path[:i])]
	if dir == nil {
		return nil
	}
	e, _ := dir.entries.get(fnvHash(path[i+1:]))
	return e
}

// longName is zipDrive::GetLongFileName: the original name, when it
// differs from the DOS name.
func (e *zipEntry) longName() (string, bool) {
	if !e.haslfn || e.long == e.name || len(e.long) > 255 {
		return "", false
	}
	return e.long, true
}

// dosValidChar is DOS_ValidCharBits (src/dos/dos_files.cpp).
var dosValidChar = [32]byte{0, 0, 0, 0, 250, 43, 255, 3, 255, 255, 255, 199, 1, 0, 0, 232, 1, 192, 5, 254, 224, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255}

func validChar(c byte) bool { return dosValidChar[c/8]&(1<<(c%8)) != 0 }

// dosToUpper is DOS_ToUpper: ASCII plus code page 437's accented letters.
func dosToUpper(c byte) byte {
	t := [0x25]byte{
		0x00, 0x9a, 0x45, 0x41, 0x8E, 0x41, 0x8F, 0x80, 0x45, 0x45, 0x45, 0x49, 0x49, 0x49, 0x00, 0x00,
		0x00, 0x92, 0x00, 0x4F, 0x99, 0x4F, 0x55, 0x55, 0x59, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x41, 0x49, 0x4F, 0x55, 0xA5}
	switch {
	case c > 0x60 && c < 0x7B:
		return c - 0x20
	case c > 0x7F && c < 0xA5 && t[c-0x80] != 0:
		return t[c-0x80]
	}
	return c
}

// toUpperAndFilter is DOS_ToUpperAndFilter.
func toUpperAndFilter(c byte) byte {
	switch {
	case validChar(c):
		return c
	case c >= 'a' && c <= 'z':
		return c & 0x5F
	case c < 0x80:
		return '-'
	}
	return dosToUpper(c)
}

// make8dot3 is DBP_Make8dot3FileName (src/dos/drives.cpp): a valid 8.3
// name is kept; otherwise the first and last 4 characters of the base and
// the first 3 of the extension are kept, uppercased and filtered.
func make8dot3(s string) (string, bool) {
	dot := -1
	for i := len(s) - 1; ; i-- {
		if s[i] == '.' {
			dot = i
			break
		}
		if i <= 0 {
			break
		}
	}
	baseLen, extLen := len(s), 0
	if dot >= 0 {
		baseLen, extLen = dot, len(s)-1-dot
	}
	if baseLen <= 8 && extLen <= 3 {
		valid := true
		for i := 0; i < len(s); i++ {
			if !validChar(s[i]) && i != dot {
				valid = false
				break
			}
		}
		if valid {
			return s, false
		}
	}
	left, right := baseLen, 0
	if baseLen > 8 {
		left, right = 4, 4
	}
	var b []byte
	for i := 0; i < left; i++ {
		b = append(b, toUpperAndFilter(s[i]))
	}
	for i := baseLen - right; i < baseLen; i++ {
		b = append(b, toUpperAndFilter(s[i]))
	}
	if baseLen == 0 {
		b = append(b, '-') // DBP_8DOT3_INVALID_CHAR
	}
	if extLen > 0 {
		b = append(b, '.')
		for i := dot + 1; i < dot+1+min(extLen, 3); i++ {
			b = append(b, toUpperAndFilter(s[i]))
		}
	}
	return string(b), true
}

// incrementName is Zip_Directory::IncrementName: bump a character in the
// middle of the base name to resolve a collision.
func incrementName(s string) (string, bool) {
	b := []byte(s)
	baseLen := len(b)
	if i := strings.IndexByte(s, '.'); i >= 0 {
		baseLen = i
	}
	j := baseLen / 2
	if baseLen > 8 {
		j = 4
	}
	at := func(i int) bool { return i < len(b) && b[i] != 0 && b[i] < '~' }
	switch {
	case baseLen >= 1 && at(j):
		b[j]++
	case baseLen >= 3 && at(j+1):
		b[j+1]++
	case baseLen >= 5 && at(j+2):
		b[j+2]++
	default:
		return s, false
	}
	return string(b), true
}

// dtaResult is one FindFirst/FindNext result.
type dtaResult struct {
	name       string
	size       uint32
	date, time uint16
	attr       byte
}

// find lists a directory as the content's union drive returns it for a
// "*.*" search with every attribute (unionDrive::FindFirst/FindNext over
// zipDrive, with no save overlay): the volume label in the root, "." and
// ".." elsewhere, then the archive's entries in hash map slot order.
func (d *zipDrive) find(dirPath, label string) []dtaResult {
	dir := d.lookup(dirPath)
	if dir == nil || dir.attr&attrDirectory == 0 {
		return nil // unionDrive::FindFirst fails TestDir: no entries at all
	}
	var out []dtaResult
	if dirPath == "" {
		if label != "" {
			out = append(out, dtaResult{name: label, attr: attrVolume})
		}
	} else {
		for _, dot := range []string{".", ".."} {
			out = append(out, dtaResult{name: dot, date: dir.date, time: dir.time, attr: dir.attr})
		}
	}
	for _, e := range dir.entries.inOrder() {
		out = append(out, dtaResult{name: e.name, size: e.size, date: e.date, time: e.time, attr: e.attr})
	}
	return out
}

// walk is DriveFileIterator: each directory's entries, the root first,
// then subdirectories last-found-first (it pops from a stack).
func (d *zipDrive) walk(fn func(path string, r dtaResult)) {
	stack := []string{""}
	for len(stack) > 0 {
		dir := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, r := range d.find(dir, "") {
			if r.attr&attrVolume != 0 || r.name == "." || r.name == ".." {
				continue
			}
			p := joinDOS(dir, r.name)
			if r.attr&attrDirectory != 0 {
				stack = append(stack, p)
			}
			fn(p, r)
		}
	}
}
