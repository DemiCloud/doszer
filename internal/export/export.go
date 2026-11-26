// Package export copies archives into a frontend's folders, with what
// they need: for RetroDECK, a game into roms/<system>/ (ES-DE's system
// folders for DOSBox Pure: dos, windows3x, windows9x), its parents
// beside it (DOSBox Pure looks for a parent in the archive's own
// directory: src/dos/drive_zip.cpp, zipDriveImpl), and the shell or OS
// image it boots into the BIOS folder, DOSBox Pure's system directory, at
// the path AUTOBOOT.DBP names (dosbox_pure_run.h, ReadAutoBoot).
package export

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DemiCloud/doszer/internal/chain"
	"github.com/DemiCloud/doszer/internal/config"
	"github.com/DemiCloud/doszer/internal/install"
	"github.com/DemiCloud/doszer/internal/platform"
)

// Target is where archives go: a ROM folder holding a folder per system,
// and the BIOS folder (DOSBox Pure's system directory).
type Target struct {
	ROMs, BIOS string
	From       string // where they came from: "--dir", "RetroDECK", "system directory"
}

// FindRetroDECK finds RetroDECK's folders: dir's roms/ and bios/ if dir
// is given; else RetroDECK's settings (retrodeck.json); else the parent
// of systemDir when it is a bios/ beside a roms/ (a synced copy of
// RetroDECK's folder).
func FindRetroDECK(dir, systemDir string) (Target, error) {
	isDir := func(p string) bool { fi, err := os.Stat(p); return err == nil && fi.IsDir() }
	if dir != "" {
		t := Target{ROMs: filepath.Join(dir, "roms"), BIOS: filepath.Join(dir, "bios"), From: "--dir"}
		if !isDir(t.ROMs) || !isDir(t.BIOS) {
			return Target{}, fmt.Errorf("%s: not a RetroDECK folder (no roms and bios folders)", dir)
		}
		return t, nil
	}
	if r := config.RetroDECK(); r.ROMs != "" && r.BIOS != "" {
		return Target{ROMs: r.ROMs, BIOS: r.BIOS, From: config.FromRetroDECK}, nil
	}
	if systemDir != "" && strings.EqualFold(filepath.Base(systemDir), "bios") {
		if roms := filepath.Join(filepath.Dir(systemDir), "roms"); isDir(roms) {
			return Target{ROMs: roms, BIOS: systemDir, From: "system directory"}, nil
		}
	}
	return Target{}, errors.New("no RetroDECK folder found: give one with --dir")
}

// System is the ES-DE system folder an archive goes in, from how it
// boots: DOS, a Windows 3.x shell, or an OS image (Windows 3.x or 9x,
// by its name; 9x when the name doesn't tell).
func System(c *chain.Chain) string {
	switch c.Kind {
	case chain.KindShell:
		if install.GuessOS(install.SystemFile{Name: c.Boot, Shell: true}).Platform == platform.Win32 {
			return "windows9x"
		}
		return "windows3x"
	case chain.KindOS:
		if install.GuessOS(install.SystemFile{Name: c.Boot}).Platform == platform.Win16 {
			return "windows3x"
		}
		return "windows9x"
	}
	return "dos"
}

// Kinds of step.
const (
	Copy     = "copy"     // not there yet
	Replace  = "replace"  // there, different: copied with overwrite
	Same     = "same"     // there, the same size and time
	Conflict = "conflict" // there, different, and kept
	Missing  = "missing"  // the source isn't here
)

// Step is one file of an export.
type Step struct {
	What   string `json:"what" yaml:"what" toml:"what"` // game, parent, system
	From   string `json:"from" yaml:"from" toml:"from"`
	To     string `json:"to" yaml:"to" toml:"to"`
	Action string `json:"action" yaml:"action" toml:"action"`
}

// Plan works out the steps exporting archives takes: each archive, the
// parents its chain names, and its shell or OS image from systemDir.
// overwrite replaces archives that differ; a shell or OS image is never
// replaced, only copied when missing (the copy there may have been
// changed by playing, and is the frontend's).
func Plan(archives []string, systemDir string, t Target, overwrite bool) ([]Step, error) {
	var steps []Step
	seen := map[string]bool{}
	add := func(what, from, to string, replaceable bool) {
		key := strings.ToLower(to)
		if seen[key] {
			return
		}
		seen[key] = true
		s := Step{What: what, From: from, To: to, Action: Copy}
		src, err := os.Stat(from)
		switch dst, derr := os.Stat(to); {
		case err != nil:
			s.Action = Missing
		case derr != nil:
		case dst.Size() == src.Size() && sameTime(dst.ModTime(), src.ModTime()):
			s.Action = Same
		case overwrite && replaceable:
			s.Action = Replace
		default:
			s.Action = Conflict
		}
		steps = append(steps, s)
	}
	for _, a := range archives {
		c := chain.Resolve(a)
		dir := filepath.Join(t.ROMs, System(c))
		for i, l := range c.Links {
			what := "game"
			if i > 0 || strings.EqualFold(filepath.Ext(l.Path), ".dosp") {
				what = "parent"
			}
			add(what, l.Path, filepath.Join(dir, filepath.Base(l.Path)), true)
		}
		if c.Kind == chain.KindShell || c.Kind == chain.KindOS {
			c.Locate(systemDir)
			if c.System == "" {
				there := *c
				if there.Locate(t.BIOS); there.System != "" { // not here, but there already
					if !seen[strings.ToLower(there.System)] {
						seen[strings.ToLower(there.System)] = true
						steps = append(steps, Step{What: "system", From: there.System, To: there.System, Action: Same})
					}
					continue
				}
				add("system", filepath.Join(systemDir, filepath.FromSlash(c.Boot)), filepath.Join(t.BIOS, filepath.FromSlash(c.Boot)), false)
				continue
			}
			rel, err := filepath.Rel(systemDir, c.System)
			if err != nil {
				return nil, err
			}
			add("system", c.System, filepath.Join(t.BIOS, rel), false)
		}
	}
	return steps, nil
}

// Apply copies the steps that copy or replace, keeping each file's
// modification time (so a later Plan sees it the same). Each goes to a
// temporary file beside its destination first, renamed into place.
func Apply(steps []Step, progress func(Step)) error {
	for _, s := range steps {
		if s.Action != Copy && s.Action != Replace {
			continue
		}
		if progress != nil {
			progress(s)
		}
		if err := copyFile(s.From, s.To); err != nil {
			return err
		}
	}
	return nil
}

// sameTime compares modification times to FAT's two seconds: an SD card
// or a synced copy may round them.
func sameTime(a, b time.Time) bool {
	d := a.Sub(b)
	return d < 2*time.Second && d > -2*time.Second
}

func copyFile(from, to string) error {
	src, err := os.Open(from)
	if err != nil {
		return err
	}
	defer src.Close()
	fi, err := src.Stat()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(to), ".doszer-export-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, src); err != nil {
		tmp.Close()
		return fmt.Errorf("%s: %w", to, err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chtimes(tmp.Name(), fi.ModTime(), fi.ModTime()); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), to)
}
