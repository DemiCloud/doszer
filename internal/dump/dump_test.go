package dump

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeRedumper stands in for redumper, in its working directory: `disc`
// writes <image-name>.log and .state and then, unless $FAKE_FAIL or
// $FAKE_SPLIT_ERRORS is set, a CUE sheet naming two tracks, the tracks and
// a scratch .scram; with $FAKE_SPLIT_ERRORS it logs unreadable samples and
// refuses to split, which `split --force-split` then does. Every command
// line is appended to $FAKE_ARGS.
func fakeRedumper(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	script := filepath.Join(t.TempDir(), "redumper")
	os.WriteFile(script, []byte(`#!/bin/sh
echo "$*" >> "$FAKE_ARGS"
for a; do case "$a" in --image-name=*) n=${a#--image-name=};; esac; done
split() {
	printf 'FILE "%s (Track 1).bin" BINARY\n  TRACK 01 MODE1/2352\n    INDEX 01 00:00:00\nFILE "%s (Track 2).bin" BINARY\n  TRACK 02 AUDIO\n    INDEX 01 00:00:00\n' "$n" "$n" > "$n.cue"
	echo data > "$n (Track 1).bin"
	echo audio > "$n (Track 2).bin"
}
case "$1" in
disc)
	echo "dumping $n" >> "$n.log"
	echo state > "$n.state"
	echo scram > "$n.scram"
	[ -z "$FAKE_FAIL" ] || exit 1
	if [ -n "$FAKE_SPLIT_ERRORS" ]; then
		echo "errors detected, track: 2, sectors: {SKIP: 1, C2: 0}, samples: {SKIP: 6, C2: 0}" >> "$n.log"
		echo "error: data errors detected, unable to continue" >> "$n.log"
		exit 1
	fi
	split;;
split)
	case "$*" in *--force-split*) split;; *) exit 1;; esac;;
esac
`), 0o755)
	old := redumper
	redumper = func(bool) (string, error) { return script, nil }
	t.Cleanup(func() { redumper = old })
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("FAKE_ARGS", filepath.Join(t.TempDir(), "args"))
}

func names(t *testing.T, dir string) []string {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func TestRun(t *testing.T) {
	fakeRedumper(t)
	parent := t.TempDir()
	dir := filepath.Join(parent, "Example Game")
	for _, want := range []string{"disc1", "disc2"} {
		res, err := Run(Options{Dir: dir})
		if err != nil {
			t.Fatal(err)
		}
		if res.Cue != filepath.Join(dir, want+".cue") || len(res.Tracks) != 2 || res.Errors != nil {
			t.Fatalf("result %+v", res)
		}
		if b, _ := os.ReadFile(res.Log); string(b) != "dumping "+want+"\n" {
			t.Fatalf("log %s: %q", res.Log, b)
		}
	}
	// Only the sheets and their tracks; redumper's work directory is gone.
	if got := names(t, dir); len(got) != 6 {
		t.Fatalf("source holds %v", got)
	}
	if got := names(t, parent); len(got) != 1 {
		t.Fatalf("beside the source: %v", got)
	}
}

// A failed dump keeps redumper's files beside the source, and running
// again resumes it under the same name with refine.
func TestRunResumes(t *testing.T) {
	fakeRedumper(t)
	parent := t.TempDir()
	dir := filepath.Join(parent, "Example Game")
	t.Setenv("FAKE_FAIL", "1")
	res, err := Run(Options{Dir: dir})
	if err == nil || !strings.Contains(err.Error(), "resume") {
		t.Fatalf("failure: %v", err)
	}
	if res.Log == "" {
		t.Fatal("log not kept")
	}
	if got := names(t, dir); len(got) != 0 {
		t.Fatalf("source holds %v", got)
	}
	work := filepath.Join(parent, ".Example Game.doszer-dump")
	if _, err := os.Stat(filepath.Join(work, "disc1.state")); err != nil {
		t.Fatal("dump state not kept for resuming")
	}

	t.Setenv("FAKE_FAIL", "")
	if res, err = Run(Options{Dir: dir}); err != nil || res.Cue != filepath.Join(dir, "disc1.cue") {
		t.Fatalf("%+v, %v", res, err)
	}
	args, _ := os.ReadFile(os.Getenv("FAKE_ARGS"))
	if lines := strings.Split(strings.TrimSpace(string(args)), "\n"); len(lines) != 2 || !strings.Contains(lines[1], "--continue=refine") {
		t.Fatalf("redumper runs: %q", lines)
	}
	if _, err := os.Stat(work); err == nil {
		t.Fatal("work directory left after the dump completed")
	}
}

// Unreadable samples block redumper's split; the dump is split anyway,
// keeping what was read, and the errors are reported.
func TestRunSplitsDespiteSampleErrors(t *testing.T) {
	fakeRedumper(t)
	t.Setenv("FAKE_SPLIT_ERRORS", "1")
	dir := filepath.Join(t.TempDir(), "Example Game")
	res, err := Run(Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 1 || !strings.Contains(res.Errors[0], "SKIP: 6") || len(res.Tracks) != 2 {
		t.Fatalf("result %+v", res)
	}
	args, _ := os.ReadFile(os.Getenv("FAKE_ARGS"))
	if !strings.Contains(string(args), "split --force-split --leave-unchanged") {
		t.Fatalf("redumper runs: %s", args)
	}
}
