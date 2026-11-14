package vdisk

// hashMap ports DOSBox Pure's BaseHashMap/StringToPointerHashMap
// (src/dos/drives.h) exactly, because its slot order is the order
// zipDrive::FindNext returns directory entries in - and so the order of
// the directory entries, and the cluster layout, of the disk
// fatFromDOSDrive builds from them.
//
// Like the original, it compares keys by hash only: two names with the
// same hash are the same entry.
type hashMap[V any] struct {
	n      uint32 // entries
	maxlen uint32 // capacity - 1 (a power of two minus one), 0 when empty
	keys   []uint32
	vals   []V
}

// fnvHash is BaseStringToPointerHashMap::Hash: 32-bit FNV-1 over the
// string's bytes, each sign-extended as the C++ code's (Bit32u)*(char*)
// does on platforms where char is signed (x86, which DOSBox Pure ships on).
func fnvHash(s string) uint32 {
	h := uint32(0x811c9dc5)
	for i := 0; i < len(s) && s[i] != 0; i++ {
		h = (h * 0x01000193) ^ uint32(int32(int8(s[i])))
	}
	return h
}

func (m *hashMap[V]) get(key uint32) (V, bool) {
	var zero V
	if m.n == 0 {
		return zero, false
	}
	k1 := key
	if k1 == 0 {
		k1 = 1
	}
	for i := k1; ; i++ {
		i &= m.maxlen
		if m.keys[i] == k1 {
			return m.vals[i], true
		}
		if m.keys[i] == 0 {
			return zero, false
		}
	}
}

func (m *hashMap[V]) put(key uint32, v V) {
	if m.n*2 >= m.maxlen {
		m.grow()
	}
	k1 := key
	if k1 == 0 {
		k1 = 1
	}
	for i := k1; ; i++ {
		i &= m.maxlen
		if m.keys[i] == 0 {
			m.n++
			m.keys[i], m.vals[i] = k1, v
			return
		}
		if m.keys[i] == k1 {
			m.vals[i] = v
			return
		}
	}
}

func (m *hashMap[V]) grow() {
	oldKeys, oldVals := m.keys, m.vals
	if m.maxlen == 0 {
		m.maxlen = 15
	} else {
		m.maxlen = m.maxlen*2 + 1
	}
	m.keys = make([]uint32, m.maxlen+1)
	m.vals = make([]V, m.maxlen+1)
	for i, k := range oldKeys {
		if k == 0 {
			continue
		}
		for j := k; ; j++ {
			j &= m.maxlen
			if m.keys[j] == 0 {
				m.keys[j], m.vals[j] = k, oldVals[i]
				break
			}
		}
	}
}

// inOrder returns the values in slot order, as GetAtIndex over
// [0, Capacity()) visits them.
func (m *hashMap[V]) inOrder() []V {
	var out []V
	for i, k := range m.keys {
		if k != 0 {
			out = append(out, m.vals[i])
		}
	}
	return out
}
