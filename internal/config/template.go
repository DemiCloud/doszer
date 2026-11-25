package config

import (
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// keyDocs describe each setting in a written config file.
var keyDocs = map[string]string{
	"system_dir":    "DOSBox Pure's system directory: OS images and shells.\nDefault: ./system, system/ beside doszer, RetroDECK's BIOS folder.",
	"save_dir":      "DOSBox Pure's save directory.\nDefault: ./saves, saves/ beside doszer, RetroDECK's (run), doszer's cache.",
	"rom_dir":       "Archives the window lists.\nDefault: ./roms, roms/ beside doszer, RetroDECK's ROM folder.",
	"ogg_quality":   "Ogg Vorbis quality for CD audio, -1 to 10.",
	"redump_dat":    "Datfile dump verifies against instead of Redump's.",
	"default_shell": "init's shell for Windows 3.x games, in the system directory, without extension.",
	"default_os":    "init's OS image for Windows 9x games, in the system directory, without extension.",
}

var tableDocs = map[string]string{
	"templates":  "Pristine OS images install uses: image name = template file.",
	"install_os": "Images install sessions boot, by boot.os: image name = install image.",
}

// Template returns a config file listing every setting with its
// description. Settings in set are written as values; the rest are
// commented out, showing the built-in default where there is one.
func Template(set Config, keys map[string]bool) string {
	var b strings.Builder
	b.WriteString("# doszer configuration (README \"Configuration\").\n# Relative paths are relative to this file.\n")
	def := Default()
	for _, k := range Keys {
		b.WriteString("\n")
		for _, l := range strings.Split(keyDocs[k], "\n") {
			b.WriteString("# " + l + "\n")
		}
		var v, dv any
		switch k {
		case "ogg_quality":
			v, dv = set.OggQuality, def.OggQuality
		default:
			v, dv = *field(&set, k), ""
		}
		if keys[k] {
			b.WriteString(line(k, v))
			continue
		}
		if k == "ogg_quality" {
			b.WriteString("# " + line(k, dv))
		} else {
			b.WriteString("# " + k + " = ''\n")
		}
	}
	for _, t := range tables {
		m := set.Templates
		if t == "install_os" {
			m = set.InstallOS
		}
		b.WriteString("\n# " + tableDocs[t] + "\n")
		var names []string
		for n := range m {
			if keys[t+"."+n] {
				names = append(names, n)
			}
		}
		sort.Strings(names)
		if len(names) == 0 {
			b.WriteString("# [" + t + "]\n# 'path/to/image' = ''\n")
			continue
		}
		b.WriteString("[" + t + "]\n")
		for _, n := range names {
			b.WriteString(line(n, m[n]))
		}
	}
	return b.String()
}

// line is k = v as TOML (string and int values can't fail to encode).
func line(k string, v any) string {
	out, _ := toml.Marshal(map[string]any{k: v})
	return string(out)
}

// Current returns the settings the config files set for sourceDir ("" for
// none), and which keys they set, for Template.
func Current(sourceDir string) (Config, map[string]bool, error) {
	cfg, origins, err := LoadOrigins(sourceDir)
	keys := map[string]bool{}
	for k := range origins {
		keys[k] = true
	}
	return cfg, keys, err
}
