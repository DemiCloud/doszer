// Package config loads doszer's configuration (README "Configuration"),
// merging, lowest precedence first: built-in defaults, the user config file,
// ./doszer.toml, and a source's own DOSZ.TOML [x-doszer] table. Command-line
// flags, the highest layer, are applied by the caller.
package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"

	"github.com/DemiCloud/doszer/internal/manifest"
)

// FileName is the config file name looked for in the working directory and
// the user config directory.
const FileName = "doszer.toml"

// Config holds every doszer setting (README "Configuration").
type Config struct {
	SystemDir   string            `toml:"system_dir"`
	SaveDir     string            `toml:"save_dir"`
	Frontend    string            `toml:"frontend"`     // "builtin" (default), "retroarch" or "standalone"
	FrontendDir string            `toml:"frontend_dir"` // the frontend's install directory
	OggQuality  int               `toml:"ogg_quality"`
	Templates   map[string]string `toml:"templates"`
	InstallOS   map[string]string `toml:"install_os"` // OS image install sessions boot, by boot.os
	RedumpDAT   string            `toml:"redump_dat"` // datfile dump verifies against instead of Redump's (internal/redump)
}

// Default returns doszer's built-in defaults.
func Default() Config {
	return Config{OggQuality: 6}
}

// Load returns the effective configuration for sourceDir, merging the user
// config file, ./doszer.toml, and sourceDir's DOSZ.TOML [x-doszer] table
// onto Default(). sourceDir may be "" to skip that layer. A layer file that
// doesn't exist is not an error.
func Load(sourceDir string) (Config, error) {
	cfg := Default()

	if path, err := UserConfigPath(); err == nil {
		if err := mergeFile(&cfg, path); err != nil {
			return cfg, err
		}
	}

	if err := mergeFile(&cfg, FileName); err != nil {
		return cfg, err
	}

	if sourceDir != "" {
		if err := mergeManifestOverride(&cfg, sourceDir); err != nil {
			return cfg, err
		}
	}

	return cfg, nil
}

// UserConfigPath returns $XDG_CONFIG_HOME/doszer/config.toml, or
// ~/.config/doszer/config.toml if XDG_CONFIG_HOME is unset.
func UserConfigPath() (string, error) {
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

// mergeFile decodes path's keys onto cfg, rejecting keys this type doesn't
// declare: path is a dedicated config file, not shared with any other
// schema.
func mergeFile(cfg *Config, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	dec := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields()
	if err := dec.Decode(cfg); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// mergeManifestOverride decodes sourceDir's DOSZ.TOML [x-doszer] table onto
// cfg. Decoding is lenient: x-doszer is a spec-reserved extension namespace
// (§5.10) the manifest package itself never validates, so every other
// manifest key is simply ignored here rather than checked.
func mergeManifestOverride(cfg *Config, sourceDir string) error {
	data, err := os.ReadFile(filepath.Join(sourceDir, manifest.FileName))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	wrapper := struct {
		X Config `toml:"x-doszer"`
	}{X: *cfg}

	if err := toml.NewDecoder(bytes.NewReader(data)).Decode(&wrapper); err != nil {
		return fmt.Errorf("%s: %w", manifest.FileName, err)
	}
	*cfg = wrapper.X
	return nil
}
