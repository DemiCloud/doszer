package build

import (
	"errors"
	"fmt"

	"github.com/DemiCloud/doszer/internal/manifest"
)

// shellForbidden are the shell-owned files §11 forbids under the shell model.
var shellForbidden = []string{
	"WINDOWS/WIN.INI", "WINDOWS/SYSTEM.INI", "WINDOWS/PROGMAN.INI", "WINDOWS/*.GRP",
}

// validateArchiveContent enforces §11's archive-content rules.
func validateArchiveContent(fs *fileSet, model manifest.Model) error {
	var errs []error

	if model == manifest.ModelShell {
		for _, pattern := range shellForbidden {
			for _, p := range fs.paths() {
				if matchGlob(pattern, p) {
					errs = append(errs, fmt.Errorf("%s: shell-owned file not allowed in the archive under the shell model", p))
				}
			}
		}
	}

	if model == manifest.ModelShell || model == manifest.ModelOS {
		for _, p := range fs.paths() {
			if matchGlob("DOSBOX.BAT", p) {
				errs = append(errs, fmt.Errorf("%s: DOSBOX.BAT not allowed in the archive under the %s model", p, model))
			}
		}
	}

	return errors.Join(errs...)
}
