// Package status shows what a command is doing on a terminal: the
// current step beside a spinner, optionally with a count, and each step
// once finished. It is silent unless Enable was called with a terminal,
// so packages report steps unconditionally and tests and pipes see
// nothing.
package status

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

var (
	mu       sync.Mutex
	out      io.Writer // nil: disabled
	outFile  *os.File
	step     string
	started  time.Time
	n, total int
	frame    int
	ticker   *time.Ticker
	stop     chan struct{}
	hook     func(step string, started time.Time, d time.Duration, ok bool)
)

var frames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Enable turns reporting on if f is a terminal (and TERM isn't "dumb").
func Enable(f *os.File) {
	if os.Getenv("TERM") == "dumb" {
		return
	}
	if fi, err := f.Stat(); err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return
	}
	mu.Lock()
	out, outFile = f, f
	mu.Unlock()
}

// Force turns reporting on for f even when it isn't a terminal: an
// explicit --format fancy.
func Force(f *os.File) {
	mu.Lock()
	out, outFile = f, f
	mu.Unlock()
}

// Disable turns reporting off.
func Disable() {
	mu.Lock()
	out, outFile = nil, nil
	mu.Unlock()
}

// SetHook calls f with every step once it has finished, shown or not
// (a run log's step timings). f runs under the package's lock: it must
// not call back into status.
func SetHook(f func(step string, started time.Time, d time.Duration, ok bool)) {
	mu.Lock()
	hook = f
	mu.Unlock()
}

// Step finishes the current step, if any, and starts a new one.
func Step(format string, args ...any) {
	mu.Lock()
	defer mu.Unlock()
	if out == nil && hook == nil {
		return
	}
	finish(true)
	step, started, n, total = fmt.Sprintf(format, args...), time.Now(), 0, 0
	if out == nil {
		return
	}
	draw()
	if ticker == nil {
		ticker, stop = time.NewTicker(100*time.Millisecond), make(chan struct{})
		go spin(ticker, stop)
	}
}

// Count shows progress through the current step: done of all.
func Count(done, all int) {
	mu.Lock()
	defer mu.Unlock()
	if step == "" {
		return
	}
	n, total = done, all
}

// Done finishes the current step as completed.
func Done() { end(true) }

// Stop ends the current step as failed, before reporting an error.
func Stop() { end(false) }

// Printf prints a line to w, clearing the spinner first if w shares its
// terminal and redrawing it after.
func Printf(w io.Writer, format string, args ...any) {
	mu.Lock()
	defer mu.Unlock()
	active := out != nil && step != ""
	if active {
		fmt.Fprint(out, "\r\033[K")
	}
	fmt.Fprintf(w, format, args...)
	if active {
		draw()
	}
}

func end(ok bool) {
	mu.Lock()
	defer mu.Unlock()
	finish(ok)
	if ticker != nil {
		ticker.Stop()
		close(stop)
		ticker = nil
	}
}

// finish replaces the spinner line with the step, completed or failed.
func finish(ok bool) {
	if step == "" {
		return
	}
	d := time.Since(started)
	if hook != nil {
		hook(step, started, d, ok)
	}
	if out != nil {
		fmt.Fprint(out, "\r\033[K")
		mark := "✓ "
		if !ok {
			mark = "✗ "
		}
		line := mark + step
		if d >= time.Second {
			line += fmt.Sprintf(" (%s)", d.Round(100*time.Millisecond))
		}
		fmt.Fprintln(out, line)
	}
	step = ""
}

// draw redraws the spinner line, cut to the terminal's width: a line that
// wrapped would be redrawn on a new line every tick.
func draw() {
	if out == nil {
		return
	}
	var suffix string
	if total > 0 {
		suffix += fmt.Sprintf(" (%d/%d)", n, total)
	}
	if d := time.Since(started); d >= 2*time.Second {
		suffix += fmt.Sprintf(" %ds", int(d.Seconds()))
	}
	width := 0
	if outFile != nil {
		width = termWidth(outFile)
	}
	if width <= 0 {
		width = 80
	}
	text := fit(step, width-1-2-len([]rune(suffix))) // spinner and space; the last column stays free
	fmt.Fprint(out, "\r\033[K"+frames[frame%len(frames)]+" "+text+suffix)
}

// fit cuts s to at most n characters, ending it with "…" if cut.
func fit(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n < 1 {
		return ""
	}
	return string(r[:n-1]) + "…"
}

func spin(t *time.Ticker, stop chan struct{}) {
	for {
		select {
		case <-t.C:
			mu.Lock()
			if step != "" {
				frame++
				draw()
			}
			mu.Unlock()
		case <-stop:
			return
		}
	}
}
