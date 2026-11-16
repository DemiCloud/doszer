package build

import (
	"errors"
	"fmt"
	"strings"

	"github.com/DemiCloud/doszer/internal/manifest"
)

// shellForbidden are the shell-owned files §11 forbids under the shell model.
var shellForbidden = []string{
	"WINDOWS/WIN.INI", "WINDOWS/SYSTEM.INI", "WINDOWS/PROGMAN.INI", "WINDOWS/*.GRP",
}

// ValidateArchiveContent enforces §11's archive-content rules, and §8.2's
// rule that every entry is named by a path as §2 defines it, against a
// path listing: a build in progress, or an already-built archive's entry
// names (`doszer verify`). A directory entry (a name ending in "/") is
// left to the caller, which reports it as one (§8.2).
func ValidateArchiveContent(paths []string, model manifest.Model) error {
	var errs []error

	for _, p := range paths {
		if strings.HasSuffix(p, "/") {
			continue
		}
		if err := manifest.CheckPath(p); err != nil {
			errs = append(errs, fmt.Errorf("entry name %v (§8.2)", err))
		}
	}

	if model == manifest.ModelShell {
		for _, pattern := range shellForbidden {
			for _, p := range paths {
				if matchGlob(pattern, p) {
					errs = append(errs, fmt.Errorf("%s: shell-owned file not allowed in the archive under the shell model", p))
				}
			}
		}
	}

	if model == manifest.ModelShell || model == manifest.ModelOS {
		for _, p := range paths {
			if matchGlob("DOSBOX.BAT", p) {
				errs = append(errs, fmt.Errorf("%s: DOSBOX.BAT not allowed in the archive under the %s model", p, model))
			}
		}
	}

	return errors.Join(errs...)
}
