package install

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/DemiCloud/doszer/internal/fat"
	"github.com/DemiCloud/doszer/internal/manifest"
	"github.com/DemiCloud/doszer/internal/vdisk"
	"github.com/DemiCloud/doszer/internal/vhd"
)

// Options configures an install.
type Options struct {
	Source string // source directory (with DOSZ.TOML) to extract into

	Exe, SystemDir, SaveDir string // DOSBox Pure; see Discover

	Dir    string // folder to extract, relative to the drive root; chosen automatically if empty
	Force  bool   // overwrite differing files in the source
	DryRun bool   // report without writing
	Keep   bool   // keep a session's save files after extracting

	From      string            // extract from this save or disk image instead of running DOSBox Pure
	Content   string            // the content a D: .sav (or .pure.zip) was made over
	Template  string            // pristine disk image a --from .vhd/.img is compared against
	Templates map[string]string // configured templates by OS image path, the fallback for Template
}

// Report is what an install found and did.
type Report struct {
	Drive     string   // the drive extracted from, as the guest saw it ("C:" or "D:")
	Dir       string   // folder extracted
	Changes   []Change // changes on that drive
	Extracted *Extracted
	Other     []Change // changes on another drive (C: in the OS image model), reported only
	Skipped   []string // source files left out of the session
	Kept      []string // save files left in place
}

// Run runs an install session, or reads opts.From, and extracts the
// result into the source.
func Run(opts Options) (*Report, error) {
	m, err := manifest.Load(opts.Source)
	if err != nil {
		return nil, err
	}
	if opts.From != "" {
		return runFrom(opts, m)
	}

	env, err := Discover(opts.Exe, opts.SystemDir, opts.SaveDir)
	if err != nil {
		return nil, err
	}
	s, err := Prepare(opts.Source, m)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	if err := env.Run(s.Content); err != nil {
		return nil, err
	}

	rep := &Report{Skipped: s.Skipped}
	var saves []string
	switch s.Model {
	case manifest.ModelOS:
		saves, err = collectOS(rep, s, env, opts)
	default:
		saves, err = collectPure(rep, s, env, opts)
	}
	if err != nil {
		rep.Kept = saves
		return rep, err
	}
	if opts.Keep || opts.DryRun {
		rep.Kept = saves
	} else {
		for _, p := range saves {
			os.Remove(p)
		}
	}
	return rep, nil
}

// collectOS reads an OS image session: D: from the content's synthesized
// disk and its <name>-<HASH>.sav, C: from the OS image and its
// <name>-CDRIVE.sav (reported, not extracted: C: is shared by every
// archive using the image).
func collectOS(rep *Report, s *Session, env Env, opts Options) ([]string, error) {
	base, zr, err := vdisk.Open(s.Content, vdisk.DefaultFreeSpaceMB)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	sf := s.SaveFiles(env.SaveDir, base)
	var saves []string

	if _, err := os.Stat(sf.CDrive); err == nil {
		saves = append(saves, sf.CDrive)
		img, err := osImagePath(env.SystemDir, s.OS)
		if err != nil {
			return saves, err
		}
		if rep.Other, err = diffOverlay(img, sf.CDrive); err != nil {
			return saves, fmt.Errorf("C: changes: %w", err)
		}
	}

	if _, err := os.Stat(sf.DDrive); err != nil {
		return saves, errors.New("the session left no D: changes: install the game to D:")
	}
	saves = append(saves, sf.DDrive)
	rep.Drive = "D:"
	return saves, extractVDisk(rep, s.Content, sf.DDrive, opts)
}

// extractVDisk extracts from a content's D: disk with a .sav applied.
func extractVDisk(rep *Report, content, sav string, opts Options) error {
	before, zr1, err := vdisk.Open(content, vdisk.DefaultFreeSpaceMB)
	if err != nil {
		return err
	}
	defer zr1.Close()
	after, zr2, err := vdisk.Open(content, vdisk.DefaultFreeSpaceMB)
	if err != nil {
		return err
	}
	defer zr2.Close()
	f, err := after.OpenSave(sav)
	if err != nil {
		return err
	}
	defer f.Close()

	bfs, err := fat.Open(before)
	if err != nil {
		return err
	}
	afs, err := fat.Open(after)
	if err != nil {
		return fmt.Errorf("D: with changes applied: %w", err)
	}
	if rep.Changes, err = Diff(bfs, afs); err != nil {
		return err
	}
	return extract(rep, afs, opts)
}

// collectPure reads a shell or plain session's <name>.pure.zip.
func collectPure(rep *Report, s *Session, env Env, opts Options) ([]string, error) {
	sf := s.SaveFiles(env.SaveDir, nil)
	if _, err := os.Stat(sf.PureZip); err != nil {
		return nil, errors.New("the session left no C: changes")
	}
	rep.Drive = "C:"
	return []string{sf.PureZip}, extractPureZip(rep, sf.PureZip, s.Content, opts)
}

func extractPureZip(rep *Report, p, content string, opts Options) error {
	pz, err := OpenPureZip(p)
	if err != nil {
		return err
	}
	defer pz.Close()
	var cfs fs.FS
	if content != "" {
		zr, err := zip.OpenReader(content)
		if err != nil {
			return err
		}
		defer zr.Close()
		cfs = zr
	}
	rep.Changes = pz.Changes(cfs)
	return extract(rep, pz, opts)
}

