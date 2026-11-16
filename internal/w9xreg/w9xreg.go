// Package w9xreg reads Windows 95/98/ME registry files (SYSTEM.DAT,
// USER.DAT: the "CREG" format), finds what one adds to another, and
// writes that as a REGEDIT4 file Windows' REGEDIT /S imports.
//
// Format, as Wine's old Windows 95 registry loader read it
// (misc/registry.c, _w95creg/_w95rgkn/_w95dke/_w95dkh/_w95dkv):
//
//	CREG header (0x20): "CREG", version, offset of the first RGDB block,
//	    checksum, u16 number of RGDB blocks
//	RGKN block at 0x20: "RGKN", size, offset of the root key entry (from
//	    the RGKN block's start); key entries of 0x1C bytes: in use, hash,
//	    next free, parent, first child, next sibling (offsets from the
//	    RGKN block's start, 0xFFFFFFFF none), u16 id, u16 RGDB block
//	RGDB blocks: "RGDB", size, ... (0x20 header), then key records:
//	    size, u16 id, u16 block, bytes used, u16 name length, u16 value
//	    count, unused, name, values: type, unused, u16 name length,
//	    u16 data length, name, data
//
// Checked against a Windows 98 SE SYSTEM.DAT and USER.DAT (15525 and 501
// keys). Names and string data are in the system's ANSI code page, kept
// as bytes.
package w9xreg

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Value types.
const (
	TypeSZ       = 1
	TypeExpandSZ = 2
	TypeBinary   = 3
	TypeDWORD    = 4
	TypeMultiSZ  = 7
)

// Value is a registry value; an empty Name is the key's default value.
type Value struct {
	Name string
	Type uint32
	Data []byte
}

// Key is a registry key.
type Key struct {
	Name    string
	Values  []Value
	Subkeys []*Key
}

const none = 0xFFFFFFFF

var le = binary.LittleEndian

// Parse reads a registry file. Its root key is the hive itself; its name is
// usually empty.
func Parse(data []byte) (*Key, error) {
	if len(data) < 0x40 || string(data[:4]) != "CREG" {
		return nil, errors.New("not a Windows 9x registry file")
	}
	rgdbOff := le.Uint32(data[8:])
	rgdbNum := int(le.Uint16(data[0x10:]))
	const rgkn = 0x20
	if string(data[rgkn:rgkn+4]) != "RGKN" {
		return nil, errors.New("registry: no RGKN block")
	}
	rgknSize := le.Uint32(data[rgkn+4:])
	rootOff := le.Uint32(data[rgkn+8:])
	if uint64(rgkn)+uint64(rgknSize) > uint64(len(data)) {
		return nil, errors.New("registry: RGKN block runs past the end of the file")
	}

	// Key records by block<<16 | id.
	records := map[uint32][]byte{}
	off := uint64(rgdbOff)
	for b := 0; b < rgdbNum; b++ {
		if off+0x20 > uint64(len(data)) || string(data[off:off+4]) != "RGDB" {
			return nil, fmt.Errorf("registry: RGDB block %d missing", b)
		}
		size := uint64(le.Uint32(data[off+4:]))
		end := off + size
		if size < 0x20 || end > uint64(len(data)) {
			return nil, fmt.Errorf("registry: RGDB block %d has a bad size", b)
		}
		for r := off + 0x20; r+0x14 <= end; {
			recSize := uint64(le.Uint32(data[r:]))
			if recSize < 0x14 || r+recSize > end {
				break
			}
			id := le.Uint16(data[r+4:])
			blk := le.Uint16(data[r+6:])
			used := uint64(le.Uint32(data[r+8:]))
			if id != 0xFFFF && used >= 0x14 && used <= recSize {
				records[uint32(blk)<<16|uint32(id)] = data[r : r+used]
			}
			r += recSize
		}
		off = end
	}

	seen := map[uint32]bool{}
	var walk func(entry uint32) (*Key, error)
	walk = func(entry uint32) (*Key, error) {
		if seen[entry] {
			return nil, errors.New("registry: key entries form a loop")
		}
		seen[entry] = true
		p := uint64(rgkn) + uint64(entry)
		if uint64(entry)+0x1C > uint64(rgknSize) {
			return nil, fmt.Errorf("registry: key entry at %#x is outside the RGKN block", entry)
		}
		child := le.Uint32(data[p+0x10:])
		id := le.Uint16(data[p+0x18:])
		blk := le.Uint16(data[p+0x1A:])
		k := &Key{}
		if blk != 0xFFFF {
			rec, ok := records[uint32(blk)<<16|uint32(id)]
			if !ok {
				return nil, fmt.Errorf("registry: no record for key %d in block %d", id, blk)
			}
			if err := k.decode(rec); err != nil {
				return nil, err
			}
		}
		for c := child; c != none; {
			sub, err := walk(c)
			if err != nil {
				return nil, err
			}
			k.Subkeys = append(k.Subkeys, sub)
			c = le.Uint32(data[uint64(rgkn)+uint64(c)+0x14:])
		}
		return k, nil
	}
	return walk(rootOff)
}

