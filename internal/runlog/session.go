package runlog

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// SessionFile is a session folder's index: what its runs did, together.
const SessionFile = "session.json"

// The logs directory holds a folder per session - the commands one of
// the window's flows ran, or one command run on its own - named by when
// it started, so the folders sort by date; in it, the index and a folder
// per run, numbered in order:
//
//	20261127-123432.519/
//	  session.json
//	  01-dump/     report.json, transcript.log, redumper.log
//	  02-init/     report.json, transcript.log, DOSZ.TOML.after
//	  ...

// Session is a session folder's index, rewritten by each of its runs.
type Session struct {
	ID      string       `json:"id"`
	Name    string       `json:"name,omitempty"` // the game's, as its newest named run says
	Source  string       `json:"source,omitempty"`
	Archive string       `json:"archive,omitempty"`
	Started time.Time    `json:"started"`
	Result  string       `json:"result"`  // its last run's
	Seconds float64      `json:"seconds"` // its runs', together: not the time between them
	Runs    []SessionRun `json:"runs"`
}

// SessionRun is one of a session's runs.
type SessionRun struct {
	ID       string    `json:"id"`
	Command  string    `json:"command"`
	Started  time.Time `json:"started"`
	Seconds  float64   `json:"seconds"`
	Result   string    `json:"result"`
	Warnings int       `json:"warnings,omitempty"`
}

// sessionRuns reads a session folder's runs, in order.
func sessionRuns(dir string) []*Report {
	ents, _ := os.ReadDir(dir)
	var out []*Report
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		if r, err := Load(filepath.Join(dir, e.Name())); err == nil {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Started.Before(out[j].Started) })
	return out
}

// writeSession rewrites a session folder's index from its runs.
func writeSession(dir string) error {
	runs := sessionRuns(dir)
	if len(runs) == 0 {
		return nil
	}
	s := Session{ID: filepath.Base(dir), Started: runs[0].Started, Result: runs[len(runs)-1].Result}
	for _, r := range runs {
		s.Runs = append(s.Runs, SessionRun{ID: r.ID, Command: r.Command, Started: r.Started, Seconds: r.Seconds, Result: r.Result, Warnings: len(r.Warnings)})
		s.Seconds += r.Seconds
		if r.Name != "" {
			s.Name = r.Name
		}
		if r.Source != "" {
			s.Source = r.Source
		}
		if r.Archive != "" {
			s.Archive = r.Archive
		}
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(filepath.Join(dir, SessionFile), append(data, '\n'))
}

// removeOrIndex removes a session folder its last run left, else
// rewrites its index.
func removeOrIndex(dir string) error {
	if len(sessionRuns(dir)) == 0 {
		return os.RemoveAll(dir)
	}
	return writeSession(dir)
}

// writeFile writes data to p through a temporary file, so a reader never
// sees half of it.
func writeFile(p string, data []byte) error {
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// LoadSession reads a session folder's index.
func LoadSession(dir string) (*Session, error) {
	data, err := os.ReadFile(filepath.Join(dir, SessionFile))
	if err != nil {
		return nil, err
	}
	var s Session
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// List reads every run under the logs directory dir, newest first. A
// folder without a readable report is skipped; a missing dir is no
// runs.
func List(dir string) ([]*Report, error) {
	ents, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*Report
	for _, e := range ents {
		if e.IsDir() {
			out = append(out, sessionRuns(filepath.Join(dir, e.Name()))...)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].Started.Equal(out[j].Started) {
			return out[i].Started.After(out[j].Started)
		}
		return out[i].ID > out[j].ID
	})
	return out, nil
}

// Delete removes runs from the logs directory dir - their folders,
// and a session's folder once it has none left - and returns how many
// it removed. It removes nothing outside dir.
func Delete(dir string, runs []*Report) (int, error) {
	n := 0
	sessions := map[string]bool{}
	for _, r := range runs {
		f := r.Folder()
		s := filepath.Dir(f)
		if rel, err := filepath.Rel(dir, f); err != nil || filepath.Dir(filepath.Dir(rel)) != "." || filepath.Dir(rel) == "." {
			return n, fmt.Errorf("%s: not a run folder in %s", f, dir)
		}
		if _, err := os.Stat(f); err != nil {
			continue // already gone: another of runs was its session
		}
		if err := os.RemoveAll(f); err != nil {
			return n, err
		}
		sessions[s] = true
		n++
	}
	for s := range sessions {
		if err := removeOrIndex(s); err != nil {
			return n, err
		}
	}
	return n, nil
}
