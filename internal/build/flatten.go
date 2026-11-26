package build

import (
	"strings"

	"github.com/DemiCloud/doszer/internal/manifest"
)

// recordFlatten records in m what flattening (§6.3) changed, for
// Options.Flatten: flatten turned on, the rewrite rules removing the
// collapsed directory names from the text files naming them as paths
// (§6.5), and boot.exec, boot.disc and icon.path moved to where flatten
// put their files (moved: old path -> new). So the manifest says exactly
// what was done, and building it again (or repacking the archive) gives
// the same output: a flattened tree has nothing left to flatten.
func recordFlatten(m *manifest.Manifest, moved map[string]string, rules []manifest.Rewrite) {
	m.Layout.Flatten = true
	m.Layout.Rewrite = append(m.Layout.Rewrite, rules...)
	to := func(p string) (string, bool) {
		for from, to := range moved {
			if strings.EqualFold(from, p) {
				return to, true
			}
		}
		return "", false
	}
	if prog, args, _ := strings.Cut(m.Boot.Exec, " "); len(prog) > 3 && strings.EqualFold(prog[:3], `C:\`) {
		if p, ok := to(strings.ReplaceAll(prog[3:], `\`, "/")); ok {
			m.Boot.Exec = `C:\` + strings.ReplaceAll(p, "/", `\`)
			if args != "" {
				m.Boot.Exec += " " + args
			}
		}
	}
	if p, ok := to(m.Boot.Disc); ok {
		m.Boot.Disc = p
	}
	if m.Icon != nil && m.Icon.Media == "" {
		if p, ok := to(m.Icon.Path); ok {
			m.Icon.Path = p
		}
	}
}
