package build

import (
	"reflect"
	"sort"
	"testing"
)

func newTestSet(paths ...string) *fileSet {
	fs := newFileSet()
	for _, p := range paths {
		fs.set(p, dataEntry([]byte(p), Epoch))
	}
	return fs
}

func TestFlattenDisabled(t *testing.T) {
	fs := newTestSet("A/B/file.txt")
	moved, dropped := flatten(fs, false)
	if moved != nil || dropped != nil {
		t.Fatalf("flatten(disabled) = %v, %v, want nil, nil", moved, dropped)
	}
	if _, ok := fs.get("A/B/file.txt"); !ok {
		t.Fatal("file missing after disabled flatten")
	}
}

func TestFlattenCollapsesWrapperDirs(t *testing.T) {
	// GAMENAME and DATA are both pass-through wrappers down to a leaf
	// directory with multiple files: GAMENAME (only DATA) -> DATA (only
	// RESOURCE) -> RESOURCE (two files). RESOURCE must survive, GAMENAME and
	// DATA must be dropped.
	fs := newTestSet(
		"GAMENAME/DATA/RESOURCE/a.txt",
		"GAMENAME/DATA/RESOURCE/b.txt",
	)

	moved, dropped := flatten(fs, true)

	wantPaths := []string{"RESOURCE/a.txt", "RESOURCE/b.txt"}
	gotPaths := fs.paths()
	sort.Strings(gotPaths)
	if !reflect.DeepEqual(gotPaths, wantPaths) {
		t.Fatalf("paths after flatten = %v, want %v", gotPaths, wantPaths)
	}

	if moved["GAMENAME/DATA/RESOURCE/a.txt"] != "RESOURCE/a.txt" {
		t.Errorf("moved map missing expected rename, got %v", moved)
	}

	sort.Strings(dropped)
	if !reflect.DeepEqual(dropped, []string{"DATA", "GAMENAME"}) {
		t.Errorf("dropped = %v, want [DATA GAMENAME]", dropped)
	}
}

func TestFlattenStopsAtLoneFile(t *testing.T) {
	// A contains only a single FILE, not a subdirectory: A must not
	// collapse, and the file must not be moved to the build root.
	fs := newTestSet("A/only.txt")

	_, dropped := flatten(fs, true)

	if len(dropped) != 0 {
		t.Errorf("dropped = %v, want none", dropped)
	}
	if _, ok := fs.get("A/only.txt"); !ok {
		t.Error("A/only.txt should not have moved to build root")
	}
}

func TestFlattenIndependentBranches(t *testing.T) {
	fs := newTestSet(
		"WRAP1/X/a.txt",
		"WRAP2/Y/Z/b.txt",
		"KEEP/one.txt",
		"KEEP/two.txt",
	)

	_, _ = flatten(fs, true)

	for _, want := range []string{"X/a.txt", "Z/b.txt", "KEEP/one.txt", "KEEP/two.txt"} {
		if _, ok := fs.get(want); !ok {
			t.Errorf("missing expected path %q after flatten, have %v", want, fs.paths())
		}
	}
}

func TestFlattenRootNeverRemoved(t *testing.T) {
	// If flattening collapses everything down to a single top-level
	// directory, that directory's own files stay nested under it (the
	// build root itself is never populated directly by flattening).
	fs := newTestSet("A/B/one.txt", "A/B/two.txt")

	_, _ = flatten(fs, true)

	if _, ok := fs.get("B/one.txt"); !ok {
		t.Fatalf("expected B/one.txt, got %v", fs.paths())
	}
}
