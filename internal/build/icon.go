package build

import (
	"bytes"
	"fmt"
	"io"
	"os"

	"github.com/DemiCloud/doszer/internal/icon"
	"github.com/DemiCloud/doszer/internal/manifest"
)

// IconName is the archive's icon (§6.8.4).
const IconName = "DOSZ.ICO"

// mediaUses returns extracts plus a reference for [icon] media, for
// locateMedia: a media the icon reads must be found, and its data track
// kept, as for an [[extract]].
func mediaUses(m *manifest.Manifest) []manifest.Extract {
	uses := m.Extract
	if m.Icon != nil && m.Icon.Media != "" {
		uses = append(append([]manifest.Extract{}, uses...), manifest.Extract{Media: m.Icon.Media, From: m.Icon.Path})
	}
	return uses
}

// iconFromMedia makes DOSZ.ICO from [icon] path in its media's data track
// file system (§5.11).
func iconFromMedia(media map[string]resolvedMedia, ic *manifest.Icon, tempDir string, tempFiles *[]*os.File) ([]byte, error) {
	rm, ok := media[ic.Media]
	if !ok {
		return nil, fmt.Errorf("icon: media %q not found", ic.Media)
	}
	t, err := openDataTrack(rm, tempDir, tempFiles)
	if err != nil {
		return nil, fmt.Errorf("icon: media %q: %w", ic.Media, err)
	}
	defer t.f.Close()
	e, err := t.img.Lookup(ic.Path)
	if err != nil {
		return nil, fmt.Errorf("icon: media %q: %s: %w", ic.Media, ic.Path, err)
	}
	if e.IsDir {
		return nil, fmt.Errorf("icon: media %q: %s is a directory", ic.Media, ic.Path)
	}
	off, n, err := t.img.Extent(e)
	if err != nil {
		return nil, err
	}
	if off+n > t.size {
		return nil, fmt.Errorf("icon: media %q: %s extends past the end of the image", ic.Media, ic.Path)
	}
	data, err := icon.Make(io.NewSectionReader(t.f, t.off+off, n), n, ic.Index)
	if err != nil {
		return nil, fmt.Errorf("icon: media %q: %s: %w", ic.Media, ic.Path, err)
	}
	return data, nil
}

// addIcon generates DOSZ.ICO (§6.8.4) when the manifest has [icon]: from
// its path in the build, or, for [icon] media, from fromMedia, which
// collect made. Without fromMedia (repack or set, whose media may not be
// readable) an existing DOSZ.ICO is kept.
func addIcon(fs *fileSet, m *manifest.Manifest, fromMedia []byte) error {
	ic := m.Icon
	if ic == nil {
		return nil
	}
	data := fromMedia
	if ic.Media == "" {
		e, ok := fs.get(ic.Path)
		if !ok {
			return fmt.Errorf("icon: %s is not in the build", ic.Path)
		}
		b, err := e.bytes()
		if err != nil {
			return fmt.Errorf("icon: %s: %w", ic.Path, err)
		}
		if data, err = icon.Make(bytes.NewReader(b), int64(len(b)), ic.Index); err != nil {
			return fmt.Errorf("icon: %s: %w", ic.Path, err)
		}
	}
	if data == nil {
		if _, ok := fs.get(IconName); ok {
			return nil
		}
		return fmt.Errorf("icon: media %q not found, and the archive has no %s to keep", ic.Media, IconName)
	}
	fs.set(IconName, dataEntry(data, Epoch))
	return nil
}

// BuildIcon returns the DOSZ.ICO the manifest implies from the archive
// file read gives (§6.8.4), for verify: nil, nil without [icon], or for
// [icon] media, which verify can't always read.
func BuildIcon(m *manifest.Manifest, read func(path string) ([]byte, error)) ([]byte, error) {
	if m.Icon == nil || m.Icon.Media != "" {
		return nil, nil
	}
	b, err := read(m.Icon.Path)
	if err != nil {
		return nil, fmt.Errorf("icon: %s: %w", m.Icon.Path, err)
	}
	data, err := icon.Make(bytes.NewReader(b), int64(len(b)), m.Icon.Index)
	if err != nil {
		return nil, fmt.Errorf("icon: %s: %w", m.Icon.Path, err)
	}
	return data, nil
}
