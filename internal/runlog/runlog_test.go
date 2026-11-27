package runlog

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DemiCloud/doszer/internal/redact"
	"github.com/DemiCloud/doszer/internal/status"
)

func redactFor() *redact.Redactor { return redact.For("/home/alice", "alice", "") }

// quiet points the process's standard output and error at a file for
// the test, so what Stdout and Stderr pass through isn't printed.
func quiet(t *testing.T) {
	t.Helper()
	f, err := os.Create(filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatal(err)
	}
	so, se := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = f, f
	t.Cleanup(func() { os.Stdout, os.Stderr = so, se; f.Close(); reset() })
}

func TestRecord(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		result string
	}{
		{"ok", nil, ResultOK},
		{"failed", errors.New("boom"), ResultFailed},
		{"interrupted", errInterrupted, ResultInterrupted},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			quiet(t)
			t.Setenv(SessionEnv, "S1")
			logs := t.TempDir()
			if err := Start(logs, "build", []string{"--flatten", "My Game"}, map[string]string{"doszer": "test"}); err != nil {
				t.Fatal(err)
			}
			fmt.Fprint(Stdout, "half ")
			fmt.Fprintln(Stdout, "a line")
			fmt.Fprint(Stdout, "10%\r50%\r100%\n")
			fmt.Fprintln(Stderr, "[WARN] something odd")
			status.Step("Writing the archive")
			status.Done()
			Decide("boot model", "os", "programs need Windows 95")
			Logf("detail")
			Attach("DOSZ.TOML", []byte("name = 'x'\n"))
			Update(func(r *Report) { r.Name = "My Game" })
			Finish(tt.err)
			fmt.Fprintln(Stdout, "after the end")

			runs, err := List(logs)
			if err != nil || len(runs) != 1 {
				t.Fatalf("List: %v, %d runs", err, len(runs))
			}
			r := runs[0]
			if r.Result != tt.result || r.Command != "build" || r.Session != "S1" || r.Name != "My Game" {
				t.Errorf("report: %+v", r)
			}
			if tt.err != nil && tt.result == ResultFailed && r.Error != "boom" {
				t.Errorf("error %q", r.Error)
			}
			if len(r.Warnings) != 1 || r.Warnings[0] != "something odd" {
				t.Errorf("warnings %q", r.Warnings)
			}
			if len(r.Steps) != 1 || r.Steps[0].Name != "Writing the archive" || !r.Steps[0].OK {
				t.Errorf("steps %+v", r.Steps)
			}
			if len(r.Decisions) != 1 || r.Decisions[0].Choice != "os" {
				t.Errorf("decisions %+v", r.Decisions)
			}
			if len(r.Files) != 1 || r.Files[0] != "DOSZ.TOML" {
				t.Errorf("files %q", r.Files)
			}
			if got := r.CommandLine(); got != "doszer build --flatten 'My Game'" {
				t.Errorf("command line %q", got)
			}
			data, err := os.ReadFile(filepath.Join(r.Folder(), TranscriptFile))
			if err != nil {
				t.Fatal(err)
			}
			tr := string(data)
			for _, want := range []string{"out  half a line\n", "out  100%\n", "err  [WARN] something odd\n", "step Writing the archive: done", "why  boot model: os (programs need Windows 95)", "log  detail", "# " + tt.result} {
				if !strings.Contains(tr, want) {
					t.Errorf("transcript lacks %q:\n%s", want, tr)
				}
			}
			if strings.Contains(tr, "after the end") || strings.Contains(tr, "50%") {
				t.Errorf("transcript:\n%s", tr)
			}
		})
	}
}

func TestFind(t *testing.T) {
	at := func(m int) time.Time { return time.Date(2026, 1, 1, 0, m, 0, 0, time.UTC) }
	src, _ := filepath.Abs("games/a")
	runs := []*Report{ // newest first, as List gives them
		{ID: "4-build", Started: at(4), Source: src, Archive: "/roms/a.dosz"},
		{ID: "3-start", Started: at(3), Session: "S", Source: src},
		{ID: "2-init", Started: at(2), Session: "S", Source: src},
		{ID: "1-build", Started: at(1), Source: "/other"},
	}
	ids := func(rs []*Report) string {
		var s []string
		for _, r := range rs {
			s = append(s, r.ID)
		}
		return strings.Join(s, ",")
	}
	tests := []struct{ arg, want string }{
		{"4-build", "4-build"},
		{"S", "2-init,3-start"},
		{"games/a", "2-init,3-start,4-build"},
		{"3", "3-start"},
		{"x", ""},
	}
	for _, tt := range tests {
		if got := ids(Find(runs, tt.arg)); got != tt.want {
			t.Errorf("Find(%q) = %s, want %s", tt.arg, got, tt.want)
		}
	}
	if got := ids(History(runs, runs[0])); got != "2-init,3-start,4-build" {
		t.Errorf("History = %s", got)
	}
	if got := Latest(runs, "/roms/a.dosz"); got != runs[0] {
		t.Errorf("Latest = %v", got)
	}
}

