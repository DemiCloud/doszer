package runlog

import (
	"archive/zip"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DemiCloud/doszer/internal/status"
)

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
