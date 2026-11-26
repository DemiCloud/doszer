package env

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/DemiCloud/doszer/internal/chain"
	"github.com/DemiCloud/doszer/internal/config"
	"github.com/DemiCloud/doszer/internal/install"
	"github.com/DemiCloud/doszer/internal/interrupt"
	"github.com/DemiCloud/doszer/internal/status"
)

// PortableOptions say how Portable sets up a directory.
type PortableOptions struct {
	Dir      string        // the portable directory
	Settings config.Config // written to its doszer.toml, directories left out
	Keys     map[string]bool
	Copy     bool          // copy files from the directories below
	From     config.Config // whose system, save and ROM directories Copy reads
	DryRun   bool
	Force    bool // overwrite an existing doszer.toml
}

// PortableAction is one thing Portable did, or would do.
type PortableAction struct {
	Action string `json:"action" yaml:"action" toml:"action"` // create, write, copy, skip
	Path   string `json:"path" yaml:"path" toml:"path"`
	Size   int64  `json:"size,omitempty" yaml:"size,omitempty" toml:"size,omitempty"`
	Why    string `json:"why,omitempty" yaml:"why,omitempty" toml:"why,omitempty"`
}

// Portable sets up o.Dir as a portable doszer: the executable, a
// doszer.toml (which makes it portable: config.PortableConfigPath) and
// system/, saves/ and roms/, which it finds by default. With o.Copy it
// copies in the shells and OS images (and sound fonts, MT-32 ROMs) of
// o.From's system directory, the archives under its ROM directories with
// their parent archives, and those archives' saves. A file already there with the same size is
// skipped, so a second run only copies what's new.
func Portable(o PortableOptions) ([]PortableAction, error) {
	var acts []PortableAction
	dir, err := filepath.Abs(o.Dir)
	if err != nil {
		return nil, err
	}
	for _, d := range []string{dir, filepath.Join(dir, "system"), filepath.Join(dir, "saves"), filepath.Join(dir, "roms")} {
		if isDir(d) {
			continue
		}
		acts = append(acts, PortableAction{Action: "create", Path: d})
		if !o.DryRun {
			if err := os.MkdirAll(d, 0o755); err != nil {
				return acts, err
			}
		}
	}

	// The executable, unless it already runs from dir.
	if exe, err := os.Executable(); err == nil {
		if r, err := filepath.EvalSymlinks(exe); err == nil {
			exe = r
		}
		dst := filepath.Join(dir, filepath.Base(exe))
		if !sameFile(exe, dst) {
			a, err := copyFile(exe, dst, o.DryRun, 0o755)
			if err != nil {
				return acts, err
			}
			acts = append(acts, a)
		}
	}

	cfgPath := filepath.Join(dir, config.FileName)
	keys := map[string]bool{}
	for k, v := range o.Keys {
		if v && !strings.HasPrefix(k, "paths.") {
			keys[k] = true // directories: the portable ones, by default
		}
	}
	switch _, err := os.Stat(cfgPath); {
	case err == nil && !o.Force:
		acts = append(acts, PortableAction{Action: "skip", Path: cfgPath, Why: "exists"})
	default:
		acts = append(acts, PortableAction{Action: "write", Path: cfgPath})
		if !o.DryRun {
			if err := os.WriteFile(cfgPath, []byte(config.Template(o.Settings, keys)), 0o644); err != nil {
				return acts, err
			}
		}
	}

	if !o.Copy {
		return acts, nil
	}
	sys := config.SystemDirOf(o.From, "").Path
	type job struct{ src, dst string }
	var jobs []job

	if sys != "" && !sameFile(sys, filepath.Join(dir, "system")) {
		images := map[string]bool{}
		for _, f := range install.SystemFiles(sys) {
			images[filepath.Join(sys, filepath.FromSlash(f.File))] = true
		}
		filepath.WalkDir(sys, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !images[p] && !midiFile(p) {
				return nil
			}
			rel, _ := filepath.Rel(sys, p)
			jobs = append(jobs, job{p, filepath.Join(dir, "system", rel)})
			return nil
		})
	}
	saved := map[string]string{} // portable save name -> archive, to report clashes
	queued := map[string]bool{}
	for _, rd := range config.ROMDirsOf(o.From, "") {
		roms := rd.Path
		if sameFile(roms, filepath.Join(dir, "roms")) {
			continue
		}
		filepath.WalkDir(roms, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.EqualFold(filepath.Ext(p), ".dosz") {
				return nil
			}
			rel, _ := filepath.Rel(roms, p)
			// The archive and its parents (a shared shell archive), each
			// beside the one naming it, as DOSBox Pure looks them up.
			for i, l := range chain.Resolve(p).Links {
				if (i > 0 && !l.Found) || queued[l.Path] {
					continue // the archive itself goes even if it can't be read
				}
				queued[l.Path] = true
				lrel, _ := filepath.Rel(roms, l.Path)
				jobs = append(jobs, job{l.Path, filepath.Join(dir, "roms", lrel)})
			}
			name := strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
			for _, s := range saveFiles(o.From, p, name) {
				base := filepath.Base(s)
				if other, ok := saved[base]; ok {
					acts = append(acts, PortableAction{Action: "skip", Path: s, Why: "the same save name as " + other + "'s"})
					continue
				}
				saved[base] = rel
				jobs = append(jobs, job{s, filepath.Join(dir, "saves", base)})
			}
			return nil
		})
	}
	for i, j := range jobs {
		status.Step("Copying %s", filepath.Base(j.src))
		status.Count(i, len(jobs))
		a, err := copyFile(j.src, j.dst, o.DryRun, 0o644)
		if err != nil {
			return acts, err
		}
		acts = append(acts, a)
	}
	return acts, nil
}