func TestReadArchive(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.dosz")
	f, _ := os.Create(p)
	zw := zip.NewWriter(f)
	for _, e := range []struct {
		name   string
		method uint16
	}{{"STORED.BIN", zip.Store}, {"TEXT.TXT", zip.Deflate}} {
		w, _ := zw.CreateHeader(&zip.FileHeader{Name: e.name, Method: e.method})
		w.Write([]byte(strings.Repeat("doszer ", 1000)))
	}
	zw.Close()
	f.Close()
	a, err := ReadArchive(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Entries) != 2 || a.Entries[0].Method != "store" || a.Entries[1].Method != "deflate" {
		t.Fatalf("entries %+v", a.Entries)
	}
	if a.Content != 14000 || a.Compressed >= a.Content || len(a.SHA1) != 40 || a.Ratio() <= 0 || a.Ratio() >= 1 {
		t.Errorf("archive %+v", a)
	}
}

func TestExport(t *testing.T) {
	quiet(t)
	logs := t.TempDir()
	if err := Start(logs, "build", []string{"/home/alice/src", "out.dosz"}, nil); err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(Stdout, "Built /home/alice/out.dosz")
	Decide("boot model", "dos", "programs need DOS")
	Attach("DOSZ.TOML.before", []byte("# by alice\n"))
	Finish(nil)
	runs, _ := List(logs)

	var buf strings.Builder
	if err := Export(&buf, runs, redactFor(), map[string][]byte{"env.json": []byte(`{"home":"/home/alice"}`)}); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(strings.NewReader(buf.String()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		var b strings.Builder
		_, _ = io.Copy(&b, rc)
		rc.Close()
		files[f.Name] = b.String()
	}
	id := runs[0].ID
	for _, name := range []string{"commands.txt", "env.json", "runs/" + id + "/" + ReportFile, "runs/" + id + "/" + TranscriptFile, "runs/" + id + "/DOSZ.TOML.before"} {
		data, ok := files[name]
		if !ok {
			t.Errorf("no %s in %v", name, files)
			continue
		}
		if strings.Contains(strings.ToLower(data), "alice") {
			t.Errorf("%s not redacted:\n%s", name, data)
		}
	}
	if c := files["commands.txt"]; !strings.Contains(c, "doszer build ~/src out.dosz") || !strings.Contains(c, "#   chose boot model: dos (programs need DOS)") {
		t.Errorf("commands.txt:\n%s", c)
	}
}

// TestSessionLayout: a session's runs are numbered folders in its
// folder, indexed; a run on its own is a session of one; deleting a
// session's last run removes its folder.
func TestSessionLayout(t *testing.T) {
	quiet(t)
	logs := t.TempDir()
	run := func(session, command string, err error) {
		t.Setenv(SessionEnv, session)
		if err := Start(logs, command, nil, nil); err != nil {
			t.Fatal(err)
		}
		Update(func(r *Report) { r.Name = "Example Game" })
		Finish(err)
		reset()
	}
	run("20261127-120000.000", "dump", nil)
	run("20261127-120000.000", "dump", nil)
	run("20261127-120000.000", "build", errors.New("boom"))
	run("", "repack", nil)

	for _, p := range []string{
		"20261127-120000.000/" + SessionFile,
		"20261127-120000.000/01-dump/" + ReportFile,
		"20261127-120000.000/02-dump/" + TranscriptFile,
		"20261127-120000.000/03-build/" + ReportFile,
	} {
		if _, err := os.Stat(filepath.Join(logs, p)); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
	s, err := LoadSession(filepath.Join(logs, "20261127-120000.000"))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Runs) != 3 || s.Result != ResultFailed || s.Name != "Example Game" || s.Runs[2].ID != "20261127-120000.000/03-build" {
		t.Errorf("index %+v", s)
	}
	runs, _ := List(logs)
	if len(runs) != 4 || runs[0].Command != "repack" || runs[0].Session == "20261127-120000.000" || runs[0].Session == "" {
		t.Fatalf("runs %+v", runs)
	}
	lone := runs[0]

	// Deleting one of a session's runs re-indexes it; its last, removes it.
	if n, err := Delete(logs, Find(runs, "20261127-120000.000/02-dump")); n != 1 || err != nil {
		t.Fatalf("delete one: %d, %v", n, err)
	}
	if s, _ := LoadSession(filepath.Join(logs, "20261127-120000.000")); s == nil || len(s.Runs) != 2 {
		t.Errorf("after deleting one: %+v", s)
	}
	if n, err := Delete(logs, []*Report{lone}); n != 1 || err != nil {
		t.Fatalf("delete lone: %d, %v", n, err)
	}
	if _, err := os.Stat(filepath.Join(logs, lone.Session)); !os.IsNotExist(err) {
		t.Errorf("session folder left: %v", err)
	}
	if _, err := Delete(logs, []*Report{{dir: filepath.Join(logs, "..", "elsewhere", "x")}}); err == nil {
		t.Error("deleted outside the logs")
	}
}