func extract(rep *Report, src fs.FS, opts Options) error {
	rep.Dir = strings.Trim(filepath.ToSlash(opts.Dir), "/")
	if rep.Dir == "" {
		var err error
		if rep.Dir, err = pickDir(rep.Changes); err != nil {
			return err
		}
	}
	var err error
	rep.Extracted, err = Extract(src, rep.Changes, rep.Dir, opts.Source, opts.Force, opts.DryRun)
	return err
}

// diffOverlay lists the changes a .sav makes to a disk image.
func diffOverlay(image, sav string) ([]Change, error) {
	disk, size, closeDisk, err := openDisk(image)
	if err != nil {
		return nil, err
	}
	defer closeDisk()
	f, err := os.Open(sav)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	ov, err := vdisk.NewOverlay(disk, size, f, info.Size())
	if err != nil {
		return nil, err
	}
	before, err := fat.Open(disk)
	if err != nil {
		return nil, err
	}
	after, err := fat.Open(ov)
	if err != nil {
		return nil, err
	}
	return Diff(before, after)
}

// openDisk opens a VHD, or failing that a raw disk image.
func openDisk(p string) (io.ReaderAt, int64, func() error, error) {
	d, err := vhd.Open(p)
	if err == nil {
		return d, d.Size(), d.Close, nil
	}
	if !errors.Is(err, vhd.ErrNotVHD) {
		return nil, 0, nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, 0, nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, nil, err
	}
	return f, info.Size(), f.Close, nil
}

var cdriveSave = regexp.MustCompile(`(?i)-CDRIVE\.sav$`)
var ddriveSave = regexp.MustCompile(`(?i)-([0-9A-F]{8})\.sav$`)

// runFrom extracts from an existing save or disk image.
func runFrom(opts Options, m *manifest.Manifest) (*Report, error) {
	rep := &Report{}
	from := opts.From
	lower := strings.ToLower(from)
	switch {
	case strings.HasSuffix(lower, ".pure.zip"):
		rep.Drive = "C:"
		return rep, extractPureZip(rep, from, opts.Content, opts)

	case cdriveSave.MatchString(from):
		if m.Boot.OS == "" {
			return nil, errors.New("a -CDRIVE.sav needs the manifest's boot.os, the OS image it was made over")
		}
		systemDir := opts.SystemDir
		if systemDir == "" {
			// Only the system directory is needed; any save directory will do.
			env, err := Discover(opts.Exe, "", os.TempDir())
			if err != nil {
				return nil, err
			}
			systemDir = env.SystemDir
		}
		img, err := osImagePath(systemDir, m.Boot.OS)
		if err != nil {
			return nil, err
		}
		rep.Drive = "C:"
		return rep, extractOverlay(rep, img, from, opts)

	case strings.HasSuffix(lower, ".sav"):
		if opts.Content == "" {
			return nil, errors.New("a D: .sav needs --content, the archive it was made over")
		}
		d, zr, err := vdisk.Open(opts.Content, vdisk.DefaultFreeSpaceMB)
		if err != nil {
			return nil, err
		}
		zr.Close()
		if g := ddriveSave.FindStringSubmatch(from); g != nil && !strings.EqualFold(g[1], fmt.Sprintf("%08X", d.Hash)) {
			return nil, fmt.Errorf("%s was made over different content: %s hashes to %08X, not %s", filepath.Base(from), filepath.Base(opts.Content), d.Hash, g[1])
		}
		rep.Drive = "D:"
		return rep, extractVDisk(rep, opts.Content, from, opts)

	case strings.HasSuffix(lower, ".vhd"), strings.HasSuffix(lower, ".img"):
		if opts.Template == "" {
			opts.Template = opts.Templates[m.Boot.OS]
		}
		return rep, extractImage(rep, from, opts)
	}
	return nil, fmt.Errorf("%s: --from takes a .sav, .pure.zip, .vhd or .img", from)
}

func extractOverlay(rep *Report, image, sav string, opts Options) error {
	disk, size, closeDisk, err := openDisk(image)
	if err != nil {
		return err
	}
	defer closeDisk()
	f, err := os.Open(sav)
	if err != nil {
		return err
	}
	defer f.Close()
	info, _ := f.Stat()
	ov, err := vdisk.NewOverlay(disk, size, f, info.Size())
	if err != nil {
		return err
	}
	bfs, err := fat.Open(disk)
	if err != nil {
		return err
	}
	afs, err := fat.Open(ov)
	if err != nil {
		return err
	}
	if rep.Changes, err = Diff(bfs, afs); err != nil {
		return err
	}
	return extract(rep, afs, opts)
}

// extractImage extracts from a whole disk image: compared against
// opts.Template if given, else every file under --dir counts as added.
func extractImage(rep *Report, image string, opts Options) error {
	disk, _, closeDisk, err := openDisk(image)
	if err != nil {
		return err
	}
	defer closeDisk()
	afs, err := fat.Open(disk)
	if err != nil {
		return err
	}
	rep.Drive = "C:"
	if opts.Template != "" {
		tdisk, _, closeT, err := openDisk(opts.Template)
		if err != nil {
			return err
		}
		defer closeT()
		bfs, err := fat.Open(tdisk)
		if err != nil {
			return err
		}
		if rep.Changes, err = Diff(bfs, afs); err != nil {
			return err
		}
		return extract(rep, afs, opts)
	}
	if opts.Dir == "" {
		return errors.New("without a template to compare against, choose the folder to extract with --dir")
	}
	all, err := listFiles(afs)
	if err != nil {
		return err
	}
	for _, f := range all {
		rep.Changes = append(rep.Changes, Change{Path: f.path, Kind: Added, Size: f.size, Modified: f.mod})
	}
	sortChanges(rep.Changes)
	return extract(rep, afs, opts)
}
