package fat

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Disk is what Write writes a volume into: the whole disk, read for the
// FSInfo sector it updates.
type Disk interface {
	io.ReaderAt
	io.WriterAt
}

// Write lays root's tree out afresh in the volume l describes: every
// directory and file contiguous, in directory order (a directory's
// clusters, then its entries' depth first), with no free space between
// them. It writes both FATs (or all l.NumFATs), FAT12/16's fixed root
// directory and the data region, marks the volume clean, and updates a
// FAT32 volume's FSInfo sectors (and their backup) with the free cluster
// count and next free cluster.
//
// The rest of the disk must already be in place: the MBR and the reserved
// sectors (boot sector, FSInfo, backup boot sector), copied from the volume
// the tree came from, since Write keeps that volume's geometry. Clusters
// Write doesn't use are left as they are, so d should start out zeroed.
//
// Short entries are written as given (name, attributes, case flags and
// times), with their cluster and size fields replaced; long names get
// fresh VFAT entries checksummed against them; "." and ".." are generated.
func Write(d Disk, l Layout, root *Node) error {
	w := &writer{l: l, cs: l.clusterSize(), first: map[*Node]uint32{}, count: map[uint32]uint32{}, next: 2}

	// Pass 1: allocate. FAT32's root stays at the cluster the boot sector
	// names (Write doesn't change the boot sector), everything else around it.
	if l.Kind == FAT32 {
		k := uint32((int64(dirSlots(root, true))*32 + w.cs - 1) / w.cs)
		if l.RootCluster < 2 || l.RootCluster+k > l.ClusterCount+2 {
			return fmt.Errorf("FAT32 root directory at cluster %d: outside the volume", l.RootCluster)
		}
		w.first[root], w.count[l.RootCluster] = l.RootCluster, k
		w.rootLo, w.rootHi = l.RootCluster, l.RootCluster+k
		w.used = k
	} else if n := dirSlots(root, true); n > l.RootEntries {
		return fmt.Errorf("root directory: %d entries, room for %d", n, l.RootEntries)
	}
	if err := w.place(root); err != nil {
		return err
	}

	// Pass 2: FATs, directories, data.
	if err := w.writeFATs(d); err != nil {
		return err
	}
	if l.Kind != FAT32 {
		buf := make([]byte, int64(l.RootEntries)*32)
		copy(buf, w.dirBytes(root, 0, 0, true))
		off := l.Base + (int64(l.ReservedSectors)+int64(l.NumFATs)*l.FATSectors)*int64(l.BytesPerSector)
		if _, err := d.WriteAt(buf, off); err != nil {
			return err
		}
	}
	if err := w.writeTree(d, root, true, 0); err != nil {
		return err
	}
	if l.Kind == FAT32 {
		return w.updateFSInfo(d)
	}
	return nil
}

type writer struct {
	l     Layout
	cs    int64
	first map[*Node]uint32  // first cluster of each node with clusters
	count map[uint32]uint32 // cluster count of each run, by first cluster
	next  uint32            // the next cluster to allocate
	used  uint32

	rootLo, rootHi uint32 // FAT32's root directory's clusters, [lo, hi)
}

// alloc gives n bytes' worth of clusters, contiguous, to node.
func (w *writer) alloc(n *Node, bytes int64) {
	k := uint32((bytes + w.cs - 1) / w.cs)
	if k == 0 {
		return
	}
	if w.next < w.rootHi && w.next+k > w.rootLo {
		w.next = w.rootHi
	}
	w.first[n] = w.next
	w.count[w.next] = k
	w.next += k
	w.used += k
}

// full reports allocation past the volume's last cluster.
func (w *writer) full() bool { return w.next > w.l.ClusterCount+2 }

func (w *writer) place(dir *Node) error {
	for _, c := range dir.Children {
		switch {
		case c.IsLabel():
		case c.IsDir():
			w.alloc(c, int64(dirSlots(c, false))*32)
			if err := w.place(c); err != nil {
				return err
			}
		default:
			if c.Size < 0 || c.Size > 0xFFFFFFFF {
				return fmt.Errorf("%s: size %d doesn't fit FAT", c.Name(), c.Size)
			}
			w.alloc(c, c.Size)
		}
		if w.full() {
			return fmt.Errorf("the files don't fit the volume's %d clusters", w.l.ClusterCount)
		}
	}
	return nil
}

