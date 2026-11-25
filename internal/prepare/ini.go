package prepare

import (
	"bytes"
	"strings"
)

// iniFile is a Windows INI file edited line by line: only the lines a
// change touches are rewritten, so comments, order, spelling, unknown
// lines and line endings stay as they were. Section and key names match
// case-insensitively, as Windows' profile functions match them; of
// several sections with one name, Windows reads the first.
type iniFile struct {
	lines []string
	eol   string // "\r\n" if the file used it, else "\n"
	final bool   // the last line ended with a line break
	tail  []byte // a trailing Ctrl-Z (DOS end-of-file marker), kept last
}

func parseINI(b []byte) *iniFile {
	f := &iniFile{eol: "\n"}
	if i := bytes.IndexByte(b, 0x1A); i >= 0 && len(bytes.TrimRight(b[i:], "\x1a\r\n")) == 0 {
		b, f.tail = b[:i], b[i:]
	}
	if bytes.Contains(b, []byte("\r\n")) {
		f.eol = "\r\n"
	}
	if len(b) == 0 {
		f.final = true
		return f
	}
	s := string(b)
	f.final = strings.HasSuffix(s, "\n")
	s = strings.TrimSuffix(s, "\n")
	for _, l := range strings.Split(s, "\n") {
		f.lines = append(f.lines, strings.TrimSuffix(l, "\r"))
	}
	return f
}

func (f *iniFile) bytes() []byte {
	var b bytes.Buffer
	for i, l := range f.lines {
		b.WriteString(l)
		if i < len(f.lines)-1 || f.final {
			b.WriteString(f.eol)
		}
	}
	b.Write(f.tail)
	return b.Bytes()
}

// sectionName returns the name of the section line l starts, if it is one.
func sectionName(l string) (string, bool) {
	t := strings.TrimSpace(l)
	if !strings.HasPrefix(t, "[") {
		return "", false
	}
	end := strings.IndexByte(t, ']')
	if end < 0 {
		return "", false
	}
	return strings.TrimSpace(t[1:end]), true
}

// keyName returns the key line l sets, if it is one.
func keyName(l string) (string, bool) {
	t := strings.TrimSpace(l)
	if t == "" || t[0] == ';' {
		return "", false
	}
	k, _, ok := strings.Cut(t, "=")
	return strings.TrimSpace(k), ok
}

// sections returns the [start, end) line ranges of the bodies of every
// section named name, in file order.
func (f *iniFile) sections(name string) [][2]int {
	var out [][2]int
	in := -1
	for i, l := range f.lines {
		if n, ok := sectionName(l); ok {
			if in >= 0 {
				out = append(out, [2]int{in, i})
				in = -1
			}
			if strings.EqualFold(n, name) {
				in = i + 1
			}
		}
	}
	if in >= 0 {
		out = append(out, [2]int{in, len(f.lines)})
	}
	return out
}

// get returns key's value in section, as Windows would read it.
func (f *iniFile) get(section, key string) (string, bool) {
	for _, r := range f.sections(section) {
		for _, l := range f.lines[r[0]:r[1]] {
			if k, ok := keyName(l); ok && strings.EqualFold(k, key) {
				_, v, _ := strings.Cut(l, "=")
				return strings.TrimSpace(v), true
			}
		}
	}
	return "", false
}

// set makes key = value in section: the line Windows would read is
// rewritten, else the key is added at the end of the first such section,
// else a new section is added at the end of the file. It reports whether
// the file changed.
func (f *iniFile) set(section, key, value string) bool {
	line := key + "=" + value
	secs := f.sections(section)
	for _, r := range secs {
		for i := r[0]; i < r[1]; i++ {
			if k, ok := keyName(f.lines[i]); ok && strings.EqualFold(k, key) {
				_, v, _ := strings.Cut(f.lines[i], "=")
				if strings.TrimSpace(v) == value {
					return false
				}
				f.lines[i] = k + "=" + value
				return true
			}
		}
	}
	if len(secs) > 0 {
		at := secs[0][1]
		for at > secs[0][0] && strings.TrimSpace(f.lines[at-1]) == "" {
			at--
		}
		f.lines = append(f.lines[:at], append([]string{line}, f.lines[at:]...)...)
		return true
	}
	if n := len(f.lines); n > 0 && strings.TrimSpace(f.lines[n-1]) != "" {
		f.lines = append(f.lines, "")
	}
	f.lines = append(f.lines, "["+section+"]", line)
	f.final = true
	return true
}

// remove deletes key from every section named section, reporting whether
// it was there.
func (f *iniFile) remove(section, key string) bool {
	secs := f.sections(section)
	drop := map[int]bool{}
	for _, r := range secs {
		for i := r[0]; i < r[1]; i++ {
			if k, ok := keyName(f.lines[i]); ok && strings.EqualFold(k, key) {
				drop[i] = true
			}
		}
	}
	if len(drop) == 0 {
		return false
	}
	kept := f.lines[:0]
	for i, l := range f.lines {
		if !drop[i] {
			kept = append(kept, l)
		}
	}
	f.lines = kept
	return true
}
