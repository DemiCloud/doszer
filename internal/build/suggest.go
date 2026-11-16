package build

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// maxScanSize skips scanning disk-backed files larger than this (disc images).
const maxScanSize = 8 << 20

// suggestRewrites finds references to names flatten dropped and returns
// ready-to-paste [[layout.rewrite]] snippets, plus warnings for binary files
// it can't rewrite (§6.5). Each file is read and lower-cased once, however
// many names it is searched for.
func suggestRewrites(fs *fileSet, dropped []string) (suggestions []string, binaryRefs []string) {
	var names []string // unique case-insensitively, first casing kept
	seen := make(map[string]bool, len(dropped))
	for _, name := range dropped {
		if lower := strings.ToLower(name); !seen[lower] {
			seen[lower] = true
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return nil, nil
	}

	textFiles := make([][]string, len(names))
	binary := make([][]string, len(names))
	for _, p := range fs.paths() {
		e, _ := fs.get(p)
		if size, err := e.size(); err != nil || size > maxScanSize {
			continue
		}
		content, err := e.peek()
		if err != nil {
			continue
		}
		lower := bytes.ToLower(content)
		for i, name := range names {
			n := strings.ToLower(name)
			if !bytes.Contains(lower, []byte(n+`\`)) && !bytes.Contains(lower, []byte(n+"/")) {
				continue
			}
			if rewritable(content) {
				textFiles[i] = append(textFiles[i], p)
			} else {
				binary[i] = append(binary[i], fmt.Sprintf("%s references %q but is binary (contains a NUL byte); cannot rewrite automatically", p, name))
			}
		}
	}

	for i, name := range names {
		binaryRefs = append(binaryRefs, binary[i]...)
		if len(textFiles[i]) > 0 {
			suggestions = append(suggestions, fmt.Sprintf(
				"[[layout.rewrite]]\nfrom  = %s\nto    = \"\"\nfiles = %s",
				strconv.Quote(name+`\`), quotePaths(textFiles[i])))
		}
	}
	return suggestions, binaryRefs
}

func quotePaths(paths []string) string {
	parts := make([]string, len(paths))
	for i, p := range paths {
		parts[i] = strconv.Quote(p)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
