// Package config loads doszer's configuration (README "Configuration"),
// merging, lowest precedence first: built-in defaults, the user config file
// (config.toml beside the executable when portable, else
// $XDG_CONFIG_HOME's), and a source's own DOSZ.TOML [x-doszer] table. Command-line flags, the highest layer, are applied by
// the caller. It also finds the directories doszer uses when none is set
// (dirs.go).
package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/DemiCloud/doszer/internal/manifest"
)

// FileName is the config file's name: beside the executable (portable),
// or in the user config directory. Only folders doszer owns are looked
// in: a config.toml in the working directory is some other program's.
const FileName = "config.toml"

// Config holds every doszer setting (README "Configuration"), in the
// config file's tables.
type Config struct {
	Paths    Paths    `toml:"paths"`
	Defaults Defaults `toml:"defaults"`
	Build    Build    `toml:"build"`
	Install  Install  `toml:"install"`
}

// Paths are the folders doszer uses.
type Paths struct {
	ROMs    List   `toml:"roms"`    // archives the window lists: one folder or several
	System  string `toml:"system"`  // DOSBox Pure's system directory: OS images and shells
	Saves   string `toml:"saves"`   // DOSBox Pure's save directory
	Sources string `toml:"sources"` // where the window keeps the games it dumps, before building them
}

// Defaults are init's shell and OS image for a game whose programs need
// Windows 3.x or Windows 9x: paths in the system directory without
// extension, as boot.shell and boot.os take them.
type Defaults struct {
	OS    string `toml:"os"`
	Shell string `toml:"shell"`
}

// Build settings.
type Build struct {
	OggQuality int    `toml:"ogg_quality"`
	RedumpDAT  string `toml:"redump_dat"` // datfile dump verifies against instead of Redump's (internal/redump)
}

// Install settings.
type Install struct {
	OS        map[string]string `toml:"os"`        // OS image install sessions boot, by boot.os
	Templates map[string]string `toml:"templates"` // pristine OS images, by image
}

// List is a setting that takes one value or several: a string or an
// array of strings.
type List []string

// UnmarshalText takes a single string (go-toml calls it for a string;
// an array decodes as a slice).
func (l *List) UnmarshalText(b []byte) error {
	*l = List{string(b)}
	return nil
}

// pathKeys are the settings holding host paths: a relative one is
// relative to the directory of the file that sets it.
var pathKeys = []string{"paths.roms", "paths.system", "paths.saves", "paths.sources", "build.redump_dat"}

// tables are the settings that map names to values: install.os.NAME.
var tables = []string{"install.os", "install.templates"}

// Default returns doszer's built-in defaults.
func Default() Config {
	return Config{Build: Build{OggQuality: 6}}
}

// Layer is one configuration file, lowest precedence first in Layers.
type Layer struct {
	Path     string `json:"path" yaml:"path" toml:"path"`
	Kind     string `json:"kind" yaml:"kind" toml:"kind"` // user, portable, manifest
	Found    bool   `json:"found" yaml:"found" toml:"found"`
	manifest bool   // a DOSZ.TOML: settings under [x-doszer]
}

// Layers returns the configuration files Load reads for sourceDir, lowest
// precedence first, whether or not they exist.
func Layers(sourceDir string) []Layer {
	var out []Layer
	user, err := UserConfigPath()
	if err == nil {
		kind := "user"
		if Portable() {
			kind = "portable"
		}
		out = append(out, Layer{Path: user, Kind: kind})
	}
	if sourceDir != "" {
		p, _ := filepath.Abs(filepath.Join(sourceDir, manifest.FileName))
		out = append(out, Layer{Path: p, Kind: "manifest", manifest: true})
	}
	for i := range out {
		fi, err := os.Stat(out[i].Path)
		out[i].Found = err == nil && !fi.IsDir()
	}
	return out
}

// Load returns the effective configuration for sourceDir, merging Layers
// onto Default(). sourceDir may be "" to skip its manifest. A layer file
// that doesn't exist is not an error.
func Load(sourceDir string) (Config, error) {
	cfg, _, err := LoadOrigins(sourceDir)
	return cfg, err
}

// LoadOrigins is Load, also returning which file set each key: setting
// key ("paths.system", "install.templates.<name>") to the layer's path.
func LoadOrigins(sourceDir string) (Config, map[string]string, error) {
	cfg := Default()
	origins := map[string]string{}
	for _, l := range Layers(sourceDir) {
		if !l.Found {
			continue
		}
		keys, err := mergeLayer(&cfg, l)
		if err != nil {
			return cfg, origins, err
		}
		for _, k := range keys {
			origins[k] = l.Path
		}
	}
	return cfg, origins, nil
}

// UserConfigPath returns the portable config (PortableConfigPath) if there
// is one, else $XDG_CONFIG_HOME/doszer/config.toml, or
// ~/.config/doszer/config.toml if XDG_CONFIG_HOME is unset.
func UserConfigPath() (string, error) {
	if p := PortableConfigPath(); p != "" {
		return p, nil
	}
	return XDGConfigPath()
}

