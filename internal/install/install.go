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
	"github.com/DemiCloud/doszer/internal/status"
	"github.com/DemiCloud/doszer/internal/vdisk"
	"github.com/DemiCloud/doszer/internal/vhd"
)

// Options configures an install.
type Options struct {
	Source string // source directory (with DOSZ.TOML) to extract into

	Frontend, FrontendDir, SystemDir, SaveDir string // DOSBox Pure; see Discover

	Dir    string // folder to extract, relative to the drive root; chosen automatically if empty
	Drive  string // OS image model: "C" to extract from C: changes instead of D:
	Force  bool   // overwrite differing files in the source
	DryRun bool   // report without writing
	Keep   bool   // keep a session's save files after extracting

	From      string            // extract from this save or disk image instead of running DOSBox Pure
	Content   string            // the content a D: .sav (or .pure.zip) was made over
	Template  string            // pristine disk image a --from .vhd/.img is compared against
	Templates map[string]string // configured templates by OS image path, the fallback for Template

	OS        string            // OS image an OS image session boots instead of boot.os (e.g. a plain install image)
	InstallOS map[string]string // configured install images by boot.os, the fallback for OS
}

func (o Options) frontend() Frontend {
	return Frontend{Kind: o.Frontend, Dir: o.FrontendDir, SystemDir: o.SystemDir, SaveDir: o.SaveDir}
}

// sessionOS is the OS image an OS image session boots, and a -CDRIVE.sav
// is read over: opts.OS, else the configured install image for boot.os,
// else boot.os itself.
func sessionOS(opts Options, m *manifest.Manifest) string {
	if opts.OS != "" {
		return opts.OS
	}
	if img := opts.InstallOS[m.Boot.OS]; img != "" {
		return img
	}
	return m.Boot.OS
}

// checkDiffSafe rejects a dynamic VHD as an OS image session's C:. With
// "Save Difference Per Content", DOSBox Pure decides whether a written
// sector changed by comparing it against the image file's bytes at
// sector*512 (src/ints/bios_disk.cpp, imageDisk::Write_AbsoluteSector),
// which for a dynamic VHD aren't that sector: a write that matches those
// bytes - typically an all-zero sector - is dropped, and the sector reads
// back as the image's old content. A raw image or fixed VHD stores sector
// N at sector*512, so the comparison holds.
func checkDiffSafe(systemDir, osPath string) error {
	img, err := osImagePath(systemDir, osPath)
	if err != nil {
		return err
	}
	d, err := vhd.Open(img)
	if errors.Is(err, vhd.ErrNotVHD) {
		return nil // a raw image
	}
	if err != nil {
		return err
	}
	defer d.Close()
	if d.Type() == vhd.TypeDynamic {
		return fmt.Errorf("%s is a dynamic VHD: DOSBox Pure's Save Difference mode loses writes on one (README \"Install\"); convert it to a fixed VHD or raw .img, e.g. qemu-img convert -O vpc -o subformat=fixed", img)
	}
	return nil
}

// osDriveDir picks the folder to extract from an OS image's C: changes:
// Windows changes its own files during any session, so WINDOWS/ and files
// in the root don't count.
func osDriveDir(changes []Change) (string, error) {
	var own []Change
	for _, c := range changes {
		top, _, nested := strings.Cut(c.Path, "/")
		if nested && !strings.EqualFold(top, "WINDOWS") {
			own = append(own, c)
		}
	}
	return pickDir(own)
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

	// Plain model only: the programs the install added that could start
	// the game (StartPrograms, as DOS paths), and the one set as
	// boot.exec when the manifest had none (StartProgram), else "".
	Programs []string
	Exec     string

	// OS image model: RegistryFile as a DOS path, holding the registry
	// entries the installer added for its program (exportRegistry), and
	// how many values; "" if it added none.
	Registry       string
	RegistryValues int
}

// Run runs an install session, or reads opts.From, and extracts the
// result into the source.
func Run(opts Options) (*Report, error) {
	m, err := manifest.Load(opts.Source)
	if err != nil {
		return nil, err
	}
	switch strings.ToUpper(strings.TrimSuffix(opts.Drive, ":")) {
	case "", "D":
		opts.Drive = ""
	case "C":
		if m.Model() != manifest.ModelOS {
			return nil, errors.New("--drive applies only to an OS image session (boot.os)")
		}
		opts.Drive = "C"
	default:
		return nil, fmt.Errorf("--drive %s: must be c or d", opts.Drive)
	}
	if m.Model() == manifest.ModelOS {
		session := *m
		session.Boot.OS = sessionOS(opts, m)
		m = &session
	}
	var rep *Report
	if opts.From != "" {
		rep, err = runFrom(opts, m)
	} else {
		rep, err = runSession(opts, m)
	}
	if err == nil {
		err = setStartProgram(rep, m, opts)
	}
	return rep, err
}