// decode reads a key record's name and values.
func (k *Key) decode(rec []byte) error {
	nameLen := int(le.Uint16(rec[0xC:]))
	n := int(le.Uint16(rec[0xE:]))
	p := 0x14
	if p+nameLen > len(rec) {
		return errors.New("registry: key name runs past its record")
	}
	k.Name = string(rec[p : p+nameLen])
	p += nameLen
	for i := 0; i < n; i++ {
		if p+0xC > len(rec) {
			return fmt.Errorf("registry: key %q: value %d runs past its record", k.Name, i)
		}
		v := Value{Type: le.Uint32(rec[p:])}
		vn := int(le.Uint16(rec[p+8:]))
		vd := int(le.Uint16(rec[p+0xA:]))
		p += 0xC
		if p+vn+vd > len(rec) {
			return fmt.Errorf("registry: key %q: value %d runs past its record", k.Name, i)
		}
		v.Name = string(rec[p : p+vn])
		v.Data = append([]byte(nil), rec[p+vn:p+vn+vd]...)
		p += vn + vd
		k.Values = append(k.Values, v)
	}
	return nil
}

// Entry is a key (Value nil) or a value that Added found, by its key's
// path below the root, backslash-separated.
type Entry struct {
	Key   string
	Value *Value
}

// Added lists what after has that before doesn't: keys, and values that
// are new or differ in type or data. Names compare case-insensitively, as
// Windows does. Removals aren't listed. Keys come in path order, a key
// before its values.
func Added(before, after *Key) []Entry {
	var out []Entry
	var walk func(b, a *Key, path string)
	walk = func(b, a *Key, path string) {
		if path != "" {
			if b == nil {
				out = append(out, Entry{Key: path})
			}
			for i := range a.Values {
				v := &a.Values[i]
				if b == nil || !hasValue(b, v) {
					out = append(out, Entry{Key: path, Value: v})
				}
			}
		}
		subs := append([]*Key(nil), a.Subkeys...)
		sort.Slice(subs, func(i, j int) bool { return strings.ToLower(subs[i].Name) < strings.ToLower(subs[j].Name) })
		for _, s := range subs {
			var bs *Key
			if b != nil {
				bs = b.subkey(s.Name)
			}
			p := s.Name
			if path != "" {
				p = path + `\` + s.Name
			}
			walk(bs, s, p)
		}
	}
	walk(before, after, "")
	return out
}

func (k *Key) subkey(name string) *Key {
	for _, s := range k.Subkeys {
		if strings.EqualFold(s.Name, name) {
			return s
		}
	}
	return nil
}

func hasValue(k *Key, v *Value) bool {
	for _, o := range k.Values {
		if strings.EqualFold(o.Name, v.Name) {
			return o.Type == v.Type && bytes.Equal(o.Data, v.Data)
		}
	}
	return false
}

// WriteReg writes entries as a REGEDIT4 file with CRLF line endings, each
// key path prefixed with root (e.g. HKEY_LOCAL_MACHINE).
func WriteReg(w io.Writer, groups ...Group) error {
	var b strings.Builder
	b.WriteString("REGEDIT4\r\n")
	last := ""
	for _, g := range groups {
		for _, e := range g.Entries {
			key := g.Root + `\` + e.Key
			if key != last {
				fmt.Fprintf(&b, "\r\n[%s]\r\n", key)
				last = key
			}
			if e.Value != nil {
				b.WriteString(regLine(e.Value))
			}
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// Group is entries under one root key, for WriteReg.
type Group struct {
	Root    string
	Entries []Entry
}

// regLine formats a value the way REGEDIT exports it.
func regLine(v *Value) string {
	name := "@"
	if v.Name != "" {
		name = quote(v.Name)
	}
	switch {
	case v.Type == TypeSZ && !bytes.ContainsRune(bytes.TrimSuffix(v.Data, []byte{0}), 0):
		return name + "=" + quote(string(bytes.TrimSuffix(v.Data, []byte{0}))) + "\r\n"
	case v.Type == TypeDWORD && len(v.Data) == 4:
		return fmt.Sprintf("%s=dword:%08x\r\n", name, le.Uint32(v.Data))
	case v.Type == TypeBinary:
		return hexLine(name+"=hex:", v.Data)
	default:
		return hexLine(fmt.Sprintf("%s=hex(%x):", name, v.Type), v.Data)
	}
}

// hexLine writes bytes as comma-separated hex, wrapped with a trailing
// backslash before 80 columns, as REGEDIT does.
func hexLine(prefix string, data []byte) string {
	var b strings.Builder
	b.WriteString(prefix)
	col := len(prefix)
	for i, c := range data {
		s := fmt.Sprintf("%02x", c)
		if i < len(data)-1 {
			s += ","
		}
		if col+len(s) > 77 && i > 0 {
			b.WriteString("\\\r\n  ")
			col = 2
		}
		b.WriteString(s)
		col += len(s)
	}
	b.WriteString("\r\n")
	return b.String()
}

func quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// Encode writes root as a registry file Parse reads back: one RGDB block,
// the root without a record of its own. For building test fixtures.
func Encode(root *Key) []byte {
	type node struct {
		k                   *Key
		parent, child, next uint32
		id                  uint16
		off                 uint32
	}
	var nodes []*node
	var add func(k *Key) *node
	add = func(k *Key) *node {
		n := &node{k: k, parent: none, child: none, next: none, id: uint16(len(nodes) - 1)}
		n.off = 0x20 + uint32(len(nodes))*0x1C
		nodes = append(nodes, n)
		var prev *node
		for _, s := range k.Subkeys {
			c := add(s)
			c.parent = n.off
			if prev == nil {
				n.child = c.off
			} else {
				prev.next = c.off
			}
			prev = c
		}
		return n
	}
	add(root)

	rgkn := make([]byte, 0x20+len(nodes)*0x1C)
	copy(rgkn, "RGKN")
	le.PutUint32(rgkn[4:], uint32(len(rgkn)))
	le.PutUint32(rgkn[8:], 0x20)
	var recs bytes.Buffer
	for i, n := range nodes {
		e := rgkn[n.off:]
		le.PutUint32(e[0xC:], n.parent)
		le.PutUint32(e[0x10:], n.child)
		le.PutUint32(e[0x14:], n.next)
		if i == 0 {
			le.PutUint16(e[0x18:], 0xFFFF)
			le.PutUint16(e[0x1A:], 0xFFFF)
			continue
		}
		le.PutUint16(e[0x18:], n.id)
		var r bytes.Buffer
		r.Write(make([]byte, 0x14))
		r.WriteString(n.k.Name)
		for _, v := range n.k.Values {
			var h [0xC]byte
			le.PutUint32(h[0:], v.Type)
			le.PutUint16(h[8:], uint16(len(v.Name)))
			le.PutUint16(h[0xA:], uint16(len(v.Data)))
			r.Write(h[:])
			r.WriteString(v.Name)
			r.Write(v.Data)
		}
		b := r.Bytes()
		le.PutUint32(b[0:], uint32(len(b)))
		le.PutUint16(b[4:], n.id)
		le.PutUint32(b[8:], uint32(len(b)))
		le.PutUint16(b[0xC:], uint16(len(n.k.Name)))
		le.PutUint16(b[0xE:], uint16(len(n.k.Values)))
		le.PutUint32(b[0x10:], none)
		recs.Write(b)
	}
	rgdb := make([]byte, 0x20, 0x20+recs.Len())
	copy(rgdb, "RGDB")
	rgdb = append(rgdb, recs.Bytes()...)
	le.PutUint32(rgdb[4:], uint32(len(rgdb)))

	out := make([]byte, 0x20)
	copy(out, "CREG")
	le.PutUint32(out[4:], 0x00010000)
	le.PutUint32(out[8:], uint32(0x20+len(rgkn)))
	le.PutUint16(out[0x10:], 1)
	out = append(out, rgkn...)
	return append(out, rgdb...)
}
