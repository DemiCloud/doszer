// Package vdisk reconstructs the D: drive DOSBox Pure presents to an
// installed operating system (the OS image model, README "Install"): a FAT
// disk it synthesizes from the content archive at boot, with the guest's
// changes stored separately in a `<content>-<HASH>.sav` sector diff.
//
// Reading those changes requires rebuilding the synthesized disk exactly,
// so this package ports, byte for byte, the DOSBox Pure 1.0 Preview 8 code
// that builds it:
//
//   - src/dos/drive_zip.cpp, zipDriveImpl: the archive's DOS view (8.3
//     names, collision handling, directory order).
//   - src/dos/drives.h, BaseHashMap: the hash map whose slot order is that
//     directory order.
//   - src/ints/bios_disk.cpp, fatFromDOSDrive: the FAT layout, long name
//     entries, short name generation, MBR and boot sector.
//   - src/ints/bios_disk.cpp, differencingDisk: the .sav (FFDD) format.
//   - dosbox_pure_libretro.cpp, DBP_GetSaveFile: the D: file filter and the
//     hash in the .sav name, which doubles as the disk's serial number.
//
// It assumes no `<content>.pure.zip` save exists for the content (DOSBox
// Pure would layer it over the archive) and no parent archives.
package vdisk

import (
	"archive/zip"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
)

const (
	sectorSize      = 512
	headCount       = 240
	sectorsPerTrack = 63
	sectBoot        = 32
	fat32RootClust  = 2
	direntSize      = 32
)

// DefaultFreeSpaceMB is dosbox_pure_bootos_dfreespace's default.
const DefaultFreeSpaceMB = 1024

// Disk is the reconstructed D: disk, optionally with a .sav diff applied.
// It implements io.ReaderAt over the whole disk (MBR included); pass it to
// fat.Open to read its files.
type Disk struct {
	Hash uint32 // the save hash: the .sav name's <HASH> and the disk serial

	mbr, boot, fsinfo []byte
	fat               []byte
	root, dirs        []byte // directory entries, 32 bytes each
	files             []vfile
	fileAtSector      []int
	readOnly          bool

	sectFat1, sectFat2, sectRoot, sectDirs, sectFiles, sectFilesEnd, sectEnd uint32

	mu     sync.Mutex
	open   map[int]*openFile
	diff   map[uint32]int64 // sector -> offset of its data in the .sav
	diffRA io.ReaderAt
}

type vfile struct {
	zf        *zip.File
	firstSect uint32
}

type openFile struct {
	rc  io.ReadCloser
	pos int64
}

// SaveName returns the .sav file name DOSBox Pure uses for this disk.
func (d *Disk) SaveName(contentName string) string {
	if d.Hash == 0x11111111 {
		return contentName + ".sav"
	}
	return fmt.Sprintf("%s-%08X.sav", contentName, d.Hash)
}

// Size returns the disk's size in bytes.
func (d *Disk) Size() int64 { return int64(d.sectEnd) * sectorSize }

// Build reconstructs the D: disk for the content archive at contentPath
// (its file name gives the volume label), with freeSpaceMB as
// dosbox_pure_bootos_dfreespace.
func Build(zr *zip.Reader, contentPath string, freeSpaceMB int) (*Disk, error) {
	zd, err := newZipDrive(zr)
	if err != nil {
		return nil, err
	}
	filter, err := vdiskFilter(zd)
	if err != nil {
		return nil, err
	}

	d := &Disk{open: map[int]*openFile{}}
	d.Hash = saveHash(zd, filter, freeSpaceMB)

	// SumFileSize: every unfiltered file, counted in 32 KB clusters.
	var used uint64
	zd.walk(func(p string, r dtaResult) {
		if !filter[fnvHash(p)] {
			used += uint64((r.size + 32*1024 - 1) / (32 * 1024) * (32 * 1024))
		}
	})

	d.readOnly = freeSpaceMB == 0 // the union drive always reports free space
	addFreeMB := uint32(freeSpaceMB)
	if d.readOnly {
		addFreeMB = 0
	}
	totalMB := uint32(used / (1024 * 1024))
	if addFreeMB != 0 {
		totalMB += 1 + addFreeMB
	}

	b := &builder{d: d, zd: zd, filter: filter, label: volumeLabel(contentPath)}
	b.chooseFormat(totalMB)
	b.setFAT(0, 0xFFFFFF8)
	b.setFAT(1, 0xFFFFFFF)
	if b.fatSz != 32 {
		d.root = make([]byte, 512*direntSize)
	}
	b.parseDir("", 0)
	if err := b.finish(addFreeMB); err != nil {
		return nil, err
	}
	return d, nil
}

