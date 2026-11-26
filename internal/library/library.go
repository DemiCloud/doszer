// Package library lists the archives doszer's window shows: those under
// the ROM directory, with what each archive says about itself (name, boot
// model, DOSZ.ICO) and how long each has been played (Stats).
package library

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/DemiCloud/doszer/internal/build"
	"github.com/DemiCloud/doszer/internal/chain"
	"github.com/DemiCloud/doszer/internal/manifest"
	"github.com/DemiCloud/doszer/internal/platform"
)

// Kind is how an archive boots, for showing.
type Kind int

// Kinds.
const (
	KindDOS   Kind = iota // DOSBox Pure's DOS: exec, or nothing set
	KindShell             // a Windows 3.x shell
	KindOS                // an OS image
)

// Game is one archive.
type Game struct {
	Path     string        // absolute
	Root     string        // the ROM directory it was found under
	Rel      string        // relative to Root, slash-separated
	Name     string        // the manifest's, else the file name without extension
	Kind     Kind          //
	System   string        // the shell or OS image it boots, without directories ("Windows 98"), or ""
	Boot     string        // the same as boot.shell or boot.os has it ("dosbox_pure/Windows 98"), or ""
	Size     int64         //
	Modified time.Time     //
	Icon     []byte        // DOSZ.ICO, or nil
	Err      string        // why the archive couldn't be read, if it couldn't
	Parents  []string      // its parent chain, nearest first, by file name
	Broken   string        // why the parent chain can't be followed, or ""
	Need     platform.Need // what its programs need (its own, else its parents')
	Disc     string        // its first disc's first data track SHA-1 (the manifest's [[source.media]]), or ""
	Redump   string        // that disc's Redump name, if Redump has it

	LastPlayed time.Time
	PlayTime   time.Duration
}

// Scan lists the .dosz archives under each of dirs, at any depth, sorted
// by name, with their play statistics from stats (nil for none). Shells,
// which live in the system directory, aren't games; an archive that
// can't be read is listed with Err set. A directory that can't be read
// is an error; the others are still listed.
func Scan(dirs []string, stats *Stats) ([]Game, error) {
	type found struct{ root, path string }
	var paths []found
	var errs []error
	for _, dir := range dirs {
		err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if p == dir {
					return err
				}
				return nil // an unreadable subdirectory: skipped
			}
			if !d.IsDir() && strings.EqualFold(filepath.Ext(p), ".dosz") {
				paths = append(paths, found{dir, p})
			}
			return nil
		})
		if err != nil {
			errs = append(errs, err)
		}
	}

	games := make([]Game, len(paths))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i, f := range paths {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			games[i] = read(f.root, f.path)
		}()
	}
	wg.Wait()
	if stats != nil {
		for i := range games {
			s := stats.Get(games[i].Path)
			games[i].LastPlayed, games[i].PlayTime = s.LastPlayed, s.Duration()
		}
	}
	sort.SliceStable(games, func(i, j int) bool {
		return strings.ToLower(games[i].Name) < strings.ToLower(games[j].Name)
	})
	return games, errors.Join(errs...)
}

