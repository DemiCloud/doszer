package build

import (
	"reflect"
	"sort"
	"strings"
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
	moved, dropped, _ := flatten(fs, false)
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

	moved, dropped, err := flatten(fs, true)
	if err != nil {
		t.Fatal(err)
	}

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

	_, dropped, _ := flatten(fs, true)

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

	_, _, _ = flatten(fs, true)

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

	_, _, _ = flatten(fs, true)

	if _, ok := fs.get("B/one.txt"); !ok {
		t.Fatalf("expected B/one.txt, got %v", fs.paths())
	}
}

func TestFlattenRejectsCollision(t *testing.T) {
	tests := []struct {
		name  string
		paths []string
	}{
		{"collapsed dir onto dir", []string{"A/WRAP/DATA/x.dat", "A/DATA/y.dat", "A/readme.txt"}},
		{"collapsed dir onto dir, other case", []string{"A/WRAP/data/x.dat", "A/DATA/y.dat", "A/readme.txt"}},
		{"collapsed dir onto file", []string{"A/WRAP/DATA/x.dat", "A/data", "A/readme.txt"}},
		{"two collapsed dirs", []string{"W1/DATA/x.dat", "W2/DATA/y.dat"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := newTestSet(tt.paths...)
			before := fs.paths()
			if _, _, err := flatten(fs, true); err == nil {
				t.Fatalf("flatten succeeded, paths now %v", fs.paths())
			}
			if got := fs.paths(); !reflect.DeepEqual(got, before) {
				t.Errorf("paths after failed flatten = %v, want unchanged %v", got, before)
			}
		})
	}
}

func TestFlattenKeepsCaseVariantSiblings(t *testing.T) {
	// Two directories differing only in case, neither collapsed, are not a
	// collision flatten caused.
	fs := newTestSet("Data/a.txt", "DATA/b.txt")
	if _, _, err := flatten(fs, true); err != nil {
		t.Fatal(err)
	}
}

func TestCollectedWarnsOnCaseDuplicates(t *testing.T) {
	fs := newFileSet()
	if w := fs.collected("GAME/README.TXT", dataEntry([]byte("a"), Epoch)); w != "" {
		t.Errorf("first path warned: %s", w)
	}
	if w := fs.collected("GAME/A.TXT", dataEntry([]byte("b"), Epoch)); w != "" {
		t.Errorf("unrelated path warned: %s", w)
	}
	if w := fs.collected("game/readme.txt", dataEntry([]byte("c"), Epoch)); !strings.Contains(w, "GAME/README.TXT and game/readme.txt") {
		t.Errorf("warning = %q", w)
	}
	if got := strings.Join(fs.paths(), ","); got != "GAME/A.TXT,game/readme.txt" {
		t.Errorf("paths = %s", got)
	}
}
