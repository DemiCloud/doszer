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

	"github.com/DemiCloud/doszer/internal/zipw"
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
		out = append(out, Change{Path: f.Name, Kind: k, Size: int64(f.UncompressedSize64), Modified: zipw.DOSModified(&f.FileHeader)})
	}
	for _, d := range pz.Deleted {
		out = append(out, Change{Path: strings.ReplaceAll(d, `\`, "/"), Kind: Deleted})
	}
	sortChanges(out)
	return out
}

// Folder is a top-level folder an install's changes are in: its added
// and modified files and their bytes.
type Folder struct {
	Name  string `json:"name" yaml:"name" toml:"name"`
	Files int    `json:"files" yaml:"files" toml:"files"`
	Bytes int64  `json:"bytes" yaml:"bytes" toml:"bytes"`
}

// DirChoiceError is pickDir's answer when the changes span several
// folders and none stands out: they're listed, the biggest first.
type DirChoiceError struct{ Folders []Folder }

func (e *DirChoiceError) Error() string {
	var names []string
	for _, f := range e.Folders {
		names = append(names, fmt.Sprintf("%s (%s)", f.Name, sizeText(f.Bytes)))
	}
	return "changes span " + strings.Join(names, ", ") + ": choose the folder to extract with --dir"
}

func sizeText(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return fmt.Sprintf("%d bytes", n)
}

// pickDir chooses the folder to extract when none was given, from the
// top-level folders added or modified files are in:
//   - the only one, also when files in the root changed beside it (a
//     launcher batch file, an AUTOEXEC.BAT line: they're listed as not
//     extracted);
//   - of several, the only one named like the game (gameNamed: HOCUS of
//     Hocus Pocus), else the one holding at least 80% of their bytes (a
//     game's data dwarfs the sound drivers or utilities an installer
//     may add beside it);
//   - with no folder, the root ("": an installer that only writes its
//     configuration beside the game it came with).
//
// Otherwise a *DirChoiceError lists them.
func pickDir(changes []Change, name string) (string, error) {
	byName := map[string]*Folder{}
	root := false
	for _, c := range changes {
		if c.Kind == Deleted {
			continue
		}
		top, _, nested := strings.Cut(c.Path, "/")
		if !nested {
			root = true
			continue
		}
		f := byName[strings.ToUpper(top)]
		if f == nil {
			f = &Folder{Name: top}
			byName[strings.ToUpper(top)] = f
		}
		f.Files++
		f.Bytes += c.Size
	}
	var folders []Folder
	var total int64
	for _, f := range byName {
		folders = append(folders, *f)
		total += f.Bytes
	}
	sort.Slice(folders, func(i, j int) bool {
		if folders[i].Bytes != folders[j].Bytes {
			return folders[i].Bytes > folders[j].Bytes
		}
		return folders[i].Name < folders[j].Name
	})
	switch {
	case len(folders) == 0 && root:
		return "", nil
	case len(folders) == 0:
		return "", errors.New("the installer changed no files")
	case len(folders) == 1:
		return folders[0].Name, nil
	}
	named := -1
	for i, f := range folders {
		if gameNamed(f.Name, name) {
			if named >= 0 {
				named = -2
				break
			}
			named = i
		}
	}
	if named >= 0 {
		return folders[named].Name, nil
	}
	if folders[0].Bytes*5 >= total*4 {
		return folders[0].Name, nil
	}
	return "", &DirChoiceError{folders}
}

// gameNamed reports whether a folder is named like the game, by letters
// and digits: the whole name (HOCUSPOC... cut to 8.3 or not), or one of
// its words of four letters or more starting the folder's name (DESCENT2
// of Descent II).
func gameNamed(folder, name string) bool {
	norm := func(s string) string {
		var b strings.Builder
		for _, r := range strings.ToLower(s) {
			if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
				b.WriteRune(r)
			}
		}
		return b.String()
	}
	f, n := norm(folder), norm(name)
	if f == "" || n == "" {
		return false
	}
	if f == n || len(f) >= 4 && strings.HasPrefix(n, f) {
		return true
	}
	for _, w := range strings.Fields(name) {
		if w = norm(w); len(w) >= 4 && strings.HasPrefix(f, w) {
			return true
		}
	}
	return false
}

// under reports whether p is dir or inside it, case-insensitively.
func under(p, dir string) bool {
	p, dir = strings.ToLower(p), strings.ToLower(strings.Trim(dir, "/"))
	return dir == "" || p == dir || strings.HasPrefix(p, dir+"/")
}
