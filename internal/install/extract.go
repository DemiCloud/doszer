package install

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Extracted reports what Extract did, or would do.
type Extracted struct {
	Written   []string // relative paths written (or to write, in a dry run)
	Unchanged []string // already identical in the destination
}

// Extract copies every added or modified file under dir from src into
// dest, at the same relative path, keeping its modification time. A file
// already in dest with different content is a conflict, and nothing is
// written unless force is set.
func Extract(src fs.FS, changes []Change, dir, dest string, force, dryRun bool) (*Extracted, error) {
	res := &Extracted{}
	type job struct {
		c    Change
		data []byte
	}
	var jobs []job
	var conflicts []string
	for _, c := range changes {
		if c.Kind == Deleted || !under(c.Path, dir) {
			continue
		}
		data, err := fs.ReadFile(src, c.Path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", c.Path, err)
		}
		target := filepath.Join(dest, filepath.FromSlash(c.Path))
		if existing, err := os.ReadFile(target); err == nil {
			if bytes.Equal(existing, data) {
				res.Unchanged = append(res.Unchanged, c.Path)
				continue
			}
			conflicts = append(conflicts, c.Path)
		}
		jobs = append(jobs, job{c, data})
	}
	if len(conflicts) > 0 && !force {
		return nil, fmt.Errorf("would overwrite %d file(s) in %s that differ (use -force): %s", len(conflicts), dest, strings.Join(conflicts, ", "))
	}
	for _, j := range jobs {
		res.Written = append(res.Written, j.c.Path)
		if dryRun {
			continue
		}
		target := filepath.Join(dest, filepath.FromSlash(j.c.Path))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return res, err
		}
		if err := os.WriteFile(target, j.data, 0o644); err != nil {
			return res, err
		}
		if err := os.Chtimes(target, j.c.Modified, j.c.Modified); err != nil {
			return res, err
		}
	}
	return res, nil
}