// midiFile reports whether p is a sound font or an MT-32/CM-32L ROM,
// which DOSBox Pure also finds in its system directory.
func midiFile(p string) bool {
	base := strings.ToLower(filepath.Base(p))
	ext := filepath.Ext(base)
	return ext == ".sf2" || ext == ".sf3" || ext == ".rom" && (strings.HasPrefix(base, "mt32") || strings.HasPrefix(base, "cm32l"))
}

// saveFiles returns archive's saves under name in the directory run uses
// for it: <name>.pure.zip, <name>-CDRIVE.sav and <name>-<HASH>.sav
// (DOSBox Pure's DBP_GetSaveFile).
func saveFiles(cfg config.Config, archive, name string) []string {
	dir := cfg.Paths.Saves
	if dir == "" {
		dir, _ = install.PlaySaveDirNoCreate(archive)
	}
	if dir == "" {
		dir, _ = config.CacheSaveDir()
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() {
			continue
		}
		if n == name+".pure.zip" || strings.HasPrefix(n, name+"-") && strings.HasSuffix(strings.ToLower(n), ".sav") {
			out = append(out, filepath.Join(dir, n))
		}
	}
	return out
}

// copyFile copies src to dst unless dst already has src's size.
func copyFile(src, dst string, dryRun bool, mode os.FileMode) (PortableAction, error) {
	fi, err := os.Stat(src)
	if err != nil {
		return PortableAction{}, err
	}
	if di, err := os.Stat(dst); err == nil && di.Size() == fi.Size() {
		return PortableAction{Action: "skip", Path: dst, Size: fi.Size(), Why: "already there"}, nil
	}
	a := PortableAction{Action: "copy", Path: dst, Size: fi.Size()}
	if dryRun {
		return a, nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return a, err
	}
	in, err := os.Open(src)
	if err != nil {
		return a, err
	}
	defer in.Close()
	tmp := dst + ".part"
	defer interrupt.Remove(tmp)()
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return a, err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return a, fmt.Errorf("copying %s: %w", src, err)
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return a, err
	}
	os.Chtimes(tmp, fi.ModTime(), fi.ModTime())
	return a, os.Rename(tmp, dst)
}

func sameFile(a, b string) bool {
	fa, err1 := os.Stat(a)
	fb, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(fa, fb)
}
