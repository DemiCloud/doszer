package config

import (
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// docs describe each setting in a written config file.
var docs = map[string]string{
	"paths.roms":        "Archives the window lists, at any depth: one folder, or a list of them.\nDefault: ./roms, roms/ beside doszer, RetroDECK's ROM folder.",
	"paths.system":      "DOSBox Pure's system directory: OS images and shells.\nDefault: ./system, system/ beside doszer, RetroDECK's BIOS folder.",
	"paths.saves":       "DOSBox Pure's save directory.\nDefault: ./saves, saves/ beside doszer, RetroDECK's (run), doszer's cache.",
	"defaults.os":       "init's OS image for Windows 9x games, in the system directory, without extension.",
	"defaults.shell":    "init's shell for Windows 3.x games, in the system directory, without extension.",
	"build.ogg_quality": "Ogg Vorbis quality for CD audio, -1 to 10.",
	"build.redump_dat":  "Datfile dump verifies against instead of Redump's.",
	"install.os":        "Images install sessions boot, by boot.os: image name = install image.",
	"install.templates": "Pristine OS images install uses: image name = template file.",
}

// Template returns a config file listing every setting with its
// description, in its table. Settings in keys are written with set's
// values; the rest are commented out, showing the built-in default where
// there is one.
func Template(set Config, keys map[string]bool) string {
	var b strings.Builder
	b.WriteString("# doszer configuration (README \"Configuration\").\n# Relative paths are relative to this file.\n")
	def := Default()
	table := ""
	for _, k := range Keys {
		i := strings.LastIndexByte(k, '.')
		t, name := k[:i], k[i+1:]
		if t != table {
			b.WriteString("\n[" + t + "]\n")
			table = t
		} else {
			b.WriteString("\n")
		}
		for _, l := range strings.Split(docs[k], "\n") {
			b.WriteString("# " + l + "\n")
		}
		var v, dv any
		switch k {
		case "paths.roms":
			v, dv = []string(set.Paths.ROMs), ""
			if len(set.Paths.ROMs) == 1 {
				v = set.Paths.ROMs[0]
			}
		case "build.ogg_quality":
			v, dv = set.Build.OggQuality, def.Build.OggQuality
		default:
			v, dv = *field(&set, k), ""
		}
		if keys[k] {
			b.WriteString(line(name, v))
		} else {
			b.WriteString("# " + line(name, dv))
		}
	}
	for _, t := range tables {
		m := set.Install.OS
		if t == "install.templates" {
			m = set.Install.Templates
		}
		b.WriteString("\n# " + docs[t] + "\n")
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

// line is k = v as TOML (strings, string lists and ints can't fail to
// encode).
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
