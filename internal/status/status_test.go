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

// A step longer than the terminal is cut to fit while drawn, so the
// spinner redraws in place; the finished line keeps it whole.
func TestLongStepFits(t *testing.T) {
	var buf bytes.Buffer
	mu.Lock()
	out, outFile = &buf, nil // no terminal to measure: 80 columns
	mu.Unlock()
	defer func() { mu.Lock(); out = nil; mu.Unlock() }()
	long := strings.Repeat("a long step ", 20)
	Step("%s", long)
	mu.Lock()
	drawn := buf.String()
	mu.Unlock()
	Done()
	line := drawn[strings.LastIndex(drawn, "\r\033[K")+len("\r\033[K"):]
	if n := len([]rune(line)); n > 79 || !strings.HasSuffix(line, "…") {
		t.Errorf("drawn line is %d characters: %q", n, line)
	}
	if !strings.Contains(buf.String(), "✓ "+long) {
		t.Error("finished line not whole")
	}
}

func TestFit(t *testing.T) {
	tests := []struct {
		s    string
		n    int
		want string
	}{
		{"short", 10, "short"},
		{"exactly10!", 10, "exactly10!"},
		{"longer than ten", 10, "longer th…"},
		{"ünïcödé text", 6, "ünïcö…"},
		{"x", 0, ""},
	}
	for _, tt := range tests {
		if got := fit(tt.s, tt.n); got != tt.want {
			t.Errorf("fit(%q, %d) = %q, want %q", tt.s, tt.n, got, tt.want)
		}
	}
}
