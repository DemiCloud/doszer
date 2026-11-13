package build

import (
	"regexp"
	"strings"
)

// matchGlob reports whether path matches pattern (§5.4): "/" separated,
// case-insensitive, "**" matches any number of directories.
func matchGlob(pattern, path string) bool {
	return matchSegments(strings.Split(pattern, "/"), strings.Split(path, "/"))
}

func matchSegments(pat, path []string) bool {
	if len(pat) == 0 {
		return len(path) == 0
	}

	if pat[0] == "**" {
		if matchSegments(pat[1:], path) {
			return true
		}
		if len(path) > 0 && matchSegments(pat, path[1:]) {
			return true
		}
		return false
	}

	if len(path) == 0 {
		return false
	}
	if !segmentRegexp(pat[0]).MatchString(path[0]) {
		return false
	}
	return matchSegments(pat[1:], path[1:])
}

var segmentCache = map[string]*regexp.Regexp{}

// segmentRegexp compiles one path segment's glob into a cached regexp.
func segmentRegexp(seg string) *regexp.Regexp {
	if re, ok := segmentCache[seg]; ok {
		return re
	}

	var b strings.Builder
	b.WriteString("(?i)^")
	for _, r := range seg {
		switch r {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteString("$")

	re := regexp.MustCompile(b.String())
	segmentCache[seg] = re
	return re
}

// matchAny reports whether path matches any of patterns.
func matchAny(patterns []string, path string) bool {
	for _, p := range patterns {
		if matchGlob(p, path) {
			return true
		}
	}
	return false
}
