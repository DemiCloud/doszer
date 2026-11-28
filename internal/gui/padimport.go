package gui

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/DemiCloud/doszer/internal/manifest"
	"github.com/DemiCloud/doszer/internal/padmap"
)

// importPads applies a mapping file to the ports being edited and says
// what it did:
//   - a manifest (a .toml) gives its [[input.pad]], labels and all;
//   - an archive (.dosz, .dosp, .zip) gives its manifest's, else its
//     PADMAP.DBP labelled by its DOS.YML;
//   - a PADMAP.DBP gives its bindings, labelled as the ports already
//     label the same actions (it has no labels of its own);
//   - a DOS.YML labels the ports' bindings, or, with none, is port 1.
//
// Files applied one after another add up: a PADMAP.DBP and a DOS.YML
// make one labelled mapping, in either order.
func importPads(cur []padmap.Pad, path string) ([]padmap.Pad, string, error) {
	name := filepath.Base(path)
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".dosz", ".dosp", ".zip":
		files, err := zipFiles(path, manifest.FileName, "PADMAP.DBP", "DOS.YML")
		if err != nil {
			return nil, "", err
		}
		if data := files[manifest.FileName]; data != nil {
			if m, err := manifest.Parse(data, ""); err == nil && len(m.Input.Pad) > 0 {
				pads := m.Pads()
				return pads, fmt.Sprintf("%s from %s's manifest", plural(len(pads), "port"), name), nil
			}
		}
		if data := files["PADMAP.DBP"]; data != nil {
			pads, err := padmap.DecodePADMAP(data)
			if err != nil {
				return nil, "", fmt.Errorf("%s: %w", name, err)
			}
			from := cur
			if yml := files["DOS.YML"]; yml != nil {
				from = append(from, padmap.ParseDOSYML(yml))
			}
			return padmap.Label(pads, from...), fmt.Sprintf("%s from %s's PADMAP.DBP", plural(len(pads), "port"), name), nil
		}
		if yml := files["DOS.YML"]; yml != nil {
			return applyYML(cur, yml, name+"'s DOS.YML")
		}
		return nil, "", fmt.Errorf("%s has no gamepad mapping", name)
	case ".toml":
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, "", err
		}
		m, err := manifest.Parse(data, "")
		if err != nil {
			return nil, "", err
		}
		if len(m.Input.Pad) == 0 {
			return nil, "", fmt.Errorf("%s has no [[input.pad]]", name)
		}
		pads := m.Pads()
		return pads, fmt.Sprintf("%s from %s", plural(len(pads), "port"), name), nil
	case ".dbp":
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, "", err
		}
		pads, err := padmap.DecodePADMAP(data)
		if err != nil {
			return nil, "", err
		}
		return padmap.Label(pads, cur...), fmt.Sprintf("%s from %s", plural(len(pads), "port"), name), nil
	case ".yml", ".yaml":
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, "", err
		}
		return applyYML(cur, data, name)
	}
	return nil, "", fmt.Errorf("%s: not a gamepad mapping (a DOSZ.TOML, PADMAP.DBP, DOS.YML or an archive)", name)
}

// applyYML labels cur's bindings from a DOS.YML, or, if cur binds
// nothing, makes it port 1.
func applyYML(cur []padmap.Pad, data []byte, name string) ([]padmap.Pad, string, error) {
	yml := padmap.ParseDOSYML(data)
	if len(yml.Controls) == 0 && len(yml.Wheel) == 0 {
		return nil, "", fmt.Errorf("%s has no input lines", name)
	}
	for _, p := range cur {
		if len(p.Controls) > 0 || len(p.Wheel) > 0 {
			return padmap.Label(cur, yml), "labels from " + name, nil
		}
	}
	return []padmap.Pad{yml}, "port 1 from " + name, nil
}

// zipFiles reads the named root entries of an archive (matched
// case-insensitively); a missing one is nil.
func zipFiles(path string, names ...string) (map[string][]byte, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	out := map[string][]byte{}
	for _, f := range zr.File {
		for _, n := range names {
			if !strings.EqualFold(f.Name, n) {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			data, err := io.ReadAll(io.LimitReader(rc, 16<<20))
			rc.Close()
			if err != nil {
				return nil, err
			}
			out[n] = data
		}
	}
	return out, nil
}

// padImport imports a mapping file into the open editor.
func (a *app) padImport(path string) {
	e := a.padEd
	if e == nil {
		return
	}
	pads, what, err := importPads(e.pads, path)
	if err != nil {
		var pe *os.PathError
		if errors.As(err, &pe) {
			err = pe.Err
		}
		a.alert("Can't import "+filepath.Base(path), err.Error())
		return
	}
	for i := range pads {
		if pads[i].Controls == nil {
			pads[i].Controls = map[string]string{}
		}
	}
	if len(pads) == 0 {
		pads = []padmap.Pad{{Controls: map[string]string{}}}
	}
	e.pads, e.port, e.wheelSel, e.changed = pads, 0, 0, true
	a.selectPadControl(e, e.sel)
	a.say("Imported "+what, false)
}
