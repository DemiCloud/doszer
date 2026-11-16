package build

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/DemiCloud/doszer/internal/manifest"
	"github.com/DemiCloud/doszer/internal/padmap"
)

// NeedsAutoboot reports whether boot settings require an AUTOBOOT.DBP (§6.8).
func NeedsAutoboot(b *manifest.Boot) bool {
	return b.Exec != "" || b.Shell != "" || b.OS != "" || b.Disc != "" || b.Skip != nil
}

// BuildAutoboot renders AUTOBOOT.DBP per §6.8.1.
func BuildAutoboot(b *manifest.Boot) []byte {
	line1 := ""
	switch {
	case b.Exec != "":
		line1 = b.Exec
	case b.Shell != "":
		line1 = "S*" + b.Shell
	case b.OS != "":
		line1 = "O*" + b.OS
	}

	line2 := ""
	if b.Skip != nil {
		line2 = strconv.Itoa(*b.Skip)
	}

	lines := []string{line1, line2, b.Disc}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	return []byte(strings.Join(lines, "\r\n"))
}

// BuildPadmap renders PADMAP.DBP (§6.8.2), or returns nil if the manifest
// has no [[input.pad]].
func BuildPadmap(m *manifest.Manifest) []byte {
	if len(m.Input.Pad) == 0 {
		return nil
	}
	return padmap.EncodePADMAP(m.Pads())
}

// BuildDOSYML renders DOS.YML (§6.8.3) over existing (the build's DOS.YML,
// or nil), or returns nil if the first [[input.pad]] entry has no binding
// DOS.YML can express.
func BuildDOSYML(m *manifest.Manifest, existing []byte) []byte {
	if len(m.Input.Pad) == 0 {
		return nil
	}
	lines := padmap.YMLLines(m.Input.Pad[0].Padmap())
	if len(lines) == 0 {
		return nil
	}
	return padmap.MergeDOSYML(existing, lines)
}

// effectiveManifest re-serializes m with the effective name set (§8.3).
func effectiveManifest(m *manifest.Manifest, name string) ([]byte, error) {
	effective := *m
	effective.Name = name

	data, err := toml.Marshal(&effective)
	if err != nil {
		return nil, fmt.Errorf("marshal effective manifest: %w", err)
	}
	return data, nil
}

// addRuntimeFiles generates §6.8's files, replacing any file of the same
// name - except DOS.YML, whose own lines other than input mappings are kept.
func addRuntimeFiles(fs *fileSet, m *manifest.Manifest, effectiveName string) error {
	if NeedsAutoboot(&m.Boot) {
		fs.set("AUTOBOOT.DBP", dataEntry(BuildAutoboot(&m.Boot), Epoch))
	}

	if m.Boot.Parent != "" {
		fs.set(m.Boot.Parent+".parent", dataEntry([]byte{}, Epoch))
	}

	if data := BuildPadmap(m); data != nil {
		fs.set("PADMAP.DBP", dataEntry(data, Epoch))
	}

	var existing []byte
	if e, ok := fs.get("DOS.YML"); ok {
		b, err := e.peek()
		if err != nil {
			return fmt.Errorf("DOS.YML: %w", err)
		}
		existing = b
	}
	if data := BuildDOSYML(m, existing); data != nil {
		fs.set("DOS.YML", dataEntry(data, Epoch))
	}

	data, err := effectiveManifest(m, effectiveName)
	if err != nil {
		return err
	}
	fs.set(manifest.FileName, dataEntry(data, Epoch))

	return nil
}
