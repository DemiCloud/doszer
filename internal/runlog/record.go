package runlog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/DemiCloud/doszer/internal/config"
	"github.com/DemiCloud/doszer/internal/interrupt"
	"github.com/DemiCloud/doszer/internal/status"
)

// Stdout and Stderr are what doszer prints through: the process's own
// standard output and error, and, while a run is recorded, its
// transcript, a timestamped line each. Writing is synchronous, so the
// terminal sees output exactly as it would without a recorder.
var (
	Stdout io.Writer = &stream{tag: "out", file: func() *os.File { return os.Stdout }}
	Stderr io.Writer = &stream{tag: "err", file: func() *os.File { return os.Stderr }}
)

type stream struct {
	tag     string
	file    func() *os.File
	partial []byte // an unfinished line, under rec.mu
}

func (s *stream) Write(p []byte) (int, error) {
	n, err := s.file().Write(p)
	mu.Lock()
	if rec != nil && !rec.done {
		rec.lines(s, p[:n])
	}
	mu.Unlock()
	return n, err
}

var (
	mu  sync.Mutex
	rec *recorder // the run being recorded, or nil
)

type recorder struct {
	r          *Report
	transcript *os.File
	done       bool
}

// Start records the run of command with args into a new folder under
// logsDir, until Finish. versions are the programs' versions to record
// (doszer's, the embedded DOSBox Pure's, ...).
func Start(logsDir, command string, args []string, versions map[string]string) error {
	mu.Lock()
	defer mu.Unlock()
	if rec != nil {
		return errors.New("runlog: already recording")
	}
	now := time.Now()
	session, err := sessionFolder(logsDir, now)
	if err != nil {
		return err
	}
	id, dir, err := runFolder(filepath.Join(logsDir, session), command)
	if err != nil {
		return err
	}
	id = session + "/" + id
	cwd, _ := os.Getwd()
	r := &Report{
		ID: id, Session: session, Command: command, Args: args, Dir: cwd,
		Started: now, Result: ResultRunning, Versions: versions,
		OS: runtime.GOOS + "/" + runtime.GOARCH, dir: dir,
	}
	t, err := os.Create(filepath.Join(dir, TranscriptFile))
	if err != nil {
		os.RemoveAll(dir)
		return err
	}
	fmt.Fprintf(t, "# %s\n# started %s in %s\n", r.CommandLine(), now.Format(time.RFC3339), cwd)
	rec = &recorder{r: r, transcript: t}
	if err := rec.save(); err != nil {
		t.Close()
		os.RemoveAll(dir)
		rec = nil
		return err
	}
	status.SetHook(stepDone)
	interrupt.OnExit(func() { Finish(errInterrupted) })
	return nil
}

var errInterrupted = errors.New("interrupted")

// NewSession names a new session: when it started, to the
// millisecond, so session folders sort by date.
func NewSession(t time.Time) string { return t.Format("20060102-150405.000") }

// sessionFolder returns the session the run belongs to: SessionEnv's
// (the window's flow, its folder made by its first run), else a new one
// of its own, made unique if another started at the same moment.
func sessionFolder(logsDir string, t time.Time) (string, error) {
	if s := os.Getenv(SessionEnv); s != "" {
		if s != filepath.Base(s) || s == "." || s == ".." {
			return "", fmt.Errorf("%s=%q: not a session name", SessionEnv, s)
		}
		return s, os.MkdirAll(filepath.Join(logsDir, s), 0o755)
	}
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		return "", err
	}
	base := NewSession(t)
	for i := 0; ; i++ {
		s := base
		if i > 0 {
			s = fmt.Sprintf("%s-%d", base, i+1)
		}
		err := os.Mkdir(filepath.Join(logsDir, s), 0o755)
		if err == nil {
			return s, nil
		}
		if !errors.Is(err, os.ErrExist) || i > 100 {
			return "", err
		}
	}
}

// runFolder makes the run's folder in its session's: its place in the
// session and its command (01-dump, 02-init...), numbered because a
// session can run a command twice (two discs, an install again).
func runFolder(sessionDir, command string) (id, dir string, err error) {
	ents, err := os.ReadDir(sessionDir)
	if err != nil {
		return "", "", err
	}
	n := 0
	for _, e := range ents {
		if e.IsDir() {
			n++
		}
	}
	for i := n + 1; i < n+100; i++ {
		id = fmt.Sprintf("%02d-%s", i, command)
		dir = filepath.Join(sessionDir, id)
		if err = os.Mkdir(dir, 0o755); err == nil {
			return id, dir, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", "", err
		}
	}
	return "", "", err
}

// Active reports whether a run is being recorded.
func Active() bool {
	mu.Lock()
	defer mu.Unlock()
	return rec != nil && !rec.done
}

// Update changes the report being recorded, if any, and saves it.
func Update(f func(r *Report)) {
	mu.Lock()
	defer mu.Unlock()
	if rec == nil || rec.done {
		return
	}
	f(rec.r)
	rec.save()
}

// Decide records a choice doszer made itself, in the report and the
// transcript.
func Decide(what, choice, why string) {
	mu.Lock()
	defer mu.Unlock()
	if rec == nil || rec.done {
		return
	}
	rec.r.Decisions = append(rec.r.Decisions, Decision{What: what, Choice: choice, Why: why})
	line := what + ": " + choice
	if why != "" {
		line += " (" + why + ")"
	}
	rec.write("why", line)
	rec.save()
}

