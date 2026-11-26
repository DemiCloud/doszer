package gui

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
)

// dialog is a modal message, question, or a doszer command's live
// output. Every action the window takes on archives is a doszer command,
// run as a process of its own and shown with its command line, so the
// command line can always do what the window does.
type dialog struct {
	title   string
	lines   []string // the message, or the command line above the output
	buttons []dialogButton
	job     *job
	scroll  int    // output lines scrolled up from the end
	field   *field // an input below the lines (a file name), or nil
}

type dialogButton struct {
	label string
	style int
	fn    func() // nil: closes the dialog
}

// job runs steps one after the other: doszer commands, each its own
// process, or pauses for the user (insert the next disc).
type job struct {
	steps   []step
	cur     int // the step running
	cmd     *exec.Cmd
	mu      sync.Mutex
	out     []byte
	done    bool
	waiting bool // at a pause step, for Continue
	err     error
	after   func(err error) // on the window's thread, once done
	notify  chan error
	stopped bool
}

// step is a doszer command (args), or, with prompt set, a pause asking
// the user to do something first.
type step struct {
	args   []string
	prompt string
}

func (j *job) Write(p []byte) (int, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.out = append(j.out, p...)
	if len(j.out) > 1<<20 {
		j.out = j.out[len(j.out)-1<<20:]
	}
	return len(p), nil
}

// lines returns the output so far, a line each.
func (j *job) lines() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	s := strings.ReplaceAll(string(j.out), "\r\n", "\n")
	// A progress line rewritten with \r keeps only its last state.
	var out []string
	for _, l := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		if i := strings.LastIndexByte(l, '\r'); i >= 0 {
			l = l[i+1:]
		}
		out = append(out, strings.ReplaceAll(l, "\t", "    "))
	}
	return out
}

// alert opens a dialog with a message and an OK button.
func (a *app) alert(title string, lines ...string) {
	a.dialog = &dialog{title: title, lines: lines, buttons: []dialogButton{{label: "OK", style: btnAccent}}}
}

// confirm asks before doing fn.
func (a *app) confirm(title, action string, fn func(), lines ...string) {
	a.dialog = &dialog{title: title, lines: lines, buttons: []dialogButton{
		{label: "Cancel"},
		{label: action, style: btnAccent, fn: fn},
	}}
}

// runDoszer runs `doszer args...` and shows its output; after, if set,
// runs once it ends (rescan, ...).
func (a *app) runDoszer(title string, args []string, after func(err error)) {
	a.runSteps(title, []step{{args: args}}, after)
}

// runSteps runs steps in one dialog, stopping at the first that fails.
func (a *app) runSteps(title string, steps []step, after func(err error)) {
	j := &job{steps: steps, after: after, notify: make(chan error, 1)}
	d := &dialog{title: title, job: j}
	a.dialog = d
	a.jobs = append(a.jobs, d)
	a.startStep(d)
}

// startStep starts d's current step, or ends the job past the last.
func (a *app) startStep(d *dialog) {
	j := d.job
	if j.cur >= len(j.steps) {
		a.jobDone(d)
		return
	}
	st := j.steps[j.cur]
	stop := dialogButton{label: "Stop", fn: func() {
		if j.done {
			return
		}
		j.stopped = true
		if j.cmd != nil && j.cmd.Process != nil {
			j.cmd.Process.Kill()
		} else {
			j.notify <- errors.New("stopped")
		}
	}}
	if st.prompt != "" {
		j.waiting = true
		d.lines = []string{st.prompt}
		j.Write([]byte("\n" + st.prompt + "\n"))
		d.buttons = []dialogButton{stop, {label: "Continue", style: btnAccent, fn: func() {
			j.waiting = false
			j.notify <- nil
		}}}
		return
	}
	d.buttons = []dialogButton{stop}
	exe, err := a.exe()
	if err != nil {
		j.notify <- err
		return
	}
	line := commandLine(st.args)
	d.lines = []string{line}
	if len(j.steps) > 1 {
		j.Write([]byte("\n$ " + line + "\n"))
	}
	cmd := exec.Command(exe, st.args...)
	hideConsole(cmd)
	cmd.Stdout, cmd.Stderr = j, j
	j.cmd = cmd
	if err := cmd.Start(); err != nil {
		j.Write([]byte(err.Error() + "\n"))
		j.notify <- err
		return
	}
	go func() { j.notify <- cmd.Wait() }()
}

// pollJobs moves the jobs on whose step ended.
func (a *app) pollJobs() {
	keep := a.jobs[:0]
	for _, d := range a.jobs {
		j := d.job
		select {
		case err := <-j.notify:
			j.cmd = nil
			if err != nil || j.stopped {
				j.err = err
				a.jobDone(d)
				continue
			}
			j.cur++
			a.startStep(d)
			if !j.done {
				keep = append(keep, d)
			}
		default:
			keep = append(keep, d)
		}
	}
	a.jobs = keep
}

