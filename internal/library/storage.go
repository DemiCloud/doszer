package library

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/DemiCloud/doszer/internal/manifest"
)

// Source is a folder in the sources folder: a game's files as dumped,
// imported or installed, before they're built.
type Source struct {
	Path     string
	Name     string // its manifest's name, else the folder's
	Size     int64
	Files    int
	Modified time.Time // its newest file's
}

// Sources lists the folders in dir, sorted by name, each with its size
// and newest file's date. A missing dir is no sources.
func Sources(dir string) ([]Source, error) {
	ents, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Source
	for _, e := range ents {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue // the dump work folders (.<name>.doszer-dump) belong to theirs
		}
		p := filepath.Join(dir, e.Name())
		s := Source{Path: p, Name: e.Name()}
		if m, err := manifest.Load(p); err == nil && m.Name != "" {
			s.Name = m.Name
		}
		filepath.WalkDir(p, func(_ string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if fi, err := d.Info(); err == nil {
				s.Size += fi.Size()
				s.Files++
				if fi.ModTime().After(s.Modified) {
					s.Modified = fi.ModTime()
				}
			}
			return nil
		})
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, nil
}

// Kinds of save file (DOSBox Pure's DBP_GetSaveFile).
const (
	SaveC      = "C: changes"            // <content>.pure.zip: the plain and shell models
	SaveCImage = "C: changes (OS image)" // <content>-CDRIVE.sav: an OS image's C:, under diff
	SaveD      = "D: changes (OS image)" // <content>-<HASH>.sav: the synthesized D: disk
)

// Save is a DOSBox Pure save file.
type Save struct {
	Path     string
	Content  string // the archive it belongs to, by name without extension
	Kind     string
	Size     int64
	Modified time.Time
	Game     string // that archive's path, "" if none of the games is it
}

var dSave = regexp.MustCompile(`^(.+)-[0-9A-Fa-f]{8}\.sav$`)

// saveContent says whose save a file is and what kind, or "" if it's
// no DOSBox Pure save.
func saveContent(name string) (content, kind string) {
	switch {
	case strings.HasSuffix(name, ".pure.zip"):
		return strings.TrimSuffix(name, ".pure.zip"), SaveC
	case strings.HasSuffix(name, "-CDRIVE.sav"):
		return strings.TrimSuffix(name, "-CDRIVE.sav"), SaveCImage
	}
	if m := dSave.FindStringSubmatch(name); m != nil {
		return m[1], SaveD
	}
	return "", ""
}

// Saves lists the DOSBox Pure save files in the folders games keep them
// in (dirOf, an archive's) and in extra folders, each matched to its
// game: a save whose folder is its game's and whose name starts with
// the game's file name. Sorted by game, then file.
func Saves(games []Game, dirOf func(archive string) string, extra ...string) []Save {
	owner := map[string]string{} // folder + "\x00" + content -> archive
	dirs := map[string]bool{}
	for _, d := range extra {
		if d != "" {
			dirs[filepath.Clean(d)] = true
		}
	}
	for _, g := range games {
		d := dirOf(g.Path)
		if d == "" {
			continue
		}
		d = filepath.Clean(d)
		dirs[d] = true
		stem := strings.TrimSuffix(filepath.Base(g.Path), filepath.Ext(g.Path))
		owner[d+"\x00"+stem] = g.Path
	}
	var out []Save
	for d := range dirs {
		ents, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, e := range ents {
			if e.IsDir() {
				continue
			}
			content, kind := saveContent(e.Name())
			if kind == "" {
				continue
			}
			s := Save{Path: filepath.Join(d, e.Name()), Content: content, Kind: kind, Game: owner[d+"\x00"+content]}
			if fi, err := e.Info(); err == nil {
				s.Size, s.Modified = fi.Size(), fi.ModTime()
			}
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := strings.ToLower(out[i].Content), strings.ToLower(out[j].Content)
		if a != b {
			return a < b
		}
		return out[i].Path < out[j].Path
	})
	return out
}