// volumeLabel is DBP_SetDriveLabelFromContentPath with
// forceAppendExtension, as RunBoot labels the D: drive: the file name cut
// at its extension, a '(' or '[', or trailing space, to at most 7
// characters, then '-' and up to 3 characters of the extension,
// uppercased by DOS_Drive's label handling.
func volumeLabel(contentPath string) string {
	file := path.Base(strings.ReplaceAll(contentPath, `\`, "/"))
	ext := ""
	if i := strings.LastIndexByte(file, '.'); i >= 0 {
		ext = file[i+1:]
		file = file[:i]
	}
	lbl := []byte(file)
	if len(lbl) > 11 {
		lbl = lbl[:11]
	}
	end := len(lbl)
	for c := end; c > 0; c-- {
		if c == end || lbl[c] == '(' || lbl[c] == '[' || (lbl[c] <= ' ' && c+1 >= end) {
			end = c
		}
	}
	lbl = lbl[:end]
	if ext != "" {
		if len(lbl) > 7 {
			lbl = lbl[:7]
		}
		lbl = append(lbl, '-')
		lbl = append(lbl, ext[:min(len(ext), 11-len(lbl))]...)
	}
	return setLabel(string(lbl))
}

// setLabel is Set_Label (src/dos/drives.cpp): the label in DOS 8.3 form,
// uppercased, with a '.' inserted after 8 characters when it has none.
func setLabel(in string) string {
	var out []byte
	togo, point, i := 8, false, 0
	for togo > 0 && i < len(in) {
		if !point && in[i] == '.' {
			togo, point = 4, true
		}
		out = append(out, byte(strings.ToUpper(string(in[i]))[0]))
		i++
		togo--
		if togo == 0 && !point {
			if i < len(in) && in[i] == '.' {
				i++
			}
			out = append(out, '.')
			point, togo = true, 3
		}
	}
	if len(out) > 0 && out[len(out)-1] == '.' {
		out = out[:len(out)-1]
	}
	return string(out)
}

// vdiskFilter is dbp_vdisk_filter: the runtime files DOSBox Pure hides
// from D:, and every mountable disc or disk image found in the content
// (init_dosbox_parse_drives), less those sharing a CUE sheet's name.
func vdiskFilter(zd *zipDrive) (map[uint32]bool, error) {
	filter := map[uint32]bool{}
	for _, n := range []string{"AUTOBOOT.DBP", "PADMAP.DBP", "FRONTEND.DBP", "DOSBOX~1.CON", "DOSBOX.CON"} {
		filter[fnvHash(n)] = true
	}

	var images []string
	var errs []error
	zd.walk(func(p string, r dtaResult) {
		if r.attr&attrDirectory != 0 {
			return
		}
		fname := p[strings.LastIndexByte(p, '\\')+1:]
		i := strings.LastIndexByte(fname, '.')
		if i < 0 {
			return
		}
		switch fname[i+1:] {
		case "ISO", "CHD", "CUE":
			images = append(images, p)
		case "INS":
			if r.size < 16384 && hasPrefix(zd.lookup(p), `FILE "`) {
				images = append(images, p)
			}
		case "IMG", "IMA", "VHD", "JRC":
			switch {
			case r.size < 163840 || (r.size%512 != 0 && r.size%2352 != 0):
			case r.size < 2949120:
				for _, k := range []uint32{160, 180, 200, 320, 360, 400, 720, 1200, 1440, 1520, 1680, 1720, 1840, 2880} {
					if k*1024 == r.size || k*1024+1024 == r.size {
						images = append(images, p)
						break
					}
				}
			default:
				// DOSBox Pure probes the image's partition table or ISO
				// signature (imageDisk::Set_GeometryForHardDisk,
				// DBP_IsDiskCDISO); not reproduced here.
				errs = append(errs, fmt.Errorf("%s: can't tell whether DOSBox Pure treats this disk image as mountable", p))
			}
		}
	})
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	// "Filter image files that have the same name as a cue file".
	keep := make([]bool, len(images))
	for i := range keep {
		keep[i] = true
	}
	for i, img := range images {
		if !strings.HasSuffix(img, ".CUE") && !strings.HasSuffix(img, ".INS") {
			continue
		}
		prefix := img[:len(img)-3]
		for j, other := range images {
			if i != j && strings.HasPrefix(other, prefix) {
				keep[j] = false
			}
		}
	}
	for i, img := range images {
		if keep[i] {
			filter[fnvHash(img)] = true
		}
	}
	return filter, nil
}

