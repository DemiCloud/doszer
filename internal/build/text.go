package build

import (
	"bytes"
	"path"
	"strings"
)

// textNamePatterns are the base-name globs for text files (§6.6).
var textNamePatterns = []string{"*.BAT", "*.INI", "*.CONF", "CONFIG.SYS"}

// IsTextFile reports whether a file is a text file per §6.6.
func IsTextFile(fullPath string, content []byte) bool {
	if !matchAny(textNamePatterns, path.Base(fullPath)) {
		return false
	}
	return bytes.IndexByte(content, 0) == -1
}

// NormalizeCRLF converts line endings to CRLF and ensures a trailing one (§6.6).
func NormalizeCRLF(data []byte) []byte {
	s := string(data)
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\n", "\r\n")
	if s != "" && !strings.HasSuffix(s, "\r\n") {
		s += "\r\n"
	}
	return []byte(s)
}

// normalizeText applies §6.6 to every text file in fs. A file whose name
// doesn't match a text pattern is never opened, and one whose content is
// already normalized is left as its original entry - both so an entry
// writeArchive can otherwise pass straight through (§8.1) isn't needlessly
// read or replaced.
func normalizeText(fs *fileSet) error {
	for _, p := range fs.paths() {
		if !matchAny(textNamePatterns, path.Base(p)) {
			continue
		}
		e, _ := fs.get(p)
		content, err := e.peek()
		if err != nil {
			return err
		}
		if !IsTextFile(p, content) {
			continue
		}
		normalized := NormalizeCRLF(content)
		if !bytes.Equal(normalized, content) {
			fs.set(p, dataEntry(normalized, e.modified))
		}
	}
	return nil
}
