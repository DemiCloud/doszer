package gui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/DemiCloud/doszer/internal/runlog"
)

// dialog is a modal message, question, or a doszer command's live
// output. Every action the window takes on archives is a doszer command,
// run as a process of its own and shown with its command line, so the
// command line can always do what the window does - but deleting files
// (libraryactions.go), which needs no doszer.
type dialog struct {
	title   string
	lines   []string // the message, or the command line above the output
	buttons []dialogButton
	job     *job
	scroll  int    // output lines scrolled up from the end
	seen    int    // output lines last drawn: a scrolled-up view holds still as more come
	field   *field // an input below the lines (a file name), or nil
	rows    []dialogRow
	image   *texture // shown above the lines, centred (About)
	imageS  int32    // its size, logical pixels
	onImage func()   // a right click on the image (its menu)
	center  bool     // centre the title and lines
	checks  []dialogCheck
}

// dialogCheck is a checkbox below a dialog's message: an option of the
// action its buttons take (also delete the saves).
type dialogCheck struct {
	label string
	on    *bool
}

// dialogRow is a row of a dialog's table: a name, a value, and a link
// opened with the system's handler when clicked.
type dialogRow struct{ name, value, link string }

type dialogButton struct {
	label string
	style int
	fn    func() // nil: closes the dialog
	keep  bool   // the dialog stays open (Copy, Stop)
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
	session string // runlog.SessionEnv for every step: one flow's runs, listed together
}

// step is a doszer command (args), or, with prompt set, a pause asking
// the user to do something first.
type step struct {
	args   []string
	prompt string
	env    []string // KEY=VALUE added to the command's environment
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
	a.runSession(title, newSession(), steps, after)
}

