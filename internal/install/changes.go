package install

import (
	"archive/zip"
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"strings"
	"time"
)

// Kind is what happened to a file.
type Kind int

const (
	Added Kind = iota
	Modified
	Deleted
)

func (k Kind) String() string {
	return [...]string{"added", "modified", "deleted"}[k]
}

// Change is one file an installer added, modified or deleted.
type Change struct {
	Path     string // slash-separated, as the changed file system names it
	Kind     Kind
	Size     int64
	Modified time.Time
}

// Diff compares two file systems' files, matching paths
// case-insensitively (FAT). A file counts as modified when its size or
// modification time differs: FAT updates the time on every write, and
// comparing content would mean reading every file of a whole OS disk.
//
// The two are listed concurrently: they are separate file systems (an
// image and the image with a save applied), each safe for concurrent use.
func Diff(before, after fs.FS) ([]Change, error) {
	var b map[string]fileInfo
	var errB error
	listed := make(chan struct{})
	go func() {
		defer close(listed)
		b, errB = listFiles(after)
	}()
	a, err := listFiles(before)
	<-listed
	if err != nil {
		return nil, err
	}
	if errB != nil {
		return nil, errB
	}
	var out []Change
	for key, fb := range b {
		fa, ok := a[key]
		switch {
		case !ok:
			out = append(out, Change{Path: fb.path, Kind: Added, Size: fb.size, Modified: fb.mod})
		case fa.size != fb.size || !fa.mod.Equal(fb.mod):
			out = append(out, Change{Path: fb.path, Kind: Modified, Size: fb.size, Modified: fb.mod})
		}
	}
	for key, fa := range a {
		if _, ok := b[key]; !ok {
			out = append(out, Change{Path: fa.path, Kind: Deleted, Size: fa.size, Modified: fa.mod})
		}
	}
	sortChanges(out)
	return out, nil
}

func sortChanges(c []Change) {
	sort.Slice(c, func(i, j int) bool { return c[i].Path < c[j].Path })
}

type fileInfo struct {
	path string
	size int64
	mod  time.Time
}

func listFiles(fsys fs.FS) (map[string]fileInfo, error) {
	out := map[string]fileInfo{}
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		out[strings.ToLower(p)] = fileInfo{path: p, size: info.Size(), mod: info.ModTime()}
		return nil
	})
	return out, err
}

// PureZip is a <content>.pure.zip save: the files DOS programs changed
// on the content's C: drive, with deletions and renames recorded in its
// FILEMODS.DBP (src/dos/drive_union.cpp, unionDriveImpl::ReadSaveFile and
// Union_Modification::Serialize).
type PureZip struct {
	*zip.ReadCloser
	Deleted []string // DOS paths
	Renamed []string // "target <- source" DOS paths
}

// OpenPureZip opens a .pure.zip save.
func OpenPureZip(p string) (*PureZip, error) {
	zr, err := zip.OpenReader(p)
	if err != nil {
		return nil, err
	}
	pz := &PureZip{ReadCloser: zr}
	for _, f := range zr.File {
		if f.Name != "FILEMODS.DBP" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			zr.Close()
			return nil, err
		}
		err = pz.parseMods(rc)
		rc.Close()
		if err != nil {
			zr.Close()
			return nil, fmt.Errorf("%s: FILEMODS.DBP: %w", p, err)
		}
	}
	return pz, nil
}

func (pz *PureZip) parseMods(r io.Reader) error {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), " \t\r")
		switch {
		case strings.HasPrefix(line, "DELETE|"):
			pz.Deleted = append(pz.Deleted, strings.TrimPrefix(line, "DELETE|"))
		case strings.HasPrefix(line, "REDIRECTFILE|"), strings.HasPrefix(line, "REDIRECTDIR|"):
			_, rest, _ := strings.Cut(line, "|")
			target, source, _ := strings.Cut(rest, "|")
			pz.Renamed = append(pz.Renamed, target+" <- "+source)
		}
	}
	return sc.Err()
}

// Changes lists the save's files as changes against the content they
// were made over: modified if the content has the same DOS path,
// compared case-insensitively (DOS paths are), else added. Deletions come
// from FILEMODS.DBP.
func (pz *PureZip) Changes(content fs.FS) []Change {
	inContent := map[string]bool{}
	if content != nil {
		fs.WalkDir(content, ".", func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				inContent[strings.ToLower(p)] = true
			}
			return nil
		})
	}
	var out []Change
	for _, f := range pz.File {
		if f.Name == "FILEMODS.DBP" || strings.HasSuffix(f.Name, "/") {
			continue
		}
		k := Added
		if inContent[strings.ToLower(f.Name)] {
			k = Modified
		}
		out = append(out, Change{Path: f.Name, Kind: k, Size: int64(f.UncompressedSize64), Modified: f.Modified})
	}
	for _, d := range pz.Deleted {
		out = append(out, Change{Path: strings.ReplaceAll(d, `\`, "/"), Kind: Deleted})
	}
	sortChanges(out)
	return out
}

// pickDir chooses the folder to extract when none was given: the one
// top-level directory every added or modified file is under, or the root
// ("") when they're all files in the root (an installer that only writes
// its configuration beside the game it came with).
func pickDir(changes []Change) (string, error) {
	tops := map[string]bool{}
	for _, c := range changes {
		if c.Kind == Deleted {
			continue
		}
		top, _, nested := strings.Cut(c.Path, "/")
		if !nested {
			top = "" // a file in the root
		}
		tops[top] = true
	}
	if len(tops) == 1 {
		for t := range tops {
			return t, nil
		}
	}
	if len(tops) == 0 {
		return "", errors.New("the installer changed no files")
	}
	var names []string
	for t := range tops {
		if t == "" {
			t = "(root)"
		}
		names = append(names, t)
	}
	sort.Strings(names)
	return "", fmt.Errorf("changes span %s: choose the folder to extract with --dir", strings.Join(names, ", "))
}

// under reports whether p is dir or inside it, case-insensitively.
func under(p, dir string) bool {
	p, dir = strings.ToLower(p), strings.ToLower(strings.Trim(dir, "/"))
	return dir == "" || p == dir || strings.HasPrefix(p, dir+"/")
}