// read reads one archive's entry.
func read(dir, p string) Game {
	abs, _ := filepath.Abs(p)
	rel, _ := filepath.Rel(dir, p)
	stem := strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
	g := Game{Path: abs, Root: dir, Rel: filepath.ToSlash(rel), Name: stem}
	if fi, err := os.Stat(p); err == nil {
		g.Size, g.Modified = fi.Size(), fi.ModTime()
	}
	zr, err := zip.OpenReader(p)
	if err != nil {
		g.Err = err.Error()
		return g
	}
	defer zr.Close()
	// How it boots comes from its chain: the nearest AUTOBOOT.DBP, which
	// may be a parent's (chain.Resolve).
	c := chain.Resolve(p)
	switch c.Kind {
	case chain.KindShell:
		g.Kind, g.Boot = KindShell, c.Boot
	case chain.KindOS:
		g.Kind, g.Boot = KindOS, c.Boot
	}
	if g.Boot != "" {
		g.System = path.Base(g.Boot)
	}
	for _, l := range c.Links[1:] {
		g.Parents = append(g.Parents, l.Name)
	}
	for _, l := range c.Links {
		if l.Err != "" || !l.Found {
			g.Broken = l.Name + " not found"
			if l.Err != "" {
				g.Broken = l.Name + ": " + l.Err
			}
			break
		}
	}
	g.Need = platform.Needs(programs(zr.File))
	if g.Need.Game.Platform == platform.NotProgram {
		for _, l := range c.Links[1:] {
			if pz, err := zip.OpenReader(l.Path); err == nil {
				g.Need = platform.Needs(programs(pz.File))
				pz.Close()
			}
			if g.Need.Game.Platform != platform.NotProgram {
				break
			}
		}
	}
	for _, f := range zr.File {
		switch {
		case f.Name == manifest.FileName:
			if data, err := small(f, 1<<20); err == nil {
				if m, err := manifest.Parse(data, stem); err == nil {
					if m.Name != "" {
						g.Name = m.Name
					}
					// The disc identifies the game, as a title ID would.
					if len(m.Source.Media) > 0 {
						md := m.Source.Media[0]
						g.Redump = md.RedumpName
						for _, t := range md.Tracks {
							if t.Type == manifest.TrackData {
								g.Disc = t.SHA1
								break
							}
						}
					}
				}
			}
		case strings.EqualFold(f.Name, build.IconName):
			if data, err := small(f, 4<<20); err == nil {
				g.Icon = data
			}
		}
	}
	return g
}

// small reads an archive entry of at most limit bytes.
func small(f *zip.File, limit int64) ([]byte, error) {
	if f.UncompressedSize64 > uint64(limit) {
		return nil, errors.New("too large")
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, limit))
}

// Played is one archive's play statistics.
type Played struct {
	LastPlayed time.Time `json:"last_played"`
	Seconds    int64     `json:"seconds"`
	Sessions   int       `json:"sessions"`
}

// Duration is the total play time.
func (p Played) Duration() time.Duration { return time.Duration(p.Seconds) * time.Second }

// Stats records how long archives have been played, in a JSON file keyed
// by each archive's path relative to the ROM directory holding it (so a
// portable doszer's statistics survive a change of drive letter), or its
// absolute path outside them.
type Stats struct {
	path    string
	romDirs []string
	mu      sync.Mutex
	Games   map[string]Played `json:"games"`
}

// LoadStats reads the statistics file at path (one that doesn't exist yet
// is empty) for archives under romDirs. With path "", statistics are kept
// in memory only.
func LoadStats(path string, romDirs []string) (*Stats, error) {
	s := &Stats{path: path, romDirs: romDirs, Games: map[string]Played{}}
	if path == "" {
		return s, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(data, s); err != nil {
		return s, err
	}
	if s.Games == nil {
		s.Games = map[string]Played{}
	}
	return s, nil
}

// key is archive's key in the file.
func (s *Stats) key(archive string) string {
	for _, d := range s.romDirs {
		if rel, err := filepath.Rel(d, archive); err == nil && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel) {
			return filepath.ToSlash(rel)
		}
	}
	return archive
}

// Get returns archive's statistics.
func (s *Stats) Get(archive string) Played {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Games[s.key(archive)]
}

// Add records a session of archive that started at start and lasted d,
// and writes the file.
func (s *Stats) Add(archive string, start time.Time, d time.Duration) error {
	s.mu.Lock()
	k := s.key(archive)
	p := s.Games[k]
	p.LastPlayed = start.UTC().Truncate(time.Second)
	p.Seconds += int64(d.Round(time.Second) / time.Second)
	p.Sessions++
	s.Games[k] = p
	if s.path == "" {
		s.mu.Unlock()
		return nil // kept for the session only
	}
	data, err := json.MarshalIndent(s, "", "  ")
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// programs classifies an archive's programs by their headers.
func programs(files []*zip.File) []platform.Program {
	var out []platform.Program
	for _, f := range files {
		if !platform.IsProgramName(f.Name) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		if t := platform.Classify(f.Name, rc); t.Platform != platform.NotProgram {
			out = append(out, platform.Program{Path: f.Name, Target: t})
		}
		rc.Close()
	}
	return out
}
