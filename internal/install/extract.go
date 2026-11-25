package install

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/DemiCloud/doszer/internal/status"
)

// Extracted reports what Extract did, or would do.
type Extracted struct {
	Written   []string // relative paths written (or to write, in a dry run)
	Unchanged []string // already identical in the destination
}

// Extract copies every added or modified file under dir from src into
// dest, at the same relative path, keeping its modification time. A file
// already in dest with different content is a conflict, and nothing is
// written unless force is set - except, with modifiedOK, a modified one:
// after a session, dest is where that file came from, so the installer's
// change to it is what extracting is for. Files are streamed, never held in memory
// whole: an installed game can be gigabytes.
//
// A file goes where dest already has it under any case (DOS paths are
// case-insensitive): a save's GAME/SETUP.CFG replaces the source's
// game/setup.cfg rather than landing beside it on a case-sensitive file
// system. A path that isn't relative and within dest is an error.
func Extract(src fs.FS, changes []Change, dir, dest string, force, modifiedOK, dryRun bool) (*Extracted, error) {
	res := &Extracted{}
	type job struct {
		c      Change
		target string
	}
	var jobs []job
	var conflicts []string
	targets := &targetResolver{dest: dest, dirs: map[string][]os.DirEntry{}}
	for _, c := range changes {
		if c.Kind == Deleted || !under(c.Path, dir) {
			continue
		}
		if !fs.ValidPath(c.Path) || strings.Contains(c.Path, `\`) || c.Path == "." {
			return nil, fmt.Errorf("%q: not a file path within the drive", c.Path)
		}
		target, err := targets.resolve(c.Path)
		if err != nil {
			return nil, err
		}
		same, exists, err := sameContent(src, c.Path, target)
		if err != nil {
			return nil, err
		}
		if same {
			res.Unchanged = append(res.Unchanged, c.Path)
			continue
		}
		if exists && !(modifiedOK && c.Kind == Modified) {
			conflicts = append(conflicts, c.Path)
		}
		jobs = append(jobs, job{c, target})
	}
	if len(conflicts) > 0 && !force {
		return nil, fmt.Errorf("would overwrite %d file(s) in %s that differ (use --force): %s", len(conflicts), dest, strings.Join(conflicts, ", "))
	}
	if !dryRun && len(jobs) > 0 {
		status.Step("Copying %s into the source", dir)
	}
	for i, j := range jobs {
		res.Written = append(res.Written, j.c.Path)
		if dryRun {
			continue
		}
		status.Count(i+1, len(jobs))
		if err := copyOut(src, j.c, j.target); err != nil {
			return res, err
		}
	}
	return res, nil
}

// targetResolver finds where a slash-separated path goes in dest: each
// component spelled as dest already spells it, compared
// case-insensitively (an exact match first), and as given where dest has
// none yet. Directory listings are cached.
type targetResolver struct {
	dest string
	dirs map[string][]os.DirEntry // directory -> its entries; nil if it doesn't exist
}

func (r *targetResolver) resolve(p string) (string, error) {
	cur := r.dest
	parts := strings.Split(p, "/")
	for i, part := range parts {
		entries, ok := r.dirs[cur]
		if !ok {
			var err error
			entries, err = os.ReadDir(cur)
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return "", err
			}
			r.dirs[cur] = entries
		}
		if entries == nil { // the rest is new
			return filepath.Join(append([]string{cur}, parts[i:]...)...), nil
		}
		name := part
		for _, e := range entries {
			if e.Name() == part {
				name = part
				break
			}
			if strings.EqualFold(e.Name(), part) {
				name = e.Name()
			}
		}
		cur = filepath.Join(cur, name)
	}
	return cur, nil
}

// sameContent reports whether target exists and holds exactly src's file
// p, comparing sizes first and then content in chunks.
func sameContent(src fs.FS, p, target string) (same, exists bool, err error) {
	tf, err := os.Open(target)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, false, nil
		}
		return false, false, err
	}
	defer tf.Close()
	sf, err := src.Open(p)
	if err != nil {
		return false, true, fmt.Errorf("%s: %w", p, err)
	}
	defer sf.Close()
	ti, err := tf.Stat()
	if err != nil {
		return false, true, err
	}
	si, err := sf.Stat()
	if err != nil {
		return false, true, fmt.Errorf("%s: %w", p, err)
	}
	if ti.Size() != si.Size() {
		return false, true, nil
	}
	a, b := make([]byte, 64<<10), make([]byte, 64<<10)
	for {
		n, errA := io.ReadFull(sf, a)
		m, errB := io.ReadFull(tf, b[:n])
		if errB != nil && errB != io.ErrUnexpectedEOF && errB != io.EOF {
			return false, true, errB
		}
		if m != n || !bytes.Equal(a[:n], b[:m]) {
			return false, true, nil
		}
		if errA == io.EOF || errA == io.ErrUnexpectedEOF {
			return true, true, nil
		}
		if errA != nil {
			return false, true, fmt.Errorf("%s: %w", p, errA)
		}
	}
}

// copyOut streams src's file c.Path to target, keeping its modification
// time.
func copyOut(src fs.FS, c Change, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	sf, err := src.Open(c.Path)
	if err != nil {
		return fmt.Errorf("%s: %w", c.Path, err)
	}
	defer sf.Close()
	f, err := os.Create(target)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, sf); err != nil {
		f.Close()
		return fmt.Errorf("%s: %w", c.Path, err)
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Chtimes(target, c.Modified, c.Modified)
}
