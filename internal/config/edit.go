package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Keys are the settings, in display order. The tables take a name:
// install.os.<name>, install.templates.<name>.
var Keys = []string{"paths.roms", "paths.system", "paths.saves", "defaults.os", "defaults.shell", "build.ogg_quality", "build.redump_dat"}

// Setting is one setting's effective value and where it came from: a
// file's path, or "default" (with the default's own source, if it was
// found: "default: beside doszer"). paths.roms has a Setting per folder.
type Setting struct {
	Key   string `json:"key" yaml:"key" toml:"key"`
	Value string `json:"value" yaml:"value" toml:"value"`
	From  string `json:"from" yaml:"from" toml:"from"`
}

// Settings returns every setting's effective value for sourceDir ("" for
// none), unset directories shown with the default doszer would use.
func Settings(sourceDir string) ([]Setting, error) {
	cfg, origins, err := LoadOrigins(sourceDir)
	if err != nil {
		return nil, err
	}
	def := Default()
	from := func(k string, d Dir) string {
		if o := origins[k]; o != "" {
			return o
		}
		if d.From != "" {
			return "default: " + d.From
		}
		return "default"
	}
	var out []Setting
	for _, k := range Keys {
		switch k {
		case "paths.roms":
			dirs := ROMDirsOf(cfg, "")
			if len(dirs) == 0 {
				out = append(out, Setting{Key: k, From: "default"})
			}
			for _, d := range dirs {
				out = append(out, Setting{Key: k, Value: d.Path, From: from(k, d)})
			}
		case "paths.system":
			d := SystemDirOf(cfg, "")
			out = append(out, Setting{Key: k, Value: d.Path, From: from(k, d)})
		case "paths.saves":
			d := SaveDirOf(cfg, "")
			out = append(out, Setting{Key: k, Value: d.Path, From: from(k, d)})
		case "build.ogg_quality":
			s := Setting{Key: k, Value: strconv.Itoa(cfg.Build.OggQuality), From: origins[k]}
			if s.From == "" && cfg.Build.OggQuality == def.Build.OggQuality {
				s.From = "default"
			}
			out = append(out, s)
		default:
			out = append(out, Setting{Key: k, Value: *field(&cfg, k), From: from(k, Dir{})})
		}
	}
	for _, t := range tables {
		m := cfg.Install.OS
		if t == "install.templates" {
			m = cfg.Install.Templates
		}
		names := make([]string, 0, len(m))
		for n := range m {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			out = append(out, Setting{Key: t + "." + n, Value: m[n], From: origins[t+"."+n]})
		}
	}
	return out, nil
}

// splitKey checks key and splits it into its table and name:
// "paths.system" is system in [paths], "install.os.X" X in [install.os].
func splitKey(key string) (table, name string, err error) {
	for _, t := range tables {
		if n, ok := strings.CutPrefix(key, t+"."); ok && n != "" {
			return t, n, nil
		}
	}
	for _, k := range Keys {
		if k == key {
			i := strings.LastIndexByte(k, '.')
			return k[:i], k[i+1:], nil
		}
	}
	return "", "", fmt.Errorf("unknown setting %q (settings: %s, install.os.NAME, install.templates.NAME)", key, strings.Join(Keys, ", "))
}

// isPathKey reports whether key holds host paths.
func isPathKey(key string) bool {
	if strings.HasPrefix(key, "install.templates.") {
		return true
	}
	for _, k := range pathKeys {
		if k == key {
			return true
		}
	}
	return false
}