// XDGConfigPath returns $XDG_CONFIG_HOME/doszer/config.toml, or, if
// XDG_CONFIG_HOME is unset, %APPDATA%\doszer\config.toml on Windows and
// ~/.config/doszer/config.toml elsewhere.
func XDGConfigPath() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" && runtime.GOOS == "windows" {
		var err error
		if base, err = os.UserConfigDir(); err != nil {
			return "", err
		}
	}
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "doszer", "config.toml"), nil
}

// mergeLayer decodes l's settings onto cfg and returns the keys it set. A
// config file's unknown keys are errors: it is a dedicated file, not
// shared with any other schema. A manifest's [x-doszer] table is decoded
// leniently: x-doszer is a spec-reserved extension namespace (§5.10) the
// manifest package itself never validates.
func mergeLayer(cfg *Config, l Layer) ([]string, error) {
	data, err := os.ReadFile(l.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var raw map[string]any
	if err := toml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("%s: %w", l.Path, err)
	}
	if l.manifest {
		x, _ := raw["x-doszer"].(map[string]any)
		raw = x
		wrapper := struct {
			X Config `toml:"x-doszer"`
		}{X: *cfg}
		if err := toml.NewDecoder(bytes.NewReader(data)).Decode(&wrapper); err != nil {
			return nil, fmt.Errorf("%s: %w", l.Path, err)
		}
		*cfg = wrapper.X
	} else {
		if unknown := unknownKeys(raw); len(unknown) > 0 {
			return nil, fmt.Errorf("%s: %s", l.Path, strings.Join(unknown, "; "))
		}
		dec := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields()
		if err := dec.Decode(cfg); err != nil {
			return nil, fmt.Errorf("%s: %w", l.Path, err)
		}
	}

	base := filepath.Dir(l.Path)
	resolve := func(p *string) {
		if *p != "" && !filepath.IsAbs(*p) {
			*p = filepath.Join(base, filepath.FromSlash(*p))
		}
	}
	keys := flatten(raw, "")
	set := map[string]bool{}
	for _, k := range keys {
		set[k] = true
		if name, ok := strings.CutPrefix(k, "install.templates."); ok {
			v := cfg.Install.Templates[name]
			resolve(&v)
			cfg.Install.Templates[name] = v
		}
	}
	if set["paths.roms"] {
		for i := range cfg.Paths.ROMs {
			resolve(&cfg.Paths.ROMs[i])
		}
	}
	for _, k := range pathKeys {
		if f := field(cfg, k); f != nil && set[k] {
			resolve(f)
		}
	}
	return keys, nil
}

// flatten lists a decoded file's keys, dotted: "paths.system", and a
// table setting's entries by name ("install.os.<name>").
func flatten(m map[string]any, prefix string) []string {
	var out []string
	for k, v := range m {
		key := prefix + k
		t, isTable := v.(map[string]any)
		switch {
		case isTable && isTableKey(key):
			for name := range t {
				out = append(out, key+"."+name)
			}
		case isTable:
			out = append(out, flatten(t, key+".")...)
		default:
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

func isTableKey(k string) bool {
	for _, t := range tables {
		if t == k {
			return true
		}
	}
	return false
}

// field returns the single-string setting k of cfg, or nil.
func field(cfg *Config, k string) *string {
	switch k {
	case "paths.system":
		return &cfg.Paths.System
	case "paths.saves":
		return &cfg.Paths.Saves
	case "paths.sources":
		return &cfg.Paths.Sources
	case "build.redump_dat":
		return &cfg.Build.RedumpDAT
	case "defaults.os":
		return &cfg.Defaults.OS
	case "defaults.shell":
		return &cfg.Defaults.Shell
	}
	return nil
}

// LoadFile returns the settings of the config file path alone, over
// Default(), and the keys it sets.
func LoadFile(path string) (Config, map[string]bool, error) {
	cfg := Default()
	abs, err := filepath.Abs(path)
	if err != nil {
		return cfg, nil, err
	}
	if _, err := os.Stat(abs); err != nil {
		return cfg, nil, err
	}
	list, err := mergeLayer(&cfg, Layer{Path: abs})
	keys := map[string]bool{}
	for _, k := range list {
		keys[k] = true
	}
	return cfg, keys, err
}

// moved are the settings' names before they were grouped in tables.
var moved = map[string]string{
	"system_dir": "paths.system", "save_dir": "paths.saves", "rom_dir": "paths.roms",
	"default_os": "defaults.os", "default_shell": "defaults.shell",
	"ogg_quality": "build.ogg_quality", "redump_dat": "build.redump_dat",
	"install_os": "install.os", "templates": "install.templates",
}

// unknownKeys names a config file's keys that aren't settings, saying
// where a moved one went.
func unknownKeys(raw map[string]any) []string {
	var out []string
	for _, k := range flatten(raw, "") {
		known := false
		for _, s := range Keys {
			known = known || k == s
		}
		for _, t := range tables {
			known = known || strings.HasPrefix(k, t+".")
		}
		if known {
			continue
		}
		top, _, _ := strings.Cut(k, ".")
		if to, ok := moved[top]; ok {
			i := strings.LastIndexByte(to, '.')
			out = append(out, fmt.Sprintf("%s: moved to [%s] %s", top, to[:i], to[i+1:]))
			continue
		}
		out = append(out, fmt.Sprintf("%s: not a setting", k))
	}
	sort.Strings(out)
	return out
}