func (a *app) jobDone(d *dialog) {
	j := d.job
	if j.done {
		return
	}
	j.done, j.waiting = true, false
	if j.stopped {
		j.err = errors.New("stopped")
	}
	d.buttons = []dialogButton{{label: "Close", style: btnAccent}}
	if j.after != nil {
		j.after(j.err)
	}
	a.dirty = true
}

// commandLine is args as one would type them.
func commandLine(args []string) string {
	parts := []string{"doszer"}
	for _, s := range args {
		if s == "" || strings.ContainsAny(s, " '\"&()") {
			s = `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, " ")
}

func (a *app) dialogKey(sc int32) {
	d := a.dialog
	switch sc {
	case scEscape:
		if d.job == nil || d.job.done {
			a.dialog = nil
		}
	case scReturn, scKPEnter:
		if len(d.buttons) > 0 {
			a.press(d.buttons[len(d.buttons)-1])
		}
	case scUp:
		d.scroll++
	case scDown:
		d.scroll = max(0, d.scroll-1)
	case scPageUp:
		d.scroll += 10
	case scPageDown:
		d.scroll = max(0, d.scroll-10)
	}
}

func (a *app) press(b dialogButton) {
	d := a.dialog
	if b.fn == nil || d.job == nil || d.job.done {
		a.dialog = nil // a dialog's buttons close it; Stop keeps it open
	}
	if b.fn != nil {
		b.fn()
	}
}

func (a *app) drawDialog() {
	d := a.dialog
	if d == nil {
		return
	}
	a.layer = layerModal
	defer func() { a.layer = layerPage }()
	a.fill(0, 0, a.w, a.h, rgba{0, 0, 0, 160})
	a.onClick(0, 0, a.w, a.h, func(bool) {})

	p := a.px(22)
	w := min(a.w-a.px(40), a.px(820))
	if d.job == nil {
		w = min(w, a.px(560))
	}
	lh := a.lineHeight(13, false) + a.px(4)
	mono := a.lineHeight(12, false) + a.px(3)
	h := p*2 + a.lineHeight(17, true) + a.px(12) + int32(len(d.lines))*lh + a.px(48)
	if d.field != nil {
		h += a.px(44)
	}
	var out []string
	if d.job != nil {
		out = d.job.lines()
		h = min(a.h-a.px(40), a.px(560))
	}
	x, y := (a.w-w)/2, (a.h-h)/2
	a.round(x-a.px(1), y-a.px(1), w+a.px(2), h+a.px(2), a.px(12), colLine)
	a.round(x, y, w, h, a.px(12), colPopup)
	ty := y + p
	a.text(a.ellipsize(d.title, w-2*p, 17, true), x+p, ty, 17, true, colText)
	ty += a.lineHeight(17, true) + a.px(12)
	for _, l := range d.lines {
		c := colMuted
		if d.job != nil {
			c = colAccent // the command line
		}
		a.text(a.ellipsize(l, w-2*p, 13, false), x+p, ty, 13, false, c)
		ty += lh
	}

	if d.field != nil {
		a.drawField(d.field, x+p, ty+a.px(4), w-2*p)
	}

	if d.job != nil {
		// The output, newest at the bottom.
		bx, by, bw := x+p, ty+a.px(4), w-2*p
		bh := y + h - p - a.px(44) - by
		a.round(bx, by, bw, bh, a.px(6), colBackground)
		rows := max(1, int((bh-a.px(12))/mono))
		d.scroll = min(d.scroll, max(0, len(out)-rows))
		end := len(out) - d.scroll
		start := max(0, end-rows)
		a.pushClip(bx, by, bw, bh)
		ly := by + a.px(6)
		for _, l := range out[start:end] {
			c := colText
			if strings.Contains(l, "ERROR:") || strings.HasPrefix(l, "[WARN]") {
				c = colError
			}
			a.monoText(l, bx+a.px(10), ly, c)
			ly += mono
		}
		a.popClip()
		status, sc := "Running"+spinner(a.frame), colAccent
		if n := len(d.job.steps); n > 1 && !d.job.done {
			status = fmt.Sprintf("Step %d of %d: %s", min(d.job.cur+1, n), n, status)
		}
		if d.job.waiting {
			status, sc = "Waiting for you", colShell
		}
		if d.job.done {
			status, sc = "Done", colDOS
			if d.job.err != nil {
				status, sc = "Failed: "+d.job.err.Error(), colError
			}
		}
		a.text(status, x+p, y+h-p-a.px(25), 13, false, sc)
	}

	bx := x + w - p
	for i := len(d.buttons) - 1; i >= 0; i-- {
		b := d.buttons[i]
		bw := a.buttonW(b.label)
		bx -= bw
		a.button(b.label, bx, y+h-p-a.px(32), b.style, func() { a.press(b) })
		bx -= a.px(8)
	}
}

// drive is an optical drive.
type drive struct {
	path  string // E: or /dev/sg1, as dump --drive takes it
	label string // the disc's label (Windows) or the drive's model (Linux)
	disc  bool   // a disc is in (Windows; Linux can't tell)
}