// dirSlots counts a directory's 32-byte entries.
func dirSlots(dir *Node, root bool) int {
	n := 0
	if !root {
		n = 2 // "." and ".."
	}
	for _, c := range dir.Children {
		n++
		if c.Long != nil && !c.IsLabel() {
			n += (len(c.Long) + 12) / 13
		}
	}
	return n
}

func (w *writer) eoc() uint32 {
	switch w.l.Kind {
	case FAT12:
		return 0xFFF
	case FAT16:
		return 0xFFFF
	}
	return 0x0FFFFFFF
}

func (w *writer) writeFATs(d Disk) error {
	l := w.l
	fat := make([]uint32, l.ClusterCount+2)
	fat[0], fat[1] = l.FAT0, l.FAT1
	switch l.Kind { // clean shut down, no disk errors (fatgen103 §4)
	case FAT16:
		fat[1] |= 0xC000
	case FAT32:
		fat[1] |= 0x0C000000
	}
	for first, k := range w.count {
		for c := first; c < first+k-1; c++ {
			fat[c] = c + 1
		}
		fat[first+k-1] = w.eoc()
	}
	size := l.FATSectors * int64(l.BytesPerSector)
	buf := make([]byte, size)
	le := binary.LittleEndian
	for c, v := range fat {
		switch l.Kind {
		case FAT12:
			i := c * 3 / 2
			if i+1 >= len(buf) {
				return errors.New("FAT too small for its cluster count")
			}
			if c&1 == 0 {
				buf[i] = byte(v)
				buf[i+1] = buf[i+1]&0xF0 | byte(v>>8)&0x0F
			} else {
				buf[i] = buf[i]&0x0F | byte(v<<4)
				buf[i+1] = byte(v >> 4)
			}
		case FAT16:
			if c*2+2 > len(buf) {
				return errors.New("FAT too small for its cluster count")
			}
			le.PutUint16(buf[c*2:], uint16(v))
		case FAT32:
			if c*4+4 > len(buf) {
				return errors.New("FAT too small for its cluster count")
			}
			le.PutUint32(buf[c*4:], v&0x0FFFFFFF)
		}
	}
	for i := 0; i < l.NumFATs; i++ {
		off := l.Base + (int64(l.ReservedSectors)+int64(i)*l.FATSectors)*int64(l.BytesPerSector)
		if _, err := d.WriteAt(buf, off); err != nil {
			return err
		}
	}
	return nil
}

// writeTree writes dir's clusters (unless it is FAT12/16's fixed root) and
// everything under it.
func (w *writer) writeTree(d Disk, dir *Node, root bool, parent uint32) error {
	self := w.first[dir]
	if !root || w.l.Kind == FAT32 {
		b := w.dirBytes(dir, self, parent, root)
		if _, err := d.WriteAt(b, w.l.clusterOffset(self)); err != nil {
			return err
		}
	}
	if root {
		self = 0 // ".." of a root subdirectory is cluster 0, on FAT32 too
	}
	for _, c := range dir.Children {
		switch {
		case c.IsLabel():
		case c.IsDir():
			if err := w.writeTree(d, c, false, self); err != nil {
				return err
			}
		case c.Size > 0:
			r, err := c.Open()
			if err != nil {
				return fmt.Errorf("%s: %w", c.Name(), err)
			}
			n, err := io.Copy(io.NewOffsetWriter(d, w.l.clusterOffset(w.first[c])), io.LimitReader(r, c.Size))
			if cl, ok := r.(io.Closer); ok {
				cl.Close()
			}
			if err == nil && n != c.Size {
				err = fmt.Errorf("read %d of %d bytes", n, c.Size)
			}
			if err != nil {
				return fmt.Errorf("%s: %w", c.Name(), err)
			}
		}
	}
	return nil
}

