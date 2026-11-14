package install

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/DemiCloud/doszer/internal/build"
	"github.com/DemiCloud/doszer/internal/manifest"
	"github.com/DemiCloud/doszer/internal/vdisk"
	"github.com/DemiCloud/doszer/internal/zipw"
)

// Session is one install run's temporary content.
type Session struct {
	Name    string // content name: also the prefix of every save file the run creates
	Content string // the temporary .dosz
	Dir     string // directory holding it, removed by Close
	Model   manifest.Model
	OS      string // OS image path for the OS image model (boot.os)
	Skipped []string
}

// frontendOverrides are the per-content option overrides an OS image
// session writes to FRONTEND.DBP, which standalone DOSBox Pure applies
// over its own configuration (dosbox_pure_libretro.cpp, init_dosbox,
// DBPS_ApplyConfigOverrides). "Save Difference Per Content" keeps the OS
// image untouched and puts C: changes in <name>-CDRIVE.sav; the D: free
// space is pinned because it seeds the D: save's hash.
const frontendOverrides = `{"dosbox_pure_bootos_ramdisk":"diff","dosbox_pure_bootos_dfreespace":"1024"}`

// sessionExcluded are source files left out of the session's content:
// the manifest, and files that would change how the session starts
// (DOSBOX.BAT bypasses AUTOBOOT.DBP).
var sessionExcluded = []string{manifest.FileName, "AUTOBOOT.DBP", "FRONTEND.DBP", "DOSBOX.BAT"}

// Prepare writes a session's content: every file in source, stored
// uncompressed for speed, plus an AUTOBOOT.DBP that boots the manifest's
// OS image or shell (the plain model starts at DOSBox Pure's start menu,
// to run the installer from) and, for an OS image, FRONTEND.DBP.
func Prepare(source string, m *manifest.Manifest) (*Session, error) {
	abs, err := filepath.Abs(source)
	if err != nil {
		return nil, err
	}
	// Beside the source, so DOSBox Pure can reach it wherever the source
	// is reachable (e.g. a Windows drive under WSL).
	dir, err := os.MkdirTemp(filepath.Dir(abs), ".doszer-install-")
	if err != nil {
		return nil, err
	}
	var id [4]byte
	rand.Read(id[:])
	s := &Session{
		Name:  "doszer-install-" + hex.EncodeToString(id[:]),
		Dir:   dir,
		Model: m.Model(),
		OS:    m.Boot.OS,
	}
	s.Content = filepath.Join(dir, s.Name+".dosz")
	if err := s.writeContent(abs, m); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func (s *Session) writeContent(source string, m *manifest.Manifest) error {
	f, err := os.Create(s.Content)
	if err != nil {
		return err
	}
	zw := zipw.New(f)

	var paths []string
	err = filepath.WalkDir(source, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(source, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		for _, x := range sessionExcluded {
			if strings.EqualFold(rel, x) {
				if x != manifest.FileName {
					s.Skipped = append(s.Skipped, rel)
				}
				return nil
			}
		}
		paths = append(paths, rel)
		return nil
	})
	if err == nil {
		for _, rel := range paths {
			if err = addFile(zw, source, rel); err != nil {
				break
			}
		}
	}
	if err == nil {
		switch s.Model {
		case manifest.ModelOS:
			err = addData(zw, "AUTOBOOT.DBP", build.BuildAutoboot(&manifest.Boot{OS: m.Boot.OS}))
			if err == nil {
				err = addData(zw, "FRONTEND.DBP", []byte(frontendOverrides))
			}
		case manifest.ModelShell:
			err = addData(zw, "AUTOBOOT.DBP", build.BuildAutoboot(&manifest.Boot{Shell: m.Boot.Shell}))
		}
	}
	if err == nil {
		err = zw.Close()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

func addFile(zw *zipw.Writer, source, rel string) error {
	p := filepath.Join(source, filepath.FromSlash(rel))
	info, err := os.Stat(p)
	if err != nil {
		return err
	}
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	return zw.Add(rel, f, true, info.ModTime())
}

func addData(zw *zipw.Writer, name string, data []byte) error {
	return zw.Add(name, strings.NewReader(string(data)), true, build.Epoch)
}

// Close removes the session's temporary content.
func (s *Session) Close() error {
	return os.RemoveAll(s.Dir)
}

// SaveFiles are the save files a session can produce.
type SaveFiles struct {
	PureZip string // <name>.pure.zip: C: changes, shell and plain models
	DDrive  string // <name>-<HASH>.sav: D: changes, OS image model
	CDrive  string // <name>-CDRIVE.sav: C: changes, OS image model
}

// SaveFiles returns the save file paths for this session in saveDir. The
// D: save's name needs the session content's disk hash, which disk gives.
func (s *Session) SaveFiles(saveDir string, disk *vdisk.Disk) SaveFiles {
	sf := SaveFiles{
		PureZip: filepath.Join(saveDir, s.Name+".pure.zip"),
		CDrive:  filepath.Join(saveDir, s.Name+"-CDRIVE.sav"),
	}
	if disk != nil {
		sf.DDrive = filepath.Join(saveDir, disk.SaveName(s.Name))
	}
	return sf
}

// osImagePath finds the OS image boot.os names in systemDir: DOSBox Pure
// matches it with the extension omitted (ReadAutoBoot).
func osImagePath(systemDir, osPath string) (string, error) {
	for _, ext := range []string{".vhd", ".img", ".VHD", ".IMG"} {
		p := filepath.Join(systemDir, filepath.FromSlash(osPath)+ext)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("OS image %s not found in %s", osPath, systemDir)
}
