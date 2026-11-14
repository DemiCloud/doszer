package manifest

import (
	"path/filepath"
	"testing"
)

// TestExamples checks that every example manifest in examples/ is valid.
func TestExamples(t *testing.T) {
	dirs, err := filepath.Glob(filepath.Join("..", "..", "examples", "*", FileName))
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) == 0 {
		t.Fatal("no example manifests found")
	}
	for _, p := range dirs {
		dir := filepath.Dir(p)
		t.Run(filepath.Base(dir), func(t *testing.T) {
			if _, err := Load(dir); err != nil {
				t.Errorf("%s: %v", p, err)
			}
		})
	}
}
