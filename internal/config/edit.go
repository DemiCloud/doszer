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

// Keys are the scalar settings, in display order. templates and
// install_os take a name: templates.<name>, install_os.<name>.
var Keys = []string{"system_dir", "save_dir", "rom_dir", "ogg_quality", "redump_dat", "default_shell", "default_os"}

// tables are the settings that map names to values.
var tables = []string{"templates", "install_os"}

// Setting is one setting's effective value and where it came from: a
// file's path, or "default" (with the default's own source, if it was
// found: "default: beside doszer").
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
	var out []Setting
	for _, k := range Keys {
		s := Setting{Key: k, From: origins[k]}
		switch k {
		case "ogg_quality":
			s.Value = strconv.Itoa(cfg.OggQuality)
			if s.From == "" && cfg.OggQuality == def.OggQuality {
				s.From = "default"
			}
		case "system_dir", "save_dir", "rom_dir":
			var d Dir
			switch k {
			case "system_dir":
				d = SystemDirOf(cfg, "")
			case "save_dir":
				d = SaveDirOf(cfg, "")
			default:
				d = ROMDirOf(cfg, "")
			}
			s.Value = d.Path
			if s.From == "" {
				s.From = "default"
				if d.From != "" {
					s.From += ": " + d.From
				}
			}
		default:
			s.Value = *field(&cfg, k)
			if s.From == "" {
				s.From = "default"
			}
		}
		out = append(out, s)
	}
	for _, t := range tables {
		m := cfg.Templates
		if t == "install_os" {
			m = cfg.InstallOS
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

// splitKey checks key and splits a table key into table and name.
func splitKey(key string) (table, name string, err error) {
	for _, t := range tables {
		if n, ok := strings.CutPrefix(key, t+"."); ok && n != "" {
			return t, n, nil
		}
	}
	for _, k := range Keys {
		if k == key {
			return "", key, nil
		}
	}
	return "", "", fmt.Errorf("unknown setting %q (settings: %s, templates.NAME, install_os.NAME)", key, strings.Join(Keys, ", "))
}

// isPathKey reports whether key holds a host path.
func isPathKey(key string) bool {
	if strings.HasPrefix(key, "templates.") {
		return true
	}
	for _, k := range pathKeys {
		if k == key {
			return true
		}
	}
	return false
}

// Set writes key = value into the config file path, creating it if
// missing and keeping every other line, comments included. A relative
// path value is taken from the working directory, and stored relative to
// the file when it lies under the file's directory (so a portable
// doszer.toml stays portable).
func Set(path, key, value string) error {
	table, name, err := splitKey(key)
	if err != nil {
		return err
	}
	var v any = value
	switch {
	case key == "ogg_quality":
		n, err := strconv.Atoi(value)
		if err != nil || n < -1 || n > 10 {
			return fmt.Errorf("ogg_quality: %q is not a number from -1 to 10", value)
		}
		v = n
	case isPathKey(key):
		v, err = storedPath(path, value)
		if err != nil {
			return err
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