// Logf adds a line to the transcript only: detail a terminal doesn't
// show.
func Logf(format string, args ...any) {
	mu.Lock()
	defer mu.Unlock()
	if rec == nil || rec.done {
		return
	}
	for _, l := range strings.Split(strings.TrimRight(fmt.Sprintf(format, args...), "\n"), "\n") {
		rec.write("log", l)
	}
}

// Attach saves data as name in the run's folder, listed in the report.
func Attach(name string, data []byte) {
	mu.Lock()
	defer mu.Unlock()
	if rec == nil || rec.done {
		return
	}
	name = filepath.Base(name)
	if err := os.WriteFile(filepath.Join(rec.r.dir, name), data, 0o644); err != nil {
		rec.write("log", "attach "+name+": "+err.Error())
		return
	}
	for _, f := range rec.r.Files {
		if f == name {
			return
		}
	}
	rec.r.Files = append(rec.r.Files, name)
	rec.save()
}

// AttachFile attaches the file at path as name; a file that can't be
// read is noted in the transcript.
func AttachFile(name, path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		Logf("attach %s: %v", name, err)
		return
	}
	Attach(name, data)
}

// Finish ends the run: its result from err, its duration, and the
// report saved for the last time. Output after it is no longer
// recorded.
func Finish(err error) {
	mu.Lock()
	defer mu.Unlock()
	if rec == nil || rec.done {
		return
	}
	for _, s := range []*stream{Stdout.(*stream), Stderr.(*stream)} {
		if len(s.partial) > 0 {
			rec.lines(s, []byte("\n"))
		}
	}
	r := rec.r
	r.Seconds = time.Since(r.Started).Seconds()
	switch {
	case errors.Is(err, errInterrupted):
		r.Result = ResultInterrupted
	case err != nil:
		r.Result, r.Error = ResultFailed, err.Error()
	default:
		r.Result = ResultOK
	}
	end := "# " + r.Result
	if r.Error != "" {
		end += ": " + strings.ReplaceAll(r.Error, "\n", "; ")
	}
	fmt.Fprintf(rec.transcript, "%s after %s\n", end, r.Duration().Round(time.Millisecond))
	rec.transcript.Close()
	rec.save()
	rec.done = true
}

// stepDone is status's hook: a step's timing, in the report and the
// transcript. status holds its own lock while calling it.
func stepDone(step string, started time.Time, d time.Duration, ok bool) {
	mu.Lock()
	defer mu.Unlock()
	if rec == nil || rec.done {
		return
	}
	rec.r.Steps = append(rec.r.Steps, Step{Name: step, Started: started, Seconds: d.Seconds(), OK: ok})
	mark := "done"
	if !ok {
		mark = "failed"
	}
	rec.write("step", fmt.Sprintf("%s: %s (%s)", step, mark, d.Round(time.Millisecond)))
}

// lines adds p's complete lines to the transcript, keeping a partial
// line for the next write; of a line rewritten with \r (progress), only
// its last state. A "[WARN] " line is also a warning in the report.
func (rc *recorder) lines(s *stream, p []byte) {
	s.partial = append(s.partial, p...)
	for {
		i := bytes.IndexByte(s.partial, '\n')
		if i < 0 {
			break
		}
		l := strings.TrimSuffix(string(s.partial[:i]), "\r")
		s.partial = s.partial[i+1:]
		if j := strings.LastIndexByte(l, '\r'); j >= 0 {
			l = l[j+1:]
		}
		rc.write(s.tag, l)
		if w, ok := strings.CutPrefix(l, "[WARN] "); ok {
			rc.r.Warnings = append(rc.r.Warnings, w)
		}
	}
}

// write adds one line to the transcript: time, tag, text.
func (rc *recorder) write(tag, line string) {
	fmt.Fprintf(rc.transcript, "%s %-4s %s\n", time.Now().Format("15:04:05.000"), tag, line)
}

// save writes the report, through a temporary file so a reader never
// sees half of it.
func (rc *recorder) save() error {
	data, err := json.MarshalIndent(rc.r, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFile(filepath.Join(rc.r.dir, ReportFile), append(data, '\n')); err != nil {
		return err
	}
	return writeSession(filepath.Dir(rc.r.dir))
}

// SettingsFor records the settings in effect for sourceDir ("" for
// none), with where each came from.
func SettingsFor(sourceDir string) {
	if !Active() {
		return
	}
	s, err := config.Settings(sourceDir)
	if err != nil {
		Logf("settings: %v", err)
		return
	}
	Update(func(r *Report) { r.Settings = s })
}

// reset forgets the recorder, for tests.
func reset() {
	mu.Lock()
	rec = nil
	mu.Unlock()
	status.SetHook(nil)
}

// Discard drops the run being recorded, folder and all: a command that
// turned out to change nothing (set with no change only shows).
func Discard() {
	mu.Lock()
	defer mu.Unlock()
	if rec == nil || rec.done {
		return
	}
	rec.transcript.Close()
	os.RemoveAll(rec.r.dir)
	removeOrIndex(filepath.Dir(rec.r.dir))
	rec.done = true
}
