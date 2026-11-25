// Package env implements `doszer env`: what doszer finds where it runs -
// its configuration files, DOSBox Pure's system and save directories and
// where each came from, the shells and OS images in the system directory,
// the configured ones, the Redump datfile and ffmpeg. It reports this
// machine only: archives may be built here and played elsewhere.
package env

import (
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/DemiCloud/doszer/internal/config"
	"github.com/DemiCloud/doszer/internal/install"
	"github.com/DemiCloud/doszer/internal/redump"
	"github.com/DemiCloud/doszer/internal/vhd"
)

// Report is what Read found.
type Report struct {
	Config    []ConfigFile `json:"config" yaml:"config" toml:"config"`
	SystemDir Dir          `json:"system_dir" yaml:"system_dir" toml:"system_dir"`
	SaveDir   Dir          `json:"save_dir" yaml:"save_dir" toml:"save_dir"`
	RetroDECK *RetroDECK   `json:"retrodeck,omitempty" yaml:"retrodeck,omitempty" toml:"retrodeck,omitempty"`
	Shells    []Image      `json:"shells,omitempty" yaml:"shells,omitempty" toml:"shells,omitempty"`
	Images    []Image      `json:"images,omitempty" yaml:"images,omitempty" toml:"images,omitempty"`
	Settings  []Setting    `json:"settings,omitempty" yaml:"settings,omitempty" toml:"settings,omitempty"`
	Redump    Redump       `json:"redump" yaml:"redump" toml:"redump"`
	FFmpeg    string       `json:"ffmpeg,omitempty" yaml:"ffmpeg,omitempty" toml:"ffmpeg,omitempty"`
	Cache     string       `json:"cache,omitempty" yaml:"cache,omitempty" toml:"cache,omitempty"`
}

// ConfigFile is one configuration layer's file.
type ConfigFile struct {
	Path  string `json:"path" yaml:"path" toml:"path"`
	Found bool   `json:"found" yaml:"found" toml:"found"`
}

// Dir is a directory and where it came from.
type Dir struct {
	Path string `json:"path,omitempty" yaml:"path,omitempty" toml:"path,omitempty"`
	From string `json:"from,omitempty" yaml:"from,omitempty" toml:"from,omitempty"`
}

// RetroDECK is RetroDECK's folders, when it is installed.
type RetroDECK struct {
	BIOS  string `json:"bios,omitempty" yaml:"bios,omitempty" toml:"bios,omitempty"`
	ROMs  string `json:"roms,omitempty" yaml:"roms,omitempty" toml:"roms,omitempty"`
	Saves string `json:"saves,omitempty" yaml:"saves,omitempty" toml:"saves,omitempty"`
}

// Image is a shell or OS image in the system directory.
type Image struct {
	Name  string `json:"name" yaml:"name" toml:"name"` // as boot.shell and boot.os take it
	File  string `json:"file" yaml:"file" toml:"file"`
	Size  int64  `json:"size" yaml:"size" toml:"size"`
	Kind  string `json:"kind" yaml:"kind" toml:"kind"` // shell, fixed VHD, dynamic VHD, raw image
	Note  string `json:"note,omitempty" yaml:"note,omitempty" toml:"note,omitempty"`
	shell bool
}

// Setting is a configured shell or image name, and whether the system
// directory has it.
type Setting struct {
	Key   string `json:"key" yaml:"key" toml:"key"` // default_shell, default_os, install_os.<name>
	Value string `json:"value" yaml:"value" toml:"value"`
	Found bool   `json:"found" yaml:"found" toml:"found"`
}

// Redump is the Redump datfiles available.
type Redump struct {
	Embedded string `json:"embedded,omitempty" yaml:"embedded,omitempty" toml:"embedded,omitempty"` // version
	Cached   string `json:"cached,omitempty" yaml:"cached,omitempty" toml:"cached,omitempty"`       // version
	Checked  string `json:"checked,omitempty" yaml:"checked,omitempty" toml:"checked,omitempty"`    // when the cached copy was last fetched or checked
	External string `json:"external,omitempty" yaml:"external,omitempty" toml:"external,omitempty"` // redump_dat
}