// setStartProgram fills rep.Programs for a plain model install to C:, and
// sets boot.exec to StartProgram's pick if the manifest has none (not in
// a dry run, which only reports it).
func setStartProgram(rep *Report, m *manifest.Manifest, opts Options) error {
	if m.Model() != manifest.ModelPlain || rep.Drive != "C:" || rep.Extracted == nil {
		return nil
	}
	for _, p := range StartPrograms(rep.Changes, rep.Dir) {
		rep.Programs = append(rep.Programs, DOSPath(rep.Drive, p))
	}
	if m.Boot.Exec != "" {
		return nil
	}
	if p := StartProgram(StartPrograms(rep.Changes, rep.Dir)); p != "" {
		rep.Exec = DOSPath(rep.Drive, p)
		if !opts.DryRun {
			return manifest.SetExecFile(opts.Source, rep.Exec)
		}
	}
	return nil
}

// runSession runs an install session under DOSBox Pure and extracts what
// it wrote.
func runSession(opts Options, m *manifest.Manifest) (*Report, error) {
	env, err := Discover(opts.frontend())
	if err != nil {
		return nil, err
	}
	if m.Model() == manifest.ModelOS {
		if err := checkDiffSafe(env.SystemDir, m.Boot.OS); err != nil {
			return nil, err
		}
	}
	s, err := Prepare(opts.Source, m)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	status.Step("DOSBox Pure is running: install the game, then close %s", env.Name())
	if err := env.Run(s); err != nil {
		return nil, err
	}

	status.Step("Reading the session's changes")
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
		if opts.Drive == "C" {
			rep.Drive = "C:"
			if _, err := os.Stat(sf.DDrive); err == nil {
				saves = append(saves, sf.DDrive)
			}
			return saves, extractOverlay(rep, img, sf.CDrive, opts)
		}
		status.Step("Comparing C: with the OS image")
		before, after, closeC, err := openOverlay(img, sf.CDrive)
		if err != nil {
			return saves, fmt.Errorf("C: changes: %w", err)
		}
		defer closeC()
		if rep.Other, err = Diff(before, after); err != nil {
			return saves, fmt.Errorf("C: changes: %w", err)
		}
		if err := extractD(rep, s, sf, &saves, opts); err != nil {
			return saves, err
		}
		return saves, exportRegistry(rep, before, after, opts)
	} else if opts.Drive == "C" {
		return saves, errors.New("the session left no C: changes")
	}
	return saves, extractD(rep, s, sf, &saves, opts)
}

// extractD extracts an OS image session's D: changes.
func extractD(rep *Report, s *Session, sf SaveFiles, saves *[]string, opts Options) error {
	if _, err := os.Stat(sf.DDrive); err != nil {
		return errors.New("the session left no D: changes: install the game to D:")
	}
	*saves = append(*saves, sf.DDrive)
	rep.Drive = "D:"
	status.Step("Reading D: changes")
	return extractVDisk(rep, s.Content, sf.DDrive, opts)
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

// openOverlay opens a disk image's file system as it is (before) and
// with a .sav applied (after), until closeAll is called.
func openOverlay(image, sav string) (before, after fs.FS, closeAll func(), err error) {
	disk, size, closeDisk, err := openDisk(image)
	if err != nil {
		return nil, nil, nil, err
	}
	f, err := os.Open(sav)
	if err != nil {
		closeDisk()
		return nil, nil, nil, err
	}
	closeAll = func() { f.Close(); closeDisk() }
	info, err := f.Stat()
	if err == nil {
		var ov io.ReaderAt
		if ov, err = vdisk.NewOverlay(disk, size, f, info.Size()); err == nil {
			if before, err = fat.Open(disk); err == nil {
				after, err = fat.Open(ov)
			}
		}
	}
	if err != nil {
		closeAll()
		return nil, nil, nil, err
	}
	return before, after, closeAll, nil
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
			return nil, errors.New("a -CDRIVE.sav needs the manifest's boot.os (or --os), the OS image it was made over")
		}
		systemDir := opts.SystemDir
		if systemDir == "" {
			// Only the system directory is needed; any save directory will do.
			f := opts.frontend()
			f.SaveDir = os.TempDir()
			env, err := Discover(f)
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
	status.Step("Reading C: changes")
	bfs, afs, closeAll, err := openOverlay(image, sav)
	if err != nil {
		return err
	}
	defer closeAll()
	if rep.Changes, err = Diff(bfs, afs); err != nil {
		return err
	}
	if opts.Dir == "" {
		if opts.Dir, err = osDriveDir(rep.Changes); err != nil {
			return err
		}
	}
	if err := extract(rep, afs, opts); err != nil {
		return err
	}
	return exportRegistry(rep, bfs, afs, opts)
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
		if err := extract(rep, afs, opts); err != nil {
			return err
		}
		return exportRegistry(rep, bfs, afs, opts)
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