// runSession is runSteps with the steps' runs logged as session, one
// already begun by an earlier dialog of the same flow.
func (a *app) runSession(title, session string, steps []step, after func(err error)) {
	j := &job{steps: steps, after: after, notify: make(chan error, 1), session: session}
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
	stop := dialogButton{label: "Stop", keep: true, fn: func() {
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
		j.Write([]byte("\n(waiting for you)\n"))
		d.buttons = []dialogButton{stop, {label: "Continue", style: btnAccent, keep: true, fn: func() {
			j.waiting = false
			j.notify <- nil
		}}}
		return
	}
	d.buttons = []dialogButton{a.copyAll(d), stop}
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
	cmd.Env = append(append(os.Environ(), runlog.SessionEnv+"="+j.session), st.env...)
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
	d.buttons = []dialogButton{a.copyAll(d), {label: "Close", style: btnAccent}}
	if j.after != nil {
		j.after(j.err)
	}
	a.readLogs() // the command may have recorded a run
	a.rescanStorage()
	a.dirty = true
}

// newSession names a new flow's runs, as runlog names a session: when
// it started.
func newSession() string { return runlog.NewSession(time.Now()) }

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
		// Enter is the main button (never Stop).
		for i := len(d.buttons) - 1; i >= 0; i-- {
			if d.buttons[i].style == btnAccent {
				a.press(d.buttons[i])
				break
			}
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
	if !b.keep {
		a.dialog = nil
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
	if d.job == nil && len(d.rows) == 0 {
		w = min(w, a.px(560))
	} else if d.job == nil {
		w = min(w, a.px(720))
	}
	lh := a.lineHeight(13, false) + a.px(4)
	mono := a.lineHeight(12, false) + a.px(3)
	// A message wraps; a command line is one line, shortened.
	// Messages and command lines wrap (a command line to three lines).
	var lines []string
	for _, l := range d.lines {
		wl := a.wrapAny(l, w-2*a.px(22), 13)
		if d.job != nil && !d.job.waiting && len(wl) > 3 {
			wl = append(wl[:2], a.ellipsize(strings.Join(wl[2:], ""), w-2*a.px(22), 13, false))
		}
		lines = append(lines, wl...)
	}
	h := p*2 + a.lineHeight(17, true) + a.px(12) + int32(len(lines))*lh + a.px(48)
	if d.field != nil {
		h += a.px(44)
	}
	h += int32(len(d.checks)) * a.px(36)
	if len(d.checks) > 0 {
		h += a.px(8)
	}
	if d.image != nil {
		h += a.px(float64(d.imageS) + 16)
	}
	rowH := a.lineHeight(13, false) + a.px(10)
	h += int32(len(d.rows)) * rowH
	if len(d.rows) > 0 {
		h += a.px(8)
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
	if d.image != nil {
		is := a.px(float64(d.imageS))
		ix := x + (w-is)/2
		a.image(d.image, ix, ty, is)
		if d.onImage != nil {
			on := d.onImage
			a.onClick(ix, ty, is, is, func(bool) { a.menuAt = on })
		}
		ty += is + a.px(16)
	}
	title := a.ellipsize(d.title, w-2*p, 17, true)
	if d.center {
		a.text(title, x+(w-a.textWidth(title, 17, true))/2, ty, 17, true, colText)
	} else {
		a.text(title, x+p, ty, 17, true, colText)
	}
	ty += a.lineHeight(17, true) + a.px(12)
	a.selRegion("lines", lines)
	for li, l := range lines {
		c := colMuted
		switch {
		case d.job != nil && d.job.waiting:
			c = colShell // what to do
		case d.job != nil:
			c = colAccent // the command line
		}
		l = a.ellipsize(l, w-2*p, 13, false)
		lx := x + p
		if d.center {
			lx = x + (w-a.textWidth(l, 13, false))/2
		}
		a.selTextLine("lines", li, x+p-a.px(4), ty, w-2*p+a.px(8), lh, l, lx, faceKey{px: int(a.px(13))})
		a.text(l, lx, ty, 13, false, c)
		ty += lh
	}

	if d.field != nil {
		a.drawField(d.field, x+p, ty+a.px(4), w-2*p)
	}
	if len(d.checks) > 0 {
		cy := ty + a.px(8)
		if d.field != nil {
			cy += a.px(44)
		}
		for _, c := range d.checks {
			on := c.on
			a.checkbox(x+p, cy, *on, a.ellipsize(c.label, w-2*p-a.px(36), 13, false), func() { *on = !*on })
			cy += a.px(36)
		}
	}
	if len(d.rows) > 0 {
		c1, c2 := int32(0), int32(0)
		for _, r := range d.rows {
			c1 = max(c1, a.textWidth(r.name, 13, true))
			c2 = max(c2, a.textWidth(r.value, 13, false))
		}
		ty += a.px(4)
		var rowText []string
		for _, r := range d.rows {
			rowText = append(rowText, strings.TrimSpace(r.name+"  "+r.value+"  "+r.link))
		}
		a.selRegion("rows", rowText)
		for ri, r := range d.rows {
			a.selLine("rows", ri, x+p-a.px(4), ty-a.px(4), c1+c2+a.px(36), rowH)
			a.text(r.name, x+p, ty, 13, true, colText)
			a.text(r.value, x+p+c1+a.px(20), ty, 13, false, colMuted)
			if r.link != "" {
				lx := x + p + c1 + c2 + a.px(40)
				link := a.ellipsize(r.link, x+w-p-lx, 13, false)
				lw := a.textWidth(link, 13, false)
				lc := colAccent
				if canOpen() && a.hovered(lx, ty, lw, rowH) {
					lc = colText
				}
				a.text(link, lx, ty, 13, false, lc)
				a.fill(lx, ty+a.lineHeight(13, false)-a.px(1), lw, a.px(1), lc)
				if canOpen() {
					url := r.link
					a.onClick(lx, ty, lw, rowH, func(bool) { a.open(url) })
				}
			}
			ty += rowH
		}
	}

	if d.job != nil {
		// The output, newest at the bottom.
		bx, by, bw := x+p, ty+a.px(4), w-2*p
		bh := y + h - p - a.px(44) - by
		a.round(bx, by, bw, bh, a.px(6), colBackground)
		// Long output lines wrap at the box's width, by character.
		cols := max(20, int((bw-a.px(20))/a.monoWidth()))
		var wrapped []string
		for _, l := range out {
			r := []rune(l)
			for len(r) > cols {
				wrapped = append(wrapped, string(r[:cols]))
				r = r[cols:]
			}
			wrapped = append(wrapped, string(r))
		}
		out = wrapped
		rows := max(1, int((bh-a.px(12))/mono))
		if d.scroll > 0 && len(out) > d.seen && d.seen > 0 {
			d.scroll += len(out) - d.seen
		}
		d.seen = len(out)
		d.scroll = min(d.scroll, max(0, len(out)-rows))
		end := len(out) - d.scroll
		start := max(0, end-rows)
		a.scrollbar(bx+bw-a.px(8), by+a.px(4), bh-a.px(8), start, rows, len(out))
		a.pushClip(bx, by, bw, bh)
		ly := by + a.px(6)
		a.selRegion("output", out)
		for oi, l := range out[start:end] {
			a.selTextLine("output", start+oi, bx+a.px(4), ly, bw-a.px(8), mono, l, bx+a.px(10), faceKey{px: int(a.px(12)), mono: true})
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

// File kinds pickFile asks for.
const (
	fileDisc    = iota // a CUE sheet or a zip of one
	fileArchive        // a .dosz or .dosp
	fileIcon           // an ICO file, or a Windows program to take one from
)

// copyAll is a job dialog's Copy button: its command lines and output,
// to paste when asking for help.
func (a *app) copyAll(d *dialog) dialogButton {
	return dialogButton{label: "Copy", keep: true, fn: func() {
		a.copyText(d.title + "\n" + strings.Join(d.job.lines(), "\n"))
	}}
}