func hasPrefix(e *zipEntry, prefix string) bool {
	if e == nil || e.file == nil {
		return false
	}
	rc, err := e.file.Open()
	if err != nil {
		return false
	}
	defer rc.Close()
	b := make([]byte, len(prefix))
	_, err = io.ReadFull(rc, b)
	return err == nil && string(b) == prefix
}

// saveHash is DBP_GetSaveFile(SFT_VIRTUALDISK)'s hash: a CRC32 chained
// over every unfiltered path and its size, date, time and attributes.
func saveHash(zd *zipDrive, filter map[uint32]bool, freeSpaceMB int) uint32 {
	h := uint32(0x11111111-1024) + uint32(freeSpaceMB)
	zd.walk(func(p string, r dtaResult) {
		if filter[fnvHash(p)] {
			return
		}
		if len(p) > 4 && strings.HasSuffix(p, ".SKC") {
			filter[fnvHash(p)] = true // ZIP seek caches are hidden too
			return
		}
		h = crc32.Update(h, crc32.IEEETable, []byte(p))
		h = crc32.Update(h, crc32.IEEETable, []byte{
			byte(r.size >> 24), byte(r.size >> 16), byte(r.size >> 8), byte(r.size),
			byte(r.date >> 8), byte(r.date), byte(r.time >> 8), byte(r.time), r.attr,
		})
	})
	return h
}

// builder holds fatFromDOSDrive's construction state.
type builder struct {
	d                 *Disk
	zd                *zipDrive
	filter            map[uint32]bool
	label             string
	fatSz             uint32
	sectorsPerCluster uint32
}

func (b *builder) chooseFormat(totalMB uint32) {
	switch {
	case totalMB >= 3072:
		b.fatSz, b.sectorsPerCluster = 32, 64
	case totalMB >= 2048:
		b.fatSz, b.sectorsPerCluster = 32, 32
	case totalMB >= 384:
		b.fatSz, b.sectorsPerCluster = 16, 64
	case totalMB >= 192:
		b.fatSz, b.sectorsPerCluster = 16, 32
	case totalMB >= 96:
		b.fatSz, b.sectorsPerCluster = 16, 16
	case totalMB >= 48:
		b.fatSz, b.sectorsPerCluster = 16, 8
	case totalMB >= 12:
		b.fatSz, b.sectorsPerCluster = 16, 4
	case totalMB >= 4:
		b.fatSz, b.sectorsPerCluster = 16, 1
	case totalMB >= 2:
		b.fatSz, b.sectorsPerCluster = 12, 4
	case totalMB >= 1:
		b.fatSz, b.sectorsPerCluster = 12, 2
	default:
		b.fatSz, b.sectorsPerCluster = 12, 1
	}
}

func (b *builder) entriesPerCluster() int {
	return int(b.sectorsPerCluster * sectorSize / direntSize)
}

// setFAT is Iter::SetFAT, growing the table as needed (FAT12 in steps of
// 3 sectors so entries stay aligned).
func (b *builder) setFAT(idx uint32, val uint32) {
	d := b.d
	for uint64(idx) >= uint64(len(d.fat))*8/uint64(b.fatSz) {
		add := sectorSize
		if b.fatSz == 12 {
			add = sectorSize * 3
		}
		d.fat = append(d.fat, make([]byte, add)...)
	}
	le := binary.LittleEndian
	switch {
	case b.fatSz == 32:
		le.PutUint32(d.fat[idx*4:], val)
	case b.fatSz == 16:
		le.PutUint16(d.fat[idx*2:], uint16(val))
	case idx&1 == 1:
		i := idx + idx/2
		le.PutUint16(d.fat[i:], le.Uint16(d.fat[i:])&0xF|uint16(val&0xFFF)<<4)
	default:
		i := idx + idx/2
		le.PutUint16(d.fat[i:], le.Uint16(d.fat[i:])&0xF000|uint16(val&0xFFF))
	}
}

// addDirEntry is Iter::AddDirEntry: the next entry slot in the current
// directory, growing the directory a cluster (or, for the FAT12/16 root,
// 512 entries) at a time. diridx indexes root or dirs as entries.
func (b *builder) addDirEntry(fat16Root bool, diridx *int) []byte {
	d := b.d
	epc := b.entriesPerCluster()
	switch {
	case !fat16Root && *diridx%epc == 0:
		if *diridx != 0 {
			b.setFAT(uint32(2+(*diridx-1)/epc), uint32(2+len(d.dirs)/direntSize/epc))
		}
		*diridx = len(d.dirs) / direntSize
		d.dirs = append(d.dirs, make([]byte, epc*direntSize)...)
		b.setFAT(uint32(2+*diridx/epc), 0x0FFFFFFF)
	case fat16Root && *diridx != 0 && *diridx%512 == 0:
		d.root = append(d.root, make([]byte, 512*direntSize)...)
	}
	i := *diridx
	*diridx++
	if fat16Root {
		return d.root[i*direntSize : (i+1)*direntSize]
	}
	return d.dirs[i*direntSize : (i+1)*direntSize]
}

