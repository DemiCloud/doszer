package dump

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/DemiCloud/doszer/internal/fat"
)

// IsDiskImage reports whether p names a floppy or hard disk image by its
// extension: .img, .ima or .vfd (a raw FAT disk, partitioned or not).
func IsDiskImage(p string) bool {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".img", ".ima", ".vfd":
		return true
	}
	return false
}

// ImportDisk copies a FAT disk image (a floppy, or a hard disk) into dir
// as it is, as Import keeps a disc: DOSBox Pure mounts the first floppy
// image in the content as A: (dosbox_pure_libretro.cpp, init_dosbox:
// "Mount the first found cdrom image as well as the first found
// floppy"), so an installer runs from A: as from its disk. A .vfd is
// copied as .img, which DOSBox Pure takes; DOSBox Pure checks a floppy
// image's size against its geometry list (init_dosbox_parse_drives), so
// one of another size is refused here. It returns the file written.
func ImportDisk(image, dir string) (string, error) {
	f, err := os.Open(image)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := fat.Open(f); err != nil {
		return "", fmt.Errorf("%s: %w", image, err)
	}
	fi, err := f.Stat()
	if err != nil {
		return "", err
	}
	if fi.Size() < 2949120 && !floppySize(fi.Size()) {
		return "", fmt.Errorf("%s: %d bytes is no floppy size DOSBox Pure mounts", image, fi.Size())
	}
	name := filepath.Base(image)
	if strings.EqualFold(filepath.Ext(name), ".vfd") {
		name = strings.TrimSuffix(name, filepath.Ext(name)) + ".img"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	dst := filepath.Join(dir, name)
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	if err := writeNew(dst, func(w io.Writer) error { _, err := io.Copy(w, f); return err }); err != nil {
		return "", err
	}
	os.Chtimes(dst, fi.ModTime(), fi.ModTime())
	return dst, nil
}

// floppySize reports whether size is a floppy DOSBox Pure mounts: a size
// in its DiskGeometryList (src/ints/bios_disk.cpp), or that plus 1 KB.
func floppySize(size int64) bool {
	for _, kb := range []int64{160, 180, 200, 320, 360, 400, 720, 1200, 1440, 1520, 1680, 1720, 1840, 2880} {
		if size == kb*1024 || size == kb*1024+1024 {
			return true
		}
	}
	return false
}