// Read gathers the report. systemDir and saveDir are what --system-dir
// and --save-dir gave, "" if not given.
func Read(cfg config.Config, systemDir, saveDir string) (*Report, error) {
	r := &Report{}
	if p, err := config.UserConfigPath(); err == nil {
		r.Config = append(r.Config, ConfigFile{p, exists(p)})
	}
	if abs, err := filepath.Abs(config.FileName); err == nil {
		r.Config = append(r.Config, ConfigFile{abs, exists(abs)})
	}

	switch {
	case systemDir != "":
		r.SystemDir = Dir{systemDir, "--system-dir"}
	case cfg.SystemDir != "":
		r.SystemDir = Dir{cfg.SystemDir, "system_dir"}
	default:
		d, from := install.FindSystemDirFrom()
		r.SystemDir = Dir{d, from}
	}
	if r.SystemDir.Path != "" && !isDir(r.SystemDir.Path) {
		return nil, fmt.Errorf("system directory %s (%s): not a directory", r.SystemDir.Path, r.SystemDir.From)
	}

	bios, roms, saves := install.RetroDECK()
	if bios != "" || roms != "" || saves != "" {
		r.RetroDECK = &RetroDECK{bios, roms, saves}
	}
	switch {
	case saveDir != "":
		r.SaveDir = Dir{saveDir, "--save-dir"}
	case cfg.SaveDir != "":
		r.SaveDir = Dir{cfg.SaveDir, "save_dir"}
	default:
		if d, from := install.LocalSaveDir(); d != "" {
			r.SaveDir = Dir{d, from}
		} else if cache, err := os.UserCacheDir(); err == nil {
			r.SaveDir = Dir{filepath.Join(cache, "doszer", "saves"), "doszer's cache"}
		}
	}

	files := install.SystemFiles(r.SystemDir.Path)
	for _, f := range files {
		img := Image{Name: f.Name, File: f.File, Size: f.Size, shell: f.Shell}
		if f.Shell {
			img.Kind = "shell"
			r.Shells = append(r.Shells, img)
			continue
		}
		img.Kind, img.Note = imageKind(filepath.Join(r.SystemDir.Path, filepath.FromSlash(f.File)))
		r.Images = append(r.Images, img)
	}
	has := func(name string, shell bool) bool {
		for _, f := range files {
			if f.Shell == shell && f.Name == name {
				return true
			}
		}
		return false
	}
	if cfg.DefaultShell != "" {
		r.Settings = append(r.Settings, Setting{"default_shell", cfg.DefaultShell, has(cfg.DefaultShell, true)})
	}
	if cfg.DefaultOS != "" {
		r.Settings = append(r.Settings, Setting{"default_os", cfg.DefaultOS, has(cfg.DefaultOS, false)})
	}
	for _, k := range slices.Sorted(maps.Keys(cfg.InstallOS)) {
		r.Settings = append(r.Settings, Setting{"install_os." + k, cfg.InstallOS[k], has(cfg.InstallOS[k], false)})
	}

	if emb, err := redump.Embedded(); err == nil && emb != nil {
		r.Redump.Embedded = emb.Version
	}
	if cached, checked, err := redump.Cached(); err == nil && cached != nil {
		r.Redump.Cached, r.Redump.Checked = cached.Version, checked.Format("2006-01-02 15:04")
	}
	r.Redump.External = cfg.RedumpDAT

	if p, err := exec.LookPath("ffmpeg"); err == nil {
		r.FFmpeg = p
	}
	if cache, err := os.UserCacheDir(); err == nil {
		r.Cache = filepath.Join(cache, "doszer")
	}
	return r, nil
}

// imageKind tells a fixed VHD from a dynamic one, or a raw image.
func imageKind(p string) (kind, note string) {
	if !strings.EqualFold(filepath.Ext(p), ".vhd") {
		return "raw image", ""
	}
	d, err := vhd.Open(p)
	if err != nil {
		return "VHD", "unreadable: " + err.Error()
	}
	defer d.Close()
	if d.Type() == vhd.TypeDynamic {
		return "dynamic VHD", "install refuses it (DOSBox Pure loses writes to it); prepare os writes a fixed copy"
	}
	return "fixed VHD", ""
}

// Print writes r as text.
func Print(w io.Writer, r *Report) {
	line := func(k, format string, a ...any) {
		if k != "" {
			k += ":"
		}
		fmt.Fprintf(w, "%-12s %s\n", k, fmt.Sprintf(format, a...))
	}
	for i, c := range r.Config {
		k := ""
		if i == 0 {
			k = "Config"
		}
		state := "not found"
		if c.Found {
			state = "found"
		}
		line(k, "%s (%s)", c.Path, state)
	}
	dir := func(k string, d Dir) {
		if d.Path == "" {
			line(k, "none found")
			return
		}
		line(k, "%s (%s)", d.Path, d.From)
	}
	dir("System dir", r.SystemDir)
	dir("Save dir", r.SaveDir)
	if rd := r.RetroDECK; rd != nil {
		if rd.ROMs != "" && rd.Saves != "" && r.SaveDir.From != "--save-dir" && r.SaveDir.From != "save_dir" && r.SaveDir.From != "./saves" && r.SaveDir.From != "beside doszer" {
			line("", "run: %s for archives under %s (RetroDECK)", filepath.Join(rd.Saves, "<folder>"), rd.ROMs)
		}
		line("RetroDECK", "BIOS %s, ROMs %s, saves %s", orNone(rd.BIOS), orNone(rd.ROMs), orNone(rd.Saves))
	}
	images := func(k string, list []Image) {
		if len(list) == 0 {
			if r.SystemDir.Path != "" {
				line(k, "none")
			}
			return
		}
		width := 0
		for _, i := range list {
			width = max(width, len([]rune(i.Name)))
		}
		for n, i := range list {
			kk := ""
			if n == 0 {
				kk = k
			}
			s := fmt.Sprintf("%-*s  %s, %s", width, i.Name, i.Kind, size(i.Size))
			if i.Note != "" {
				s += ": " + i.Note
			}
			line(kk, "%s", s)
		}
	}
	images("Shells", r.Shells)
	images("OS images", r.Images)
	for _, s := range r.Settings {
		state := "found"
		if !s.Found {
			state = "not in the system directory here"
		}
		line(s.Key, "%s (%s)", s.Value, state)
	}
	var dats []string
	if r.Redump.External != "" {
		dats = append(dats, "redump_dat "+r.Redump.External+" (in use)")
	}
	if r.Redump.Cached != "" {
		dats = append(dats, "cached "+r.Redump.Cached+", checked "+r.Redump.Checked)
	}
	if r.Redump.Embedded != "" {
		dats = append(dats, "embedded "+r.Redump.Embedded)
	}
	if len(dats) == 0 {
		dats = append(dats, "none (fetched on the first dump)")
	}
	line("Redump", "%s", strings.Join(dats, "; "))
	if r.FFmpeg != "" {
		line("ffmpeg", "%s", r.FFmpeg)
	} else {
		line("ffmpeg", "not found (needed to build Ogg Vorbis audio)")
	}
	if r.Cache != "" {
		line("Cache", "%s", r.Cache)
	}
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func isDir(p string) bool { fi, err := os.Stat(p); return err == nil && fi.IsDir() }

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func size(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.2f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
}