// lfnSlots are the byte offsets of a long name entry's 13 UTF-16 units.
var lfnSlots = [13]int{1, 3, 5, 7, 9, 14, 16, 18, 20, 22, 24, 28, 30}

// parseDir is Iter::ParseDir.
func (b *builder) parseDir(dir string, parentFirstCluster uint16) {
	d := b.d
	fat16Root := dir == "" && b.fatSz != 32
	entries := func() []byte {
		if fat16Root {
			return d.root
		}
		return d.dirs
	}
	firstidx := 0
	if !fat16Root {
		firstidx = len(d.dirs) / direntSize
	}
	epc := b.entriesPerCluster()
	bytesPerCluster := b.sectorsPerCluster * sectorSize
	myFirstCluster := uint16(0)
	if dir != "" {
		myFirstCluster = uint16(2 + firstidx/epc)
	}

	label := ""
	if dir == "" {
		label = b.label
	}
	diridx := 0
	for _, r := range b.zd.find(dir, label) {
		dot, dotdot := r.name == ".", r.name == ".."
		if dir == "" && (dot || dotdot) {
			continue
		}
		fpath := joinDOS(dir, r.name)
		if b.filter[fnvHash(fpath)] {
			continue
		}

		var long string
		isLong := false
		if !dot && !dotdot && r.attr&attrVolume == 0 {
			if e := b.zd.lookup(fpath); e != nil {
				long, isLong = e.longName()
			}
		}
		if isLong {
			b.writeLFN(fat16Root, &diridx, long)
		}

		e := b.addDirEntry(fat16Root, &diridx)
		for i := 0; i < 11; i++ {
			e[i] = ' '
		}
		name, ext := r.name, ""
		if !dot && !dotdot {
			if i := strings.LastIndexByte(r.name, '.'); i >= 0 {
				name, ext = r.name[:i], r.name[i+1:]
			}
		}
		copy(e[0:11], name) // a name over 8 characters runs into the extension, as memcpy does
		copy(e[8:11], ext)

		attr := r.attr
		if d.readOnly {
			attr |= attrReadOnly
		}
		if isLong {
			attr |= attrPending
		}
		e[11] = attr
		le := binary.LittleEndian
		le.PutUint16(e[18:], r.date) // access date
		le.PutUint16(e[22:], r.time)
		le.PutUint16(e[24:], r.date)

		switch {
		case dot:
			e[11] |= attrDirectory
			le.PutUint16(e[26:], myFirstCluster)
		case dotdot:
			e[11] |= attrDirectory
			le.PutUint16(e[26:], parentFirstCluster)
		case r.attr&attrVolume != 0:
		case r.attr&attrDirectory == 0:
			le.PutUint32(e[28:], r.size)
			idx := len(d.files)
			d.files = append(d.files, vfile{zf: b.zd.lookup(fpath).file})
			numSects := (r.size + bytesPerCluster - 1) / bytesPerCluster * b.sectorsPerCluster
			for i := uint32(0); i < numSects; i++ {
				d.fileAtSector = append(d.fileAtSector, idx)
			}
		}
	}

	// Second pass: assign short names to long-named entries, then descend.
	// diridx is now one past this directory's last entry: addDirEntry
	// makes it an absolute index into dirs, or into root for FAT12/16.
	for ei := firstidx; ei < diridx; ei++ {
		e := entries()[ei*direntSize : (ei+1)*direntSize]
		var child string
		if e[11]&attrDirectory != 0 {
			if e[0] == '.' && (e[1] == ' ' || (e[1] == '.' && e[2] == ' ')) {
				continue
			}
			child = string(untilSpace(e[0:8]))
			if e[8] != ' ' {
				child += "." + string(untilSpace(e[8:11]))
			}
			child = joinDOS(dir, child)
		}
		if e[11]&attrPending != 0 {
			b.shortName(entries(), ei, firstidx, diridx)
			e = entries()[ei*direntSize : (ei+1)*direntSize]
		}
		if e[11]&attrDirectory != 0 {
			binary.LittleEndian.PutUint16(e[26:], uint16(2+len(d.dirs)/direntSize/epc))
			b.parseDir(child, myFirstCluster)
		}
	}
}

func untilSpace(b []byte) []byte {
	if i := strings.IndexByte(string(b), ' '); i >= 0 {
		return b[:i]
	}
	return b
}

