package install

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/DemiCloud/doszer/internal/status"
	"github.com/DemiCloud/doszer/internal/w9xreg"
)

// RegistryFile is the REGEDIT4 file install writes into the extracted
// folder: the registry entries the installer added for its program.
const RegistryFile = "INSTALL.REG"

// hives are the Windows 9x registry files install compares, and which of
// their keys belong to an installed program: its own keys under Software,
// not Windows' (Microsoft, and Description\Microsoft\Rpc's timestamps) or
// file associations (Classes), which Windows changes on its own during
// any session.
var hives = []struct {
	file  string // on C:
	under string // keys exported: under this, outside skip
	skip  []string
	strip string // removed from a key's path
	root  string // the .reg file's root key
}{
	{"WINDOWS/SYSTEM.DAT", `Software\`, []string{`Software\Microsoft\`, `Software\Classes\`, `Software\Description\`}, "", "HKEY_LOCAL_MACHINE"},
	// Without user profiles, HKEY_CURRENT_USER is USER.DAT's .DEFAULT.
	{"WINDOWS/USER.DAT", `.DEFAULT\Software\`, []string{`.DEFAULT\Software\Microsoft\`, `.DEFAULT\Software\Classes\`, `.DEFAULT\Software\Description\`}, `.DEFAULT\`, "HKEY_CURRENT_USER"},
}

// registryAdded lists the program registry entries after (C: after the
// session) has that before (the OS image) doesn't, and how many values
// they hold.
func registryAdded(before, after fs.FS) ([]w9xreg.Group, int, error) {
	var groups []w9xreg.Group
	values := 0
	for _, h := range hives {
		a, err := fs.ReadFile(after, h.file)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, 0, err
		}
		b, err := fs.ReadFile(before, h.file)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, 0, err
		}
		if bytes.Equal(a, b) {
			continue
		}
		ak, err := w9xreg.Parse(a)
		if err != nil {
			return nil, 0, fmt.Errorf("%s: %w", h.file, err)
		}
		bk := &w9xreg.Key{}
		if b != nil {
			if bk, err = w9xreg.Parse(b); err != nil {
				return nil, 0, fmt.Errorf("%s: %w", h.file, err)
			}
		}
		g := w9xreg.Group{Root: h.root}
		for _, e := range w9xreg.Added(bk, ak) {
			if !hasPrefixFold(e.Key+`\`, h.under) || len(e.Key) < len(h.under) {
				continue
			}
			skipped := false
			for _, s := range h.skip {
				skipped = skipped || hasPrefixFold(e.Key+`\`, s)
			}
			if skipped {
				continue
			}
			e.Key = e.Key[len(h.strip):]
			g.Entries = append(g.Entries, e)
			if e.Value != nil {
				values++
			}
		}
		if len(g.Entries) > 0 {
			groups = append(groups, g)
		}
	}
	return groups, values, nil
}

// archiveDrive is where an OS image archive's own files appear.
const archiveDrive = "D:"

// retarget moves string values' paths into dir (e.g. C:\GAMES), whole
// path components only, to archiveDrive: the same length, so only the
// drive letter changes.
func retarget(groups []w9xreg.Group, dir string) {
	needle := []byte(strings.ToLower(dir))
	for _, g := range groups {
		for _, e := range g.Entries {
			v := e.Value
			if v == nil || (v.Type != w9xreg.TypeSZ && v.Type != w9xreg.TypeExpandSZ && v.Type != w9xreg.TypeMultiSZ) {
				continue
			}
			lower := bytes.ToLower(v.Data)
			for i := 0; i+len(needle) <= len(lower); i++ {
				if !bytes.HasPrefix(lower[i:], needle) {
					continue
				}
				if end := i + len(needle); end < len(lower) && !strings.ContainsRune("\\\x00\" ;,", rune(lower[end])) {
					continue
				}
				if i > 0 && isPathByte(lower[i-1]) {
					continue // part of a longer name, e.g. XC:\GAMES
				}
				v.Data[i] = archiveDrive[0]
			}
		}
	}
}

func isPathByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '_'
}

func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

// exportRegistry writes the registry entries the installer added
// (registryAdded) into the extracted folder as RegistryFile, for the
// game's WINDOWS.BAT to import at startup: the archive boots the OS image
// as it was, without them. The extracted folder is on D: in the archive
// (the OS image model's content drive) wherever it was installed, so
// paths into it extracted from C: are moved to D: (retarget), and the
// report names RegistryFile's D: path. Like Extract, it won't replace a
// differing file without opts.Force, and writes nothing in a dry run.
func exportRegistry(rep *Report, before, after fs.FS, opts Options) error {
	if rep.Extracted == nil || rep.Dir == "" {
		return nil
	}
	status.Step("Comparing the Windows registry")
	groups, values, err := registryAdded(before, after)
	if err != nil || len(groups) == 0 {
		return err
	}
	if !strings.EqualFold(rep.Drive, archiveDrive) {
		retarget(groups, DOSPath(rep.Drive, rep.Dir))
	}
	var buf bytes.Buffer
	if err := w9xreg.WriteReg(&buf, groups...); err != nil {
		return err
	}
	target := filepath.Join(opts.Source, filepath.FromSlash(rep.Dir), RegistryFile)
	old, err := os.ReadFile(target)
	switch {
	case err == nil && bytes.Equal(old, buf.Bytes()):
	case err == nil && !opts.Force:
		return fmt.Errorf("would overwrite %s, which differs (use --force)", target)
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return err
	case !opts.DryRun:
		if err := os.WriteFile(target, buf.Bytes(), 0o644); err != nil {
			return err
		}
	}
	rep.Registry = DOSPath(archiveDrive, rep.Dir+"/"+RegistryFile)
	rep.RegistryValues = values
	return nil
}
