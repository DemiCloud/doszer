// Package chain follows an archive's dependencies as DOSBox Pure does:
// its parent markers (empty root "<name>.parent" files, resolved in the
// archive's directory, src/dos/drive_zip.cpp zipDriveImpl) archive by
// archive, then the shell or OS image the AUTOBOOT.DBP in effect boots
// from the system directory.
//
// The chain's archives are merged into one C: drive, a child's entry
// winning over a parent's of the same name (zipDriveImpl renames the
// parent's), and ReadAutoBoot (dosbox_pure_run.h) reads AUTOBOOT.DBP from
// that drive: the nearest archive's AUTOBOOT.DBP is the one in effect.
package chain

import (
	"archive/zip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Link is one archive of the chain.
type Link struct {
	Name     string `json:"name" yaml:"name" toml:"name"` // the file name: the archive's, or the parent marker's
	Path     string `json:"path" yaml:"path" toml:"path"`
	Found    bool   `json:"found" yaml:"found" toml:"found"`
	Err      string `json:"error,omitempty" yaml:"error,omitempty" toml:"error,omitempty"`
	Autoboot string `json:"autoboot,omitempty" yaml:"autoboot,omitempty" toml:"autoboot,omitempty"` // its AUTOBOOT.DBP's first line, if it has one
	Has      bool   `json:"has_autoboot" yaml:"has_autoboot" toml:"has_autoboot"`
}

// Kinds of boot.
const (
	KindDOS   = "dos"   // DOSBox Pure's DOS: a program, or the start menu
	KindShell = "shell" // a shell from the system directory
	KindOS    = "os"    // an OS image from the system directory
)

// Chain is an archive's dependencies.
type Chain struct {
	Links    []Link `json:"links" yaml:"links" toml:"links"`             // the archive itself first, then its parents
	BootFrom int    `json:"boot_from" yaml:"boot_from" toml:"boot_from"` // index of the link whose AUTOBOOT.DBP is in effect, -1 for none
	Kind     string `json:"kind" yaml:"kind" toml:"kind"`
	Boot     string `json:"boot,omitempty" yaml:"boot,omitempty" toml:"boot,omitempty"`       // the program, or the shell or OS image path as boot.shell and boot.os have it
	System   string `json:"system,omitempty" yaml:"system,omitempty" toml:"system,omitempty"` // the shell or image file found in the system directory, "" if missing (Locate)
}

// Broken reports why the chain can't run, or "".
func (c *Chain) Broken() string {
	for _, l := range c.Links {
		switch {
		case l.Err != "":
			return l.Name + ": " + l.Err
		case !l.Found:
			return l.Name + " not found"
		}
	}
	if (c.Kind == KindShell || c.Kind == KindOS) && c.System == "" {
		return c.Boot + " not in the system directory"
	}
	return ""
}

// Resolve follows archive's parent markers.
func Resolve(archive string) *Chain {
	c := &Chain{BootFrom: -1, Kind: KindDOS}
	abs, err := filepath.Abs(archive)
	if err != nil {
		abs = archive
	}
	seen := map[string]bool{}
	next := Link{Name: filepath.Base(abs), Path: abs}
	for {
		key := strings.ToLower(next.Path)
		if seen[key] {
			next.Err = "loops back to an archive already in the chain"
			c.Links = append(c.Links, next)
			break
		}
		seen[key] = true
		parents := read(&next)
		c.Links = append(c.Links, next)
		if c.BootFrom < 0 && next.Has {
			c.BootFrom = len(c.Links) - 1
		}
		if !next.Found || next.Err != "" || len(parents) == 0 {
			break
		}
		if len(parents) > 1 {
			c.Links[len(c.Links)-1].Err = "more than one parent marker (" + strings.Join(parents, ", ") + "): DOSBox Pure refuses the archive"
			break
		}
		next = Link{Name: parents[0], Path: filepath.Join(filepath.Dir(next.Path), parents[0])}
	}
	if c.BootFrom >= 0 {
		line := c.Links[c.BootFrom].Autoboot
		switch {
		case strings.HasPrefix(line, "S*"):
			c.Kind, c.Boot = KindShell, line[2:]
		case strings.HasPrefix(line, "O*"):
			c.Kind, c.Boot = KindOS, line[2:]
		default:
			c.Boot = line
		}
	}
	return c
}

// read opens l's archive, recording its AUTOBOOT.DBP, and returns the
// parents its markers name.
func read(l *Link) []string {
	zr, err := zip.OpenReader(l.Path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			l.Err = err.Error()
		}
		return nil
	}
	defer zr.Close()
	l.Found = true
	var parents []string
	for _, f := range zr.File {
		switch {
		case !strings.Contains(f.Name, "/") && len(f.Name) > 7 && strings.EqualFold(f.Name[len(f.Name)-7:], ".parent") && f.UncompressedSize64 == 0:
			parents = append(parents, f.Name[:len(f.Name)-7])
		case strings.EqualFold(f.Name, "AUTOBOOT.DBP"):
			if rc, err := f.Open(); err == nil {
				data, _ := io.ReadAll(io.LimitReader(rc, 4<<10))
				rc.Close()
				line, _, _ := strings.Cut(string(data), "\n")
				l.Autoboot, l.Has = strings.TrimRight(line, "\r"), true
			}
		}
	}
	return parents
}

// Locate finds the chain's shell or OS image in systemDir: boot.shell or
// boot.os plus an extension, matched exactly as DOSBox Pure does
// (ReadAutoBoot): a shell is a .dosz, an image an .img, .ima or .vhd.
func (c *Chain) Locate(systemDir string) {
	c.System = ""
	if systemDir == "" || (c.Kind != KindShell && c.Kind != KindOS) {
		return
	}
	exts := []string{".img", ".ima", ".vhd", ".IMG", ".IMA", ".VHD"}
	if c.Kind == KindShell {
		exts = []string{".dosz", ".DOSZ"}
	}
	base := filepath.Join(systemDir, filepath.FromSlash(c.Boot))
	for _, e := range exts {
		if fi, err := os.Stat(base + e); err == nil && !fi.IsDir() {
			c.System = base + e
			return
		}
	}
}

// UsedBy returns the archives under dir (.dosz, .shell, .zip) whose
// chain includes archive as a parent, at any remove.
func UsedBy(dir, archive string) []string {
	abs, _ := filepath.Abs(archive)
	var out []string
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(p))
		if ext != ".dosz" && ext != ".shell" && ext != ".zip" {
			return nil
		}
		if pa, _ := filepath.Abs(p); strings.EqualFold(pa, abs) {
			return nil
		}
		for _, l := range Resolve(p).Links[1:] {
			if strings.EqualFold(l.Path, abs) {
				out = append(out, p)
				break
			}
		}
		return nil
	})
	return out
}
