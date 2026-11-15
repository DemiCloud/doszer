package manifest

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

var (
	execLine   = regexp.MustCompile(`^\s*#?\s*exec\s*=`)
	bootHeader = regexp.MustCompile(`^\[\s*boot\s*\]\s*(#.*)?$`)
)

// SetExec sets boot.exec in manifest data, keeping everything else as it
// is: the [boot] table's first exec line, commented out or not, is
// replaced, else the key is added right after [boot], else a [boot] table
// is appended. The result must still parse.
func SetExec(data []byte, exec string) ([]byte, error) {
	nl := "\n"
	if bytes.Contains(data, []byte("\r\n")) {
		nl = "\r\n"
	}
	entry := "exec = " + quote(exec)
	lines := bytes.Split(data, []byte("\n"))
	boot := -1
	done := false
	for i, l := range lines {
		t := bytes.TrimSpace(l)
		if len(t) > 0 && t[0] == '[' {
			if boot >= 0 {
				break // end of [boot]
			}
			if bootHeader.Match(t) {
				boot = i
			}
			continue
		}
		if boot >= 0 && execLine.Match(l) {
			repl := []byte(entry)
			if bytes.HasSuffix(l, []byte("\r")) {
				repl = append(repl, '\r')
			}
			lines[i] = repl
			done = true
			break
		}
	}
	var out []byte
	switch {
	case done:
		out = bytes.Join(lines, []byte("\n"))
	case boot >= 0:
		cr := ""
		if bytes.HasSuffix(lines[boot], []byte("\r")) {
			cr = "\r"
		}
		rest := append([][]byte{[]byte(entry + cr)}, lines[boot+1:]...)
		out = bytes.Join(append(lines[:boot+1:boot+1], rest...), []byte("\n"))
	default:
		out = data
		if len(out) > 0 && !bytes.HasSuffix(out, []byte("\n")) {
			out = append(out, nl...)
		}
		out = append(out, nl+"[boot]"+nl+entry+nl...)
	}
	m, err := Parse(out, "x")
	if err != nil {
		return nil, err
	}
	if m.Boot.Exec != exec {
		return nil, fmt.Errorf("%s: boot.exec is %q after setting it to %q", FileName, m.Boot.Exec, exec)
	}
	return out, nil
}

// SetExecFile sets boot.exec in a source directory's manifest (SetExec).
func SetExecFile(sourceDir, exec string) error {
	p := filepath.Join(sourceDir, FileName)
	data, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	out, err := SetExec(data, exec)
	if err != nil {
		return err
	}
	return os.WriteFile(p, out, 0o644)
}
