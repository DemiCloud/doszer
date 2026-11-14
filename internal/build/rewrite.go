package build

import (
	"bytes"
	"fmt"
	"regexp"

	"github.com/DemiCloud/doszer/internal/manifest"
)

// RewriteHit is one file a rewrite rule changed, for dry-run reporting.
type RewriteHit struct {
	Rule int
	Path string
}

// applyRewrite applies each rule in order to the files it matches that
// contain no NUL byte (§6.5), warning per §11 on a rule that replaced
// nothing.
func applyRewrite(fs *fileSet, rules []manifest.Rewrite) ([]RewriteHit, []string, error) {
	var hits []RewriteHit
	var warnings []string

	for i, rule := range rules {
		total := 0
		for _, p := range fs.paths() {
			if !matchAny(rule.Files, p) {
				continue
			}

			e, _ := fs.get(p)
			content, err := e.bytes()
			if err != nil {
				return nil, nil, err
			}
			if !rewritable(content) {
				continue
			}

			replaced, n := replaceFold(string(content), rule.From, rule.To)
			if n == 0 {
				continue
			}
			fs.set(p, dataEntry([]byte(replaced), e.modified))
			total += n
			hits = append(hits, RewriteHit{Rule: i, Path: p})
		}

		if total == 0 {
			warnings = append(warnings, fmt.Sprintf("layout.rewrite: rule %q -> %q matched nothing", rule.From, rule.To))
		}
	}

	return hits, warnings, nil
}

// rewritable reports whether §6.5 may rewrite a file: it contains no NUL
// byte. Unlike §6.6's text files, its name doesn't matter - a rule's
// files patterns choose it.
func rewritable(content []byte) bool {
	return !bytes.Contains(content, []byte{0})
}

// replaceFold case-insensitively replaces from with to, returning the
// result and replacement count.
func replaceFold(s, from, to string) (string, int) {
	if from == "" {
		return s, 0
	}

	re := regexp.MustCompile("(?i)" + regexp.QuoteMeta(from))
	count := 0
	result := re.ReplaceAllStringFunc(s, func(string) string {
		count++
		return to
	})
	return result, count
}
