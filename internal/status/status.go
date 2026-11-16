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
	step     string
	started  time.Time
	n, total int
	frame    int
	ticker   *time.Ticker
	stop     chan struct{}
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
	out = f
	mu.Unlock()
}

// Step finishes the current step, if any, and starts a new one.
func Step(format string, args ...any) {
	mu.Lock()
	defer mu.Unlock()
	if out == nil {
		return
	}
	finish(true)
	step, started, n, total = fmt.Sprintf(format, args...), time.Now(), 0, 0
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
	if out == nil || step == "" {
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
	if out == nil {
		return
	}
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
	fmt.Fprint(out, "\r\033[K")
	mark := "✓ "
	if !ok {
		mark = "✗ "
	}
	line := mark + step
	if d := time.Since(started); d >= time.Second {
		line += fmt.Sprintf(" (%s)", d.Round(100*time.Millisecond))
	}
	fmt.Fprintln(out, line)
	step = ""
}

func draw() {
	line := frames[frame%len(frames)] + " " + step
	if total > 0 {
		line += fmt.Sprintf(" (%d/%d)", n, total)
	}
	if d := time.Since(started); d >= 2*time.Second {
		line += fmt.Sprintf(" %ds", int(d.Seconds()))
	}
	fmt.Fprint(out, "\r\033[K"+line)
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
