// Package config loads doszer's configuration (README "Configuration"),
// merging, lowest precedence first: built-in defaults, the user config file
// (doszer.toml beside the executable when portable, else
// $XDG_CONFIG_HOME's), ./doszer.toml, and a source's own DOSZ.TOML
// [x-doszer] table. Command-line flags, the highest layer, are applied by
// the caller. It also finds the directories doszer uses when none is set
// (dirs.go).
package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/pelletier/go-toml/v2"

	"github.com/DemiCloud/doszer/internal/manifest"
)

// FileName is the config file name looked for in the working directory and
// beside the executable.
const FileName = "doszer.toml"

// Config holds every doszer setting (README "Configuration").
type Config struct {
	SystemDir  string            `toml:"system_dir"`
	SaveDir    string            `toml:"save_dir"`
	ROMDir     string            `toml:"rom_dir"` // archives the window lists
	OggQuality int               `toml:"ogg_quality"`
	Templates  map[string]string `toml:"templates"`
	InstallOS  map[string]string `toml:"install_os"` // OS image install sessions boot, by boot.os
	RedumpDAT  string            `toml:"redump_dat"` // datfile dump verifies against instead of Redump's (internal/redump)

	// init's shell and OS image for a game whose programs need Windows 3.x
	// or Windows 9x: paths in the system directory without extension, as
	// boot.shell and boot.os take them.
	DefaultShell string `toml:"default_shell"`
	DefaultOS    string `toml:"default_os"`
}

// pathKeys are the settings holding a host path: a relative one is
// relative to the directory of the file that sets it.
var pathKeys = []string{"system_dir", "save_dir", "rom_dir", "redump_dat"}

// Default returns doszer's built-in defaults.
func Default() Config {
	return Config{OggQuality: 6}
}

// Layer is one configuration file, lowest precedence first in Layers.
type Layer struct {
	Path     string `json:"path" yaml:"path" toml:"path"`
	Kind     string `json:"kind" yaml:"kind" toml:"kind"` // user, portable, cwd, manifest
	Found    bool   `json:"found" yaml:"found" toml:"found"`
	manifest bool   // a DOSZ.TOML: settings under [x-doszer]
}

// Layers returns the configuration files Load reads for sourceDir, lowest
// precedence first, whether or not they exist. ./doszer.toml is left out
// when it is the portable config itself.
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
	if cwd, err := filepath.Abs(FileName); err == nil && !sameFile(cwd, user) {
		out = append(out, Layer{Path: cwd, Kind: "cwd"})
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

func sameFile(a, b string) bool {
	if a == b {
		return true
	}
	fa, err1 := os.Stat(a)
	fb, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(fa, fb)
}

// Load returns the effective configuration for sourceDir, merging Layers
// onto Default(). sourceDir may be "" to skip its manifest. A layer file
// that doesn't exist is not an error.
func Load(sourceDir string) (Config, error) {
	cfg, _, err := LoadOrigins(sourceDir)
	return cfg, err
}

// LoadOrigins is Load, also returning which file set each key: setting
// key ("system_dir", "templates.<name>") to the layer's path.
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

// XDGConfigPath returns $XDG_CONFIG_HOME/doszer/config.toml, or
// ~/.config/doszer/config.toml if XDG_CONFIG_HOME is unset.
func XDGConfigPath() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
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
	var keys []string
	for k, v := range raw {
		if t, ok := v.(map[string]any); ok {
			for name := range t {
				keys = append(keys, k+"."+name)
				if k == "templates" {
					v := cfg.Templates[name]
					resolve(&v)
					cfg.Templates[name] = v
				}
			}
			continue
		}
		keys = append(keys, k)
	}
	for _, k := range pathKeys {
		if _, ok := raw[k]; ok {
			resolve(field(cfg, k))
		}
	}
	sort.Strings(keys)
	return keys, nil
}

// field returns the string setting k of cfg, or nil.
func field(cfg *Config, k string) *string {
	switch k {
	case "system_dir":
		return &cfg.SystemDir
	case "save_dir":
		return &cfg.SaveDir
	case "rom_dir":
		return &cfg.ROMDir
	case "redump_dat":
		return &cfg.RedumpDAT
	case "default_shell":
		return &cfg.DefaultShell
	case "default_os":
		return &cfg.DefaultOS
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
