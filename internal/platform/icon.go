package platform

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/DemiCloud/doszer/internal/icon"
)

// IconChoice is an icon init found for [icon] (spec §5.11).
type IconChoice struct {
	Path  string // in the source, or on Disc
	Disc  string // the disc image (Disc.Path) it is on, or ""
	Index int
	From  string // why: "AUTORUN.INF", "the game's program"
}

// SuggestIcon finds a game's icon in dir: the one an AUTORUN.INF names,
// in the source's root or a disc image's; else the icon of the one
// Windows program, helpers aside, named after its folder or the game
// (Likely). A choice is only made if its icon can be read. nil if none.
func SuggestIcon(dir, name string, progs []Program) *IconChoice {
	if data, err := os.ReadFile(filepath.Join(dir, "AUTORUN.INF")); err == nil {
		if p, i, ok := AutorunIcon(data); ok {
			if real := findFold(dir, p); real != "" && iconOK(filepath.Join(dir, filepath.FromSlash(real)), i) {
				return &IconChoice{Path: real, Index: i, From: "AUTORUN.INF"}
			}
		}
	}
	discs := Discs(dir)
	defer func() {
		for _, d := range discs {
			d.Close()
		}
	}()
	for _, d := range discs {
		e, err := d.Image.Lookup("AUTORUN.INF")
		if err != nil || e.IsDir {
			continue
		}
		r, err := d.Image.OpenFile(e)
		if err != nil {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(r, 64<<10))
		if err != nil {
			continue
		}
		p, i, ok := AutorunIcon(data)
		if !ok {
			continue
		}
		e, err = d.Image.Lookup(p)
		if err != nil || e.IsDir {
			continue
		}
		rd, err := d.Image.OpenFile(e)
		if err != nil {
			continue
		}
		b, err := io.ReadAll(io.LimitReader(rd, 64<<20))
		if err != nil {
			continue
		}
		if _, err := icon.Make(bytes.NewReader(b), int64(len(b)), i); err == nil {
			return &IconChoice{Path: p, Disc: d.Path, Index: i, From: d.Path + "'s AUTORUN.INF"}
		}
	}

	var win []Program
	for _, p := range progs {
		if (p.Target.Platform == Win16 || p.Target.Platform == Win32) && !strings.Contains(p.Path, ":") && !IsHelper(p.Path) {
			win = append(win, p)
		}
	}
	if p := Likely(win, name); p != nil && iconOK(filepath.Join(dir, filepath.FromSlash(p.Path)), 0) {
		return &IconChoice{Path: p.Path, From: "the game's program"}
	}
	return nil
}

// AutorunIcon returns the icon an AUTORUN.INF's [autorun] section names:
// icon=<file>[,<index>], the file as a slash-separated relative path. A
// negative index (a resource ID) is not supported.
func AutorunIcon(data []byte) (p string, index int, ok bool) {
	section := ""
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(l, "[") && strings.HasSuffix(l, "]") {
			section = strings.ToLower(strings.TrimSpace(l[1 : len(l)-1]))
			continue
		}
		k, v, found := strings.Cut(l, "=")
		if !found || section != "autorun" || !strings.EqualFold(strings.TrimSpace(k), "icon") {
			continue
		}
		v = strings.TrimSpace(v)
		if f, idx, has := strings.Cut(v, ","); has {
			n, err := strconv.Atoi(strings.TrimSpace(idx))
			if err != nil || n < 0 {
				return "", 0, false
			}
			v, index = strings.TrimSpace(f), n
		}
		v = strings.Trim(v, `"`)
		v = strings.TrimLeft(strings.ReplaceAll(v, `\`, "/"), "/")
		v = path.Clean(v)
		if v == "." || v == ".." || strings.HasPrefix(v, "../") || strings.Contains(v, ":") {
			return "", 0, false
		}
		return v, index, true
	}
	return "", 0, false
}

// findFold returns rel as dir spells it, matching each component
// case-insensitively, or "".
func findFold(dir, rel string) string {
	var out []string
	cur := dir
	for _, part := range strings.Split(rel, "/") {
		entries, err := os.ReadDir(cur)
		if err != nil {
			return ""
		}
		found := ""
		for _, e := range entries {
			if strings.EqualFold(e.Name(), part) {
				found = e.Name()
				break
			}
		}
		if found == "" {
			return ""
		}
		out = append(out, found)
		cur = filepath.Join(cur, found)
	}
	return strings.Join(out, "/")
}

// iconOK reports whether the file p yields an icon (icon.Make).
func iconOK(p string, index int) bool {
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	_, err = icon.Make(f, fi.Size(), index)
	return err == nil
}
