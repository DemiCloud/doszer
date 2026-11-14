package build

import (
	"fmt"
	"strconv"
	"strings"
)

// maxScanSize skips scanning disk-backed files larger than this (disc images).
const maxScanSize = 8 << 20

// suggestRewrites finds references to names flatten dropped and returns
// ready-to-paste [[layout.rewrite]] snippets, plus warnings for binary files
// it can't rewrite (§6.5).
func suggestRewrites(fs *fileSet, dropped []string) (suggestions []string, binaryRefs []string) {
	seen := make(map[string]bool, len(dropped))

	for _, name := range dropped {
		lower := strings.ToLower(name)
		if seen[lower] {
			continue
		}
		seen[lower] = true

		needle := name + `\`
		var textFiles []string

		for _, p := range fs.paths() {
			e, _ := fs.get(p)
			if size, err := e.size(); err != nil || size > maxScanSize {
				continue
			}
			content, err := e.peek()
			if err != nil {
				continue
			}
			if !containsFold(content, []byte(needle)) && !containsFold(content, []byte(name+"/")) {
				continue
			}

			if IsTextFile(p, content) {
				textFiles = append(textFiles, p)
			} else {
				binaryRefs = append(binaryRefs, fmt.Sprintf("%s references %q but is not a text file (§6.6); cannot rewrite automatically", p, name))
			}
		}

		if len(textFiles) > 0 {
			suggestions = append(suggestions, fmt.Sprintf(
				"[[layout.rewrite]]\nfrom  = %s\nto    = \"\"\nfiles = %s",
				strconv.Quote(needle), quotePaths(textFiles)))
		}
	}

	return suggestions, binaryRefs
}

func containsFold(content, needle []byte) bool {
	return strings.Contains(strings.ToLower(string(content)), strings.ToLower(string(needle)))
}

func quotePaths(paths []string) string {
	parts := make([]string, len(paths))
	for i, p := range paths {
		parts[i] = strconv.Quote(p)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
