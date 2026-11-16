package status

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestSteps(t *testing.T) {
	tests := []struct {
		name string
		run  func()
		want []string // finished lines, in order
	}{
		{"completed steps", func() { Step("Collecting files"); Step("Writing %s", "a.dosz"); Done() },
			[]string{"✓ Collecting files", "✓ Writing a.dosz"}},
		{"failed step", func() { Step("Reading a.dosz"); Stop() },
			[]string{"✗ Reading a.dosz"}},
		{"nothing to end", func() { Done() }, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			mu.Lock()
			out = &buf
			mu.Unlock()
			defer func() { mu.Lock(); out = nil; mu.Unlock() }()
			tt.run()
			var got []string
			for _, l := range strings.Split(buf.String(), "\n") {
				if i := strings.LastIndex(l, "\r\033[K"); i >= 0 {
					l = l[i+len("\r\033[K"):]
				}
				if l != "" {
					got = append(got, l)
				}
			}
			if strings.Join(got, "|") != strings.Join(tt.want, "|") {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDisabledOffTerminal(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	Enable(f)
	Step("Collecting files")
	Done()
	if fi, _ := f.Stat(); fi.Size() != 0 {
		t.Errorf("wrote %d bytes to a file", fi.Size())
	}
}
