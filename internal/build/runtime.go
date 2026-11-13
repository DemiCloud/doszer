package build

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/DemiCloud/doszer/internal/manifest"
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

// addRuntimeFiles generates §6.8's files, replacing any file of the same name.
func addRuntimeFiles(fs *fileSet, m *manifest.Manifest, effectiveName string) error {
	if NeedsAutoboot(&m.Boot) {
		fs.set("AUTOBOOT.DBP", dataEntry(BuildAutoboot(&m.Boot), Epoch))
	}

	if m.Boot.Parent != "" {
		fs.set(m.Boot.Parent+".parent", dataEntry([]byte{}, Epoch))
	}

	data, err := effectiveManifest(m, effectiveName)
	if err != nil {
		return err
	}
	fs.set(manifest.FileName, dataEntry(data, Epoch))

	return nil
}