// dirBytes encodes a directory's entries, padded to its clusters (FAT32's
// root and subdirectories) or unpadded (FAT12/16's root).
func (w *writer) dirBytes(dir *Node, self, parent uint32, root bool) []byte {
	var b bytes.Buffer
	if !root {
		for i, name := range []string{".          ", "..         "} {
			e := dir.Short
			copy(e[0:11], name)
			e[11] = attrDirectory
			e[12] = 0
			cl := self
			if i == 1 {
				cl = parent
			}
			setCluster(e[:], cl)
			binary.LittleEndian.PutUint32(e[28:], 0)
			b.Write(e[:])
		}
	}
	for _, c := range dir.Children {
		e := c.Short
		if c.IsLabel() {
			b.Write(e[:])
			continue
		}
		if c.Long != nil {
			for _, l := range vfatEntries(c.Long, shortNameChecksum(e[0:11])) {
				b.Write(l[:])
			}
		}
		setCluster(e[:], w.first[c])
		size := uint32(c.Size)
		if c.IsDir() {
			size = 0
		}
		binary.LittleEndian.PutUint32(e[28:], size)
		b.Write(e[:])
	}
	if root && w.l.Kind != FAT32 {
		return b.Bytes()
	}
	k := int64(w.count[self])
	if k == 0 {
		k = 1
	}
	out := make([]byte, k*w.cs)
	copy(out, b.Bytes())
	return out
}

func setCluster(e []byte, c uint32) {
	binary.LittleEndian.PutUint16(e[26:], uint16(c))
	binary.LittleEndian.PutUint16(e[20:], uint16(c>>16))
}

// vfatEntries encodes a long name as VFAT entries, in the order they're
// stored: the last part first, flagged 0x40.
func vfatEntries(long []uint16, sum byte) [][32]byte {
	n := (len(long) + 12) / 13
	units := make([]uint16, n*13)
	copy(units, long)
	for i := len(long); i < len(units); i++ {
		units[i] = 0xFFFF
	}
	if len(long) < len(units) {
		units[len(long)] = 0x0000
	}
	out := make([][32]byte, 0, n)
	for seq := n; seq >= 1; seq-- {
		var e [32]byte
		e[0] = byte(seq)
		if seq == n {
			e[0] |= 0x40
		}
		e[11] = attrLFN
		e[13] = sum
		for j, off := range []int{1, 3, 5, 7, 9, 14, 16, 18, 20, 22, 24, 28, 30} {
			binary.LittleEndian.PutUint16(e[off:], units[(seq-1)*13+j])
		}
		out = append(out, e)
	}
	return out
}

// updateFSInfo sets FAT32's free cluster count and next free cluster
// hint, in the FSInfo sector and its backup.
func (w *writer) updateFSInfo(d Disk) error {
	l := w.l
	if l.FSInfoSector == 0 || l.FSInfoSector == 0xFFFF {
		return nil
	}
	sectors := []int{l.FSInfoSector}
	if l.BackupBootSector != 0 && l.BackupBootSector != 0xFFFF {
		sectors = append(sectors, l.BackupBootSector+l.FSInfoSector)
	}
	le := binary.LittleEndian
	for _, s := range sectors {
		buf := make([]byte, l.BytesPerSector)
		off := l.Base + int64(s)*int64(l.BytesPerSector)
		if _, err := d.ReadAt(buf, off); err != nil {
			return fmt.Errorf("FSInfo: %w", err)
		}
		if le.Uint32(buf[0:]) != 0x41615252 || le.Uint32(buf[484:]) != 0x61417272 {
			return fmt.Errorf("sector %d: not an FSInfo sector", s)
		}
		next := w.next
		if next >= w.rootLo && next < w.rootHi {
			next = w.rootHi
		}
		le.PutUint32(buf[488:], l.ClusterCount-w.used)
		le.PutUint32(buf[492:], next)
		if _, err := d.WriteAt(buf, off); err != nil {
			return err
		}
	}
	return nil
}
