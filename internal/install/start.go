package install

import (
	"path"
	"sort"
	"strings"
)

// StartPrograms lists what an install added or changed under dir that
// could start the game: .BAT, .COM and .EXE files in dir or one folder
// below it, shallowest first, then batch files before programs, then by
// path. Paths are relative to the drive root, slash-separated.
func StartPrograms(changes []Change, dir string) []string {
	dir = strings.Trim(dir, "/")
	type cand struct {
		path         string
		depth, order int
	}
	var cs []cand
	for _, c := range changes {
		if c.Kind == Deleted || !under(c.Path, dir) {
			continue
		}
		rel := c.Path
		if dir != "" {
			rel = c.Path[len(dir)+1:]
		}
		depth := strings.Count(rel, "/")
		if depth > 1 {
			continue
		}
		order := map[string]int{".bat": 0, ".com": 1, ".exe": 2}
		o, ok := order[strings.ToLower(path.Ext(c.Path))]
		if !ok {
			continue
		}
		cs = append(cs, cand{c.Path, depth, o})
	}
	sort.Slice(cs, func(i, j int) bool {
		a, b := cs[i], cs[j]
		if a.depth != b.depth {
			return a.depth < b.depth
		}
		if a.order != b.order {
			return a.order < b.order
		}
		return strings.ToLower(a.path) < strings.ToLower(b.path)
	})
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.path
	}
	return out
}

// DOSPath turns a drive-relative slash path into a DOS path on drive
// ("C:"), e.g. C:\GAME\GO.BAT.
func DOSPath(drive, p string) string {
	return drive + `\` + strings.ToUpper(strings.ReplaceAll(p, "/", `\`))
}
