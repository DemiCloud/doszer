// Package lint finds what makes an archive worse without making it
// wrong: verify checks it against the specification, lint against
// good practice (an icon, identified discs, a boot system new enough
// for its programs, the right extension).
package lint

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/DemiCloud/doszer/internal/chain"
	"github.com/DemiCloud/doszer/internal/info"
	"github.com/DemiCloud/doszer/internal/install"
	"github.com/DemiCloud/doszer/internal/platform"
)

// Finding is one thing lint found.
type Finding struct {
	Check   string `json:"check" yaml:"check" toml:"check"` // the check's name: manifest, icon, media, audio, chain, os, type, start
	Message string `json:"message" yaml:"message" toml:"message"`
}

// Checks are the checks' names, in the order they run.
var Checks = []string{"manifest", "icon", "media", "audio", "chain", "os", "type", "start"}

// Check lints the archive at path; systemDir is where shells and OS
// images are (empty: the chain's system isn't looked for).
func Check(path, systemDir string) ([]Finding, error) {
	r, err := info.Read(path)
	if err != nil {
		return nil, err
	}
	c := chain.Resolve(path)
	c.Locate(systemDir)
	users := 0
	for _, u := range chain.UsedBy(filepath.Dir(path), path) {
		if l := chain.Resolve(u).Links; len(l) > 1 && strings.EqualFold(l[1].Path, c.Links[0].Path) {
			users++
		}
	}
	return check(r, c, users, systemDir != ""), nil
}

// check is Check over what it read: r the archive's report, c its chain,
// users how many archives beside it name it as their parent, located
// whether c's system was looked for.
func check(r *info.Report, c *chain.Chain, users int, located bool) []Finding {
	var out []Finding
	add := func(check, format string, a ...any) {
		out = append(out, Finding{check, fmt.Sprintf(format, a...)})
	}
	parent := strings.EqualFold(filepath.Ext(r.Path), ".dosp")

	if r.Manifest == nil {
		add("manifest", "no embedded manifest (DOSZ.TOML): repack --manifest adds one")
	}
	if !parent && r.Icon == "" {
		add("icon", "no icon (DOSZ.ICO)")
	}
	for _, d := range r.Discs {
		if d.Media == nil {
			add("media", "%s: not identified by a [[source.media]] entry", d.Path)
		}
		if d.WAV > 0 {
			add("audio", "%s: %s in WAV (Ogg Vorbis is smaller)", d.Path, plural(d.WAV, "audio track"))
		}
	}
	if b := c.Broken(); b != "" && (located || (c.Kind != chain.KindShell && c.Kind != chain.KindOS)) {
		add("chain", "%s", b)
	}

	// What its own programs need against what it boots.
	need := r.Need.Game
	if r.Programs > 0 && !r.Need.DOS && need.Platform != platform.NotProgram && need.Platform != platform.DOS {
		switch c.Kind {
		case chain.KindDOS:
			if c.BootFrom >= 0 || !parent {
				add("os", "its programs need %s; it boots DOS", need)
			}
		case chain.KindShell, chain.KindOS:
			have := install.GuessOS(install.SystemFile{Name: c.Boot, Shell: c.Kind == chain.KindShell})
			if have.Platform != platform.NotProgram && !install.Satisfies(have, need) {
				add("os", "its programs need %s; it boots %s (%s)", need, c.Boot, have)
			}
		}
	}

	switch {
	case !parent && users > 0 && r.Programs == 0:
		add("type", "a parent of %s with no programs of its own: doszer convert makes it .dosp", plural(users, "archive"))
	case parent && users == 0:
		add("type", "a parent no archive beside it uses")
	}
	if !parent && c.Kind == chain.KindDOS && c.Boot == "" && r.Programs > 0 {
		add("start", "no program to start (boot.exec): it opens DOSBox Pure's start menu")
	}
	return out
}

func plural(n int, s string) string {
	if n == 1 {
		return "1 " + s
	}
	return fmt.Sprintf("%d %ss", n, s)
}