// writeLFN writes a long name's entries the way ParseDir does, including
// its UTF-8 decoding: lengths count code points, 4-byte sequences become
// '_', and continuation bytes are masked with 0x7F rather than 0x3F.
func (b *builder) writeLFN(fat16Root bool, diridx *int, long string) {
	s := []byte(long)
	// Byte offset of each code point, plus the end.
	var starts []int
	for i := 0; i < len(s); {
		starts = append(starts, i)
		i++
		for i < len(s) && s[i]&0xC0 == 0x80 {
			i++
		}
	}
	n := len(starts)
	starts = append(starts, len(s))
	blocks := (n + 12) / 13
	for i := 0; i < blocks; i++ {
		e := b.addDirEntry(fat16Root, diridx)
		e[0] = byte(blocks - i)
		if i == 0 {
			e[0] |= 0x40
		}
		e[11] = attrLongName
		e[12] = 0
		p := starts[(blocks-i-1)*13]
		end := len(s)
		for j := 0; j < 13; j++ {
			slot := e[lfnSlots[j]:]
			if p > end {
				slot[0], slot[1] = 0xFF, 0xFF
				continue
			}
			if p == end {
				slot[0], slot[1] = 0, 0
				p++
				continue
			}
			c := uint16(s[p])
			p++
			at := func(k int) byte {
				if p+k < len(s) {
					return s[p+k]
				}
				return 0
			}
			switch {
			case c < 0xC0:
			case c < 0xE0 && at(0) != 0:
				c = (c&0x3f)<<6 | uint16(at(0)&0x7f)
				p++
			case c < 0xF0 && at(0) != 0 && at(1) != 0:
				c = (c&0x1f)<<12 | uint16(at(0)&0x7f)<<6 | uint16(at(1)&0x7f)
				p += 2
			case at(0) != 0 && at(1) != 0 && at(2) != 0:
				p += 3
				c = '_'
			default:
				c = '_'
			}
			slot[0], slot[1] = byte(c), byte(c>>8)
		}
	}
}