// Set writes key = values into the config file path, creating it if
// missing and keeping every other line, comments included. Only
// paths.roms takes several values (written as an array). A relative
// path value is taken from the working directory, and stored relative to
// the file when it lies under the file's directory (so a portable
// doszer.toml stays portable).
func Set(path, key string, values ...string) error {
	table, name, err := splitKey(key)
	if err != nil {
		return err
	}
	if len(values) == 0 {
		return fmt.Errorf("%s: no value", key)
	}
	if len(values) > 1 && key != "paths.roms" {
		return fmt.Errorf("%s takes one value", key)
	}
	var v any = values[0]
	switch {
	case key == "build.ogg_quality":
		n, err := strconv.Atoi(values[0])
		if err != nil || n < -1 || n > 10 {
			return fmt.Errorf("build.ogg_quality: %q is not a number from -1 to 10", values[0])
		}
		v = n
	case isPathKey(key):
		stored := make([]string, len(values))
		for i, p := range values {
			if stored[i], err = storedPath(path, p); err != nil {
				return err
			}
		}
		v = stored[0]
		if len(stored) > 1 {
			v = stored
		}
	}
	line, err := toml.Marshal(map[string]any{name: v})
	if err != nil {
		return err
	}
	return edit(path, table, name, strings.TrimRight(string(line), "\n"))
}

// Unset removes key from the config file path. A key the file doesn't set
// is not an error.
func Unset(path, key string) error {
	table, name, err := splitKey(key)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	return edit(path, table, name, "")
}

// storedPath returns p as Set stores it in the file at cfgPath.
func storedPath(cfgPath, p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	dir, err := filepath.Abs(filepath.Dir(cfgPath))
	if err != nil {
		return "", err
	}
	if rel, err := filepath.Rel(dir, abs); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
		return filepath.ToSlash(rel), nil
	}
	return abs, nil
}

var (
	headerRe = regexp.MustCompile(`^\s*\[\s*([^\]]*?)\s*\]`)
	keyRe    = regexp.MustCompile(`^\s*("(?:[^"\\]|\\.)*"|'[^']*'|[A-Za-z0-9_-]+)\s*=`)
)

// edit replaces the line setting name in table ("" for top level) with
// line, removes it if line is "", or adds line where it belongs. The
// result must still load, or nothing is written.
func edit(path, table, name, line string) error {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	var lines []string
	if text != "" {
		lines = strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	}

	section := ""     // current table
	found := -1       // the line setting name
	sectionEnd := -1  // last key line of table, or its header
	firstHeader := -1 // the first table header
	for i, l := range lines {
		if m := headerRe.FindStringSubmatch(l); m != nil {
			section = strings.Trim(m[1], `"'`)
			if firstHeader < 0 {
				firstHeader = i
			}
			if section == table {
				sectionEnd = i
			}
			continue
		}
		if section != table {
			continue
		}
		m := keyRe.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		sectionEnd = i
		k := m[1]
		if uq, err := strconv.Unquote(k); err == nil && strings.HasPrefix(k, `"`) {
			k = uq
		} else {
			k = strings.Trim(k, "'")
		}
		if k == name {
			found = i
		}
	}

	switch {
	case found >= 0 && line == "":
		lines = append(lines[:found], lines[found+1:]...)
	case found >= 0:
		lines[found] = line
	case line == "":
		return nil
	case sectionEnd >= 0:
		lines = insert(lines, sectionEnd+1, line)
	case table == "":
		at := len(lines)
		if firstHeader >= 0 {
			at = firstHeader
			for at > 0 && strings.TrimSpace(lines[at-1]) == "" {
				at--
			}
		}
		lines = insert(lines, at, line)
	default:
		if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
			lines = append(lines, "")
		}
		lines = append(lines, "["+table+"]", line)
	}

	out := []byte(strings.Join(lines, "\n") + "\n")
	var cfg Config
	if err := toml.NewDecoder(bytes.NewReader(out)).DisallowUnknownFields().Decode(&cfg); err != nil {
		return fmt.Errorf("%s: the change would not load: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}

func insert(lines []string, at int, l string) []string {
	lines = append(lines, "")
	copy(lines[at+1:], lines[at:])
	lines[at] = l
	return lines
}

// CheckKey returns an error if key is not a setting.
func CheckKey(key string) error {
	_, _, err := splitKey(key)
	return err
}
