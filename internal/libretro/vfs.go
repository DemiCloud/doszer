package libretro

import (
	"os"
	"path/filepath"
	"unsafe"

	"github.com/ebitengine/purego"
)

// The VFS interface (libretro.h, struct retro_vfs_interface, version 3).
// DOSBox Pure uses it only to list directories - finding OS images and
// shells in the system directory (DBP_ScanSystem) and matching paths case
// insensitively - and to remove files; it opens files natively unless a
// path is a scheme URI (VFSIO::Use). So only those calls do anything:
// every file call fails, as for a file that can't be opened.
var vfsTable [19]uintptr

// RETRO_VFS_STAT_* flags.
const (
	vfsStatValid = 1 << 0
	vfsStatDir   = 1 << 1
)

const fail = ^uintptr(0) // -1, for int and int64 results alike

type vfsDir struct {
	path    string
	entries []os.DirEntry
	pos     int
	name    []byte
}

type vfsState struct {
	dirs map[uintptr]*vfsDir
	next uintptr
}

func registerVFS() {
	failing := purego.NewCallback(func() uintptr { return fail })
	null := purego.NewCallback(func() uintptr { return 0 })
	vfsTable = [19]uintptr{
		null,    // get_path
		null,    // open
		failing, // close
		failing, // size
		failing, // tell
		failing, // seek
		failing, // read
		failing, // write
		failing, // flush
		purego.NewCallback(vfsRemove),
		purego.NewCallback(vfsRename),
		failing, // truncate
		purego.NewCallback(vfsStat),
		purego.NewCallback(vfsMkdir),
		purego.NewCallback(vfsOpendir),
		purego.NewCallback(vfsReaddir),
		purego.NewCallback(vfsDirentName),
		purego.NewCallback(vfsDirentIsDir),
		purego.NewCallback(vfsClosedir),
	}
}

func vfsRemove(path *byte) uintptr {
	if os.Remove(goString(path)) != nil {
		return fail
	}
	return 0
}

func vfsRename(from, to *byte) uintptr {
	if os.Rename(goString(from), goString(to)) != nil {
		return fail
	}
	return 0
}

func vfsStat(path *byte, size *int32) uintptr {
	fi, err := os.Stat(goString(path))
	if err != nil {
		return 0
	}
	if size != nil {
		*size = int32(fi.Size())
	}
	if fi.IsDir() {
		return vfsStatValid | vfsStatDir
	}
	return vfsStatValid
}

func vfsMkdir(path *byte) uintptr {
	p := goString(path)
	if fi, err := os.Stat(p); err == nil && fi.IsDir() {
		return ^uintptr(1) // -2: already exists
	}
	if os.MkdirAll(p, 0o755) != nil {
		return fail
	}
	return 0
}

func vfsOpendir(path *byte, includeHidden bool) uintptr {
	p := goString(path)
	entries, err := os.ReadDir(p)
	if err != nil {
		return 0
	}
	s := &h.vfs
	if s.dirs == nil {
		s.dirs = map[uintptr]*vfsDir{}
	}
	s.next++
	s.dirs[s.next] = &vfsDir{path: p, entries: entries, pos: -1}
	return s.next
}

func vfsReaddir(d uintptr) uintptr {
	dir := h.vfs.dirs[d]
	if dir == nil || dir.pos+1 >= len(dir.entries) {
		return 0
	}
	dir.pos++
	dir.name = cString(dir.entries[dir.pos].Name())
	return 1
}

func vfsDirentName(d uintptr) unsafe.Pointer {
	dir := h.vfs.dirs[d]
	if dir == nil || dir.name == nil {
		return nil
	}
	return unsafe.Pointer(&dir.name[0])
}

func vfsDirentIsDir(d uintptr) uintptr {
	dir := h.vfs.dirs[d]
	if dir == nil || dir.pos < 0 {
		return 0
	}
	e := dir.entries[dir.pos]
	if e.Type()&os.ModeSymlink != 0 {
		fi, err := os.Stat(filepath.Join(dir.path, e.Name()))
		return boolResult(err == nil && fi.IsDir())
	}
	return boolResult(e.IsDir())
}

func vfsClosedir(d uintptr) uintptr {
	delete(h.vfs.dirs, d)
	return 0
}