// shortName is ParseDir's "convert LFN to SFN" step for the entry at ei,
// whose long name entries precede it. lo and hi bound this directory's
// entries, for the ~N collision check.
func (b *builder) shortName(all []byte, ei, lo, hi int) {
	ent := func(i int) []byte { return all[i*direntSize : (i+1)*direntSize] }
	e := ent(ei)
	name := e[0:11]
	for i := range name {
		name[i] = ' '
	}
	ni, niext, lossy := 0, 0, false

	// Long name entries in name order: ord 1 sits just before e, the
	// 0x40-flagged last chunk furthest away.
outer:
	for li := ei - 1; li >= 0; li-- {
		le := ent(li)
		for j := 0; j < 13; j++ {
			c := binary.LittleEndian.Uint16(le[lfnSlots[j]:])
			if c == 0 {
				if niext != 0 && ni-niext > 3 {
					lossy = true
				}
				break
			}
			if c == '.' {
				if ni > 8 {
					copy(name[8:], "   ")
					ni = 8
				}
				if ni == 0 || niext != 0 {
					lossy = true
				}
				niext = ni
				continue
			}
			if c == ' ' || ni == 11 || (ni == 8 && niext == 0) {
				lossy = true
				continue
			}
			switch {
			case (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9'):
			case c >= 'a' && c <= 'z':
				c ^= 0x20
			case c < 0x80 && strings.IndexByte("$%'-_@~`!(){}^#&", byte(c)) >= 0:
			default:
				lossy = true
				c = '_'
			}
			name[ni] = byte(c)
			ni++
		}
		if le[0]&0x40 != 0 {
			break outer
		}
	}

	if niext != 0 && niext != 8 {
		for i := 2; i >= 0; i-- {
			name[8+i] = name[niext+i]
			name[niext+i] = ' '
		}
	}
	if niext != 0 && niext <= 4 && ni-niext > 3 {
		for i := niext + 3; i < 8; i++ {
			name[i] = ' '
		}
	}

	if lossy {
		if niext == 0 {
			niext = ni
		}
		for i := 1; i <= 999999; i++ {
			tail := strconv.Itoa(i)
			p := min(niext+len(tail)+1, 8)
			copy(name[p-len(tail):], tail)
			name[p-len(tail)-1] = '~'
			conflict := false
			for e2 := lo; e2 < hi; e2++ {
				o := ent(e2)
				if o[11]&(attrVolume|attrPending) == 0 && string(o[0:11]) == string(name) {
					conflict = true
					break
				}
			}
			if !conflict {
				break
			}
		}
	}

	var sum byte
	for _, c := range name {
		sum = (sum>>1 | sum<<7) + c
	}
	for li := ei - 1; li >= 0; li-- {
		le := ent(li)
		le[13] = sum
		if le[0]&0x40 != 0 {
			break
		}
	}
	e[11] &^= attrPending
}

// finish assigns file clusters, sizes the FAT and builds the MBR, boot
// sector and FSInfo sector: the rest of fatFromDOSDrive's constructor.
func (b *builder) finish(addFreeMB uint32) error {
	d := b.d
	le := binary.LittleEndian
	epc := uint32(b.entriesPerCluster())
	bytesPerCluster := b.sectorsPerCluster * sectorSize
	fileCluster := uint32(2 + uint32(len(d.dirs)/direntSize)/epc)

	fileSect := uint32(0)
	for _, region := range [][]byte{d.root, d.dirs} {
		for i := 0; i+direntSize <= len(region); i += direntSize {
			e := region[i : i+direntSize]
			size := le.Uint32(e[28:])
			if size == 0 || e[11]&attrLFNMask == attrLongName {
				continue
			}
			le.PutUint16(e[20:], uint16(fileCluster>>16))
			le.PutUint16(e[26:], uint16(fileCluster))
			numClusters := (size + bytesPerCluster - 1) / bytesPerCluster
			for c := fileCluster; c != fileCluster+numClusters-1; c++ {
				b.setFAT(c, c+1)
			}
			b.setFAT(fileCluster+numClusters-1, 0xFFFFFFF)
			if int(fileSect) >= len(d.fileAtSector) {
				return errors.New("file layout out of step with directory entries")
			}
			d.files[d.fileAtSector[fileSect]].firstSect = fileSect
			fileCluster += numClusters
			fileSect += numClusters * b.sectorsPerCluster
		}
	}

	pageClusters := sectorSize * 8 / b.fatSz
	minCluster := pageClusters
	switch b.fatSz {
	case 32:
		minCluster += 65525
	case 16:
		minCluster += 4085
	}
	addFreeClusters := (addFreeMB*(1024*1024/sectorSize) + b.sectorsPerCluster - 1) / b.sectorsPerCluster
	target := fileCluster + max(addFreeClusters, pageClusters)
	b.setFAT(max(target, minCluster)-1, 0)
	totalClusters := uint32(uint64(len(d.fat)) * 8 / uint64(b.fatSz))
	if d.readOnly {
		for c := fileCluster; c != totalClusters; c++ {
			b.setFAT(c, 0xFFFFFF7)
		}
	}

	sectorsPerFat := uint32(len(d.fat) / sectorSize)
	reserved := uint32(1)
	if b.fatSz == 32 {
		reserved = 32
	}
	partSize := totalClusters*b.sectorsPerCluster + reserved
	d.sectFat1 = sectBoot + reserved
	d.sectFat2 = d.sectFat1 + sectorsPerFat
	d.sectRoot = d.sectFat2 + sectorsPerFat
	d.sectDirs = d.sectRoot + uint32((len(d.root)+sectorSize-1)/sectorSize)
	d.sectFiles = d.sectDirs + uint32((len(d.dirs)+sectorSize-1)/sectorSize)
	d.sectFilesEnd = d.sectFiles + uint32(len(d.fileAtSector))
	d.sectEnd = sectBoot + partSize
	for i := range d.files {
		d.files[i].firstSect += d.sectFiles
	}

	serial := d.Hash
	d.mbr = make([]byte, sectorSize)
	le.PutUint32(d.mbr[440:], serial)
	p := d.mbr[446:]
	p[0] = 0x80
	if (d.sectEnd-1)/(headCount*sectorsPerTrack) <= 0x3FF {
		chsWrite(p[1:4], sectBoot)
		chsWrite(p[5:8], d.sectEnd-1)
	}
	le.PutUint32(p[8:], sectBoot)
	le.PutUint32(p[12:], partSize)
	d.mbr[510], d.mbr[511] = 0x55, 0xAA

	bs := make([]byte, sectorSize)
	copy(bs[0:], "\xEB\x3C\x90")
	copy(bs[3:], "MSWIN4.1")
	le.PutUint16(bs[11:], sectorSize)
	bs[13] = byte(b.sectorsPerCluster)
	le.PutUint16(bs[14:], uint16(reserved))
	bs[16] = 2
	bs[21] = 0xF8
	le.PutUint16(bs[24:], sectorsPerTrack)
	le.PutUint16(bs[26:], headCount)
	le.PutUint32(bs[28:], sectBoot)
	le.PutUint32(bs[32:], partSize)
	bs[510], bs[511] = 0x55, 0xAA
	code := bs[36:] // bootstrap.bootcode
	if b.fatSz != 32 {
		switch {
		case b.fatSz == 12:
			p[4] = 0x01
		case d.sectEnd < 65536:
			p[4] = 0x04
		default:
			p[4] = 0x06
		}
		le.PutUint16(bs[17:], uint16(len(d.root)/direntSize))
		le.PutUint16(bs[22:], uint16(sectorsPerFat))
		code[0], code[2] = 0x80, 0x29
		le.PutUint32(code[3:], serial+1)
		copy(code[7:], "NO NAME    ")
		copy(code[18:], "FAT1    ")
		code[22] = byte('0' + b.fatSz%10)
		d.fsinfo = make([]byte, sectorSize)
	} else {
		p[4] = 0x0C
		le.PutUint32(code[0:], sectorsPerFat)
		le.PutUint32(code[8:], fat32RootClust)
		le.PutUint16(code[12:], 1)
		le.PutUint16(code[14:], 6)
		code[28], code[30] = 0x80, 0x29
		le.PutUint32(code[31:], serial+1)
		copy(code[35:], "NO NAME    ")
		copy(code[46:], "FAT32   ")

		fs := make([]byte, sectorSize)
		le.PutUint32(fs[0:], 0x41615252)
		le.PutUint32(fs[484:], 0x61417272)
		le.PutUint32(fs[488:], totalClusters-fileCluster)
		le.PutUint32(fs[492:], fileCluster)
		le.PutUint32(fs[508:], 0xAA550000)
		d.fsinfo = fs
	}
	d.boot = bs
	return nil
}

func chsWrite(chs []byte, lba uint32) {
	cyl := lba / (headCount * sectorsPerTrack)
	head := (lba / sectorsPerTrack) % headCount
	sec := lba%sectorsPerTrack + 1
	chs[0] = byte(head)
	chs[1] = byte(sec&0x3F | (cyl>>8)&0x3) // sic: chs_write doesn't shift the cylinder bits into 6-7
	chs[2] = byte(cyl)
}

// ApplySave layers a .sav (FFDD) sector diff over the disk, as
// differencingDisk::SetupSave reads it. r must stay readable while the
// disk is read.
func (d *Disk) ApplySave(r io.ReaderAt, size int64) error {
	diff, err := parseFFDD(r, size, d.sectEnd)
	if err != nil {
		return err
	}
	d.mu.Lock()
	d.diff, d.diffRA = diff, r
	d.mu.Unlock()
	return nil
}

// parseFFDD indexes a .sav file: "FFDD\x01", then records of a
// little-endian sector number and 512 bytes of sector data. A freed slot
// (sector 0xFFFFFFFF) is skipped; a later record for the same sector wins.
func parseFFDD(r io.ReaderAt, size int64, sectors uint32) (map[uint32]int64, error) {
	head := make([]byte, 5)
	if _, err := r.ReadAt(head, 0); err != nil || string(head) != "FFDD\x01" {
		return nil, errors.New("not a DOSBox Pure disk save (FFDD header)")
	}
	diff := map[uint32]int64{}
	num := make([]byte, 4)
	for off := int64(5); off+4 <= size; off += 4 + sectorSize {
		if _, err := r.ReadAt(num, off); err != nil {
			return nil, err
		}
		sect := binary.LittleEndian.Uint32(num)
		if sect == 0xFFFFFFFF {
			continue
		}
		if sect >= sectors {
			return nil, fmt.Errorf("save diff sector %d is past the disk's end (%d sectors): the save doesn't belong to this disk", sect, sectors)
		}
		diff[sect] = off + 4
	}
	return diff, nil
}

// Overlay is a disk with a .sav sector diff applied: how DOSBox Pure's
// "Save Difference Per Content" (dosbox_pure_bootos_ramdisk = "diff")
// presents the OS image as C:, its changes kept in <content>-CDRIVE.sav
// (imageDisk::SetDifferencingDisk). It implements io.ReaderAt.
type Overlay struct {
	base io.ReaderAt
	size int64
	sav  io.ReaderAt
	diff map[uint32]int64
}

// NewOverlay layers the .sav in sav (savSize bytes) over a base disk of
// size bytes, such as a vhd.Disk.
func NewOverlay(base io.ReaderAt, size int64, sav io.ReaderAt, savSize int64) (*Overlay, error) {
	diff, err := parseFFDD(sav, savSize, uint32(size/sectorSize))
	if err != nil {
		return nil, err
	}
	return &Overlay{base: base, size: size, sav: sav, diff: diff}, nil
}

// Size returns the disk's size in bytes.
func (o *Overlay) Size() int64 { return o.size }

// ChangedSectors returns how many sectors the save changes.
func (o *Overlay) ChangedSectors() int { return len(o.diff) }

// ReadAt reads the disk, taking each sector from the save if it has one.
func (o *Overlay) ReadAt(p []byte, off int64) (int, error) {
	n := 0
	buf := make([]byte, sectorSize)
	for n < len(p) {
		pos := off + int64(n)
		if pos >= o.size {
			return n, io.EOF
		}
		sect := uint32(pos / sectorSize)
		var err error
		if at, ok := o.diff[sect]; ok {
			_, err = o.sav.ReadAt(buf, at)
		} else {
			_, err = o.base.ReadAt(buf, int64(sect)*sectorSize)
		}
		if err != nil && err != io.EOF {
			return n, err
		}
		n += copy(p[n:], buf[pos%sectorSize:])
	}
	return n, nil
}

// ChangedSectors returns how many sectors the applied save changes.
func (d *Disk) ChangedSectors() int { return len(d.diff) }

// ReadAt reads the disk: a sector from the save diff if it has one, else
// the synthesized sector (ReadSector/GetUnmodifiedSector).
func (d *Disk) ReadAt(p []byte, off int64) (int, error) {
	n := 0
	buf := make([]byte, sectorSize)
	for n < len(p) {
		pos := off + int64(n)
		if pos >= d.Size() {
			return n, io.EOF
		}
		sect := uint32(pos / sectorSize)
		if err := d.readSector(sect, buf); err != nil {
			return n, err
		}
		n += copy(p[n:], buf[pos%sectorSize:])
	}
	return n, nil
}

func (d *Disk) readSector(sect uint32, buf []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if off, ok := d.diff[sect]; ok {
		_, err := d.diffRA.ReadAt(buf, off)
		if err == io.EOF {
			err = nil // a truncated final record reads short, as fread does
		}
		return err
	}
	clear(buf)
	switch {
	case sect >= d.sectFilesEnd:
	case sect >= d.sectFiles:
		return d.readFileSector(d.fileAtSector[sect-d.sectFiles], sect, buf)
	case sect >= d.sectDirs:
		copy(buf, d.dirs[(sect-d.sectDirs)*sectorSize:])
	case sect >= d.sectRoot:
		copy(buf, d.root[(sect-d.sectRoot)*sectorSize:])
	case sect >= d.sectFat2:
		copy(buf, d.fat[(sect-d.sectFat2)*sectorSize:])
	case sect >= d.sectFat1:
		copy(buf, d.fat[(sect-d.sectFat1)*sectorSize:])
	case sect == sectBoot, sect == sectBoot+6:
		copy(buf, d.boot)
	case sect == 0:
		copy(buf, d.mbr)
	case sect == sectBoot+1, sect == sectBoot+2, sect == sectBoot+7, sect == sectBoot+8:
		copy(buf, d.fsinfo)
	}
	return nil
}

// readFileSector reads one sector of a content file, keeping a few files'
// decompressors open so sequential reads don't restart from the beginning.
func (d *Disk) readFileSector(idx int, sect uint32, buf []byte) error {
	f := d.files[idx]
	pos := int64(sect-f.firstSect) * sectorSize
	of := d.open[idx]
	if of != nil && of.pos > pos {
		of.rc.Close()
		of = nil
	}
	if of == nil {
		if len(d.open) >= 4 {
			for k, v := range d.open {
				v.rc.Close()
				delete(d.open, k)
				break
			}
		}
		rc, err := f.zf.Open()
		if err != nil {
			return err
		}
		of = &openFile{rc: rc}
		d.open[idx] = of
	}
	if _, err := io.CopyN(io.Discard, of.rc, pos-of.pos); err != nil {
		return err
	}
	n, err := io.ReadFull(of.rc, buf)
	of.pos = pos + int64(n)
	if err == io.ErrUnexpectedEOF || err == io.EOF {
		err = nil // the file's last sector: zero padded
	}
	return err
}

// Open opens a content archive and builds its D: disk.
func Open(contentPath string, freeSpaceMB int) (*Disk, *zip.ReadCloser, error) {
	zr, err := zip.OpenReader(contentPath)
	if err != nil {
		return nil, nil, err
	}
	d, err := Build(&zr.Reader, contentPath, freeSpaceMB)
	if err != nil {
		zr.Close()
		return nil, nil, err
	}
	return d, zr, nil
}

// OpenSave opens a .sav file and applies it to d.
func (d *Disk) OpenSave(path string) (*os.File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err == nil {
		err = d.ApplySave(f, info.Size())
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
