// Package redact removes what identifies a person from text doszer
// writes for sharing (a run log's debug ZIP): their home directory, user
// name and host name, and any user's folder in a Windows, WSL, macOS or
// Linux path. Every rule lives here, so anything shared goes through the
// same list, and a new kind of personal data is one more rule.
package redact

import (
	"encoding/json"
	"os"
	"os/user"
	"regexp"
	"sort"
	"strings"
)

// Placeholders that stand in for what was removed.
const (
	Home = "~"
	User = "<user>"
	Host = "<host>"
)

// Redactor applies its rules, in order, to text.
type Redactor struct {
	rules []rule
}

type rule struct {
	re   *regexp.Regexp
	repl string
}

// minWord is the shortest user or host name replaced wherever it stands
// as a word: shorter ones ("al", "pc") would garble ordinary text.
const minWord = 3

// New returns the redactor for this machine: its home directory, user
// name and host name, and users' folders in paths.
func New() *Redactor {
	home, _ := os.UserHomeDir()
	var name string
	if u, err := user.Current(); err == nil {
		name = u.Username
		if i := strings.LastIndexAny(name, `\/`); i >= 0 {
			name = name[i+1:] // DOMAIN\user
		}
	}
	host, _ := os.Hostname()
	return For(home, name, host)
}

// For returns a redactor for the given home directory, user name and
// host name, any of which may be "".
func For(home, name, host string) *Redactor {
	r := &Redactor{}
	// The home directory itself, in every spelling a path of it may
	// take: as is, with the other slashes, and as WSL sees a Windows one.
	var homes []string
	if home != "" && home != "/" {
		slashed := strings.ReplaceAll(home, `\`, "/")
		homes = append(homes, home, slashed, strings.ReplaceAll(home, "/", `\`))
		if len(home) > 2 && home[1] == ':' {
			homes = append(homes, "/mnt/"+strings.ToLower(home[:1])+slashed[2:])
		}
	}
	sort.Slice(homes, func(i, j int) bool { return len(homes[i]) > len(homes[j]) })
	for _, h := range homes {
		r.add(`(?i)`+regexp.QuoteMeta(h)+`\b`, Home)
	}
	// Any user's folder: C:\Users\x, /mnt/c/Users/x, /Users/x, /home/x.
	r.add(`(?i)\b([a-z]:\\{1,2}(?:users|documents and settings)\\{1,2})[^\\/"'\s]+`, "${1}"+User)
	r.add(`(?i)\b([a-z]:/(?:users|documents and settings)/)[^\\/"'\s]+`, "${1}"+User)
	r.add(`(?i)(/mnt/[a-z]/(?:users|documents and settings)/)[^/"'\s]+`, "${1}"+User)
	r.add(`(^|[\s"'=:(])(/Users/)[^/"'\s]+`, "${1}${2}"+User)
	r.add(`(^|[\s"'=:(])(/home/)[^/"'\s]+`, "${1}${2}"+User)
	// The names themselves, wherever they stand as words.
	for _, w := range []struct{ s, repl string }{{name, User}, {host, Host}} {
		if len(w.s) >= minWord {
			r.add(`(?i)\b`+regexp.QuoteMeta(w.s)+`\b`, w.repl)
		}
	}
	return r
}

func (r *Redactor) add(expr, repl string) {
	r.rules = append(r.rules, rule{regexp.MustCompile(expr), repl})
}

// String returns s redacted.
func (r *Redactor) String(s string) string {
	for _, ru := range r.rules {
		s = ru.re.ReplaceAllString(s, ru.repl)
	}
	return s
}

// Bytes returns text redacted.
func (r *Redactor) Bytes(b []byte) []byte { return []byte(r.String(string(b))) }

// JSON returns a JSON document with every string in it (keys too)
// redacted, re-encoded indented: redacting its text instead could break
// its escaping (a Windows path's doubled backslashes).
func (r *Redactor) JSON(data []byte) ([]byte, error) {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	out, err := json.MarshalIndent(r.value(v), "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

func (r *Redactor) value(v any) any {
	switch x := v.(type) {
	case string:
		return r.String(x)
	case []any:
		for i := range x {
			x[i] = r.value(x[i])
		}
	case map[string]any:
		m := make(map[string]any, len(x))
		for k, e := range x {
			m[r.String(k)] = r.value(e)
		}
		return m
	}
	return v
}
