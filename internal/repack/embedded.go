package repack

import (
	"archive/zip"
	"errors"
	"io"
	"strings"

	"github.com/DemiCloud/doszer/internal/manifest"
)

// ErrNoManifest means an archive has no embedded DOSZ.TOML (one not
// built by a DOSZ implementation).
var ErrNoManifest = errors.New("no embedded manifest")

// EmbeddedManifest returns an archive's DOSZ.TOML (spec §8.3) and, for
// editing it beside the manifest, the archive's own WINDOWS.BAT, if any.
func EmbeddedManifest(path string) (toml, windowsBat []byte, err error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, nil, err
	}
	defer zr.Close()
	read := func(f *zip.File) ([]byte, error) {
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(io.LimitReader(rc, 4<<20))
	}
	for _, f := range zr.File {
		switch {
		case f.Name == manifest.FileName:
			if toml, err = read(f); err != nil {
				return nil, nil, err
			}
		case strings.EqualFold(f.Name, "WINDOWS.BAT"):
			if windowsBat, err = read(f); err != nil {
				return nil, nil, err
			}
		}
	}
	if toml == nil {
		return nil, windowsBat, ErrNoManifest
	}
	return toml, windowsBat, nil
}
