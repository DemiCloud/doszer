package build

import (
	"fmt"
	"path"
	"strings"

	"github.com/DemiCloud/doszer/internal/cue"
	"github.com/DemiCloud/doszer/internal/manifest"
)

// renameMediaCue renames each CUE sheet in fs that matches a declared
// [[source.media]] entry's track layout to the effective name (§6.4), in
// the same directory, updating boot.disc in place if it referenced the old
// name. convertMedia then names the sheet's track files after it.
//
// Matching is structural only - track count and the audio/data sequence,
// read straight from the CUE text - not by re-hashing tracks: build already
// verified hashes during collect (§7.3), and repack treats the archive's
// existing content as already identified, same as the rest of §6.1.
func renameMediaCue(fs *fileSet, media []manifest.Media, boot *manifest.Boot, effectiveName string) error {
	if len(media) == 0 {
		return nil
	}

	type candidate struct {
		path  string
		shape []bool // IsAudio per track, in order
	}
	var candidates []candidate
	for _, p := range fs.paths() {
		if !strings.EqualFold(path.Ext(p), ".cue") {
			continue
		}
		e, _ := fs.get(p)
		data, err := e.peek()
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		sheet, err := cue.Parse(data)
		if err != nil {
			continue // not a valid cue sheet: not a rename candidate
		}
		candidates = append(candidates, candidate{path: p, shape: trackShape(sheet)})
	}

	used := map[string]bool{}
	multi := len(media) > 1

	for _, m := range media {
		newName := effectiveName
		if multi {
			newName = fmt.Sprintf("%s (%s)", effectiveName, m.ID)
		}
		// A matching sheet that already has the name comes first, so
		// another match isn't renamed onto it.
		ordered := make([]candidate, 0, len(candidates))
		for _, named := range []bool{true, false} {
			for _, c := range candidates {
				if strings.EqualFold(path.Base(c.path), newName+".cue") == named {
					ordered = append(ordered, c)
				}
			}
		}
		for _, c := range ordered {
			if used[c.path] {
				continue
			}
			if !matchesShape(c.shape, m.Tracks) {
				continue
			}

			// Same directory: the sheet's FILE names are relative to it.
			newPath := path.Join(path.Dir(c.path), newName+".cue")

			used[c.path] = true
			if !strings.EqualFold(c.path, newPath) {
				if _, exists := fs.get(newPath); exists {
					return fmt.Errorf("%s: renaming it to %s would replace an existing file (§6.4)", c.path, newPath)
				}
				e, _ := fs.get(c.path)
				fs.delete(c.path)
				fs.set(newPath, e)
				if boot.Disc != "" && strings.EqualFold(boot.Disc, cueLabel(c.path)) {
					boot.Disc = cueLabel(newPath)
				}
			}
			break
		}
	}

	return nil
}

// trackShape lists whether each track in sheet, in order across all FILEs,
// is a CD-DA audio track.
func trackShape(sheet *cue.Sheet) []bool {
	var shape []bool
	for _, f := range sheet.Files {
		for _, t := range f.Tracks {
			shape = append(shape, t.IsAudio())
		}
	}
	return shape
}

// matchesShape reports whether shape (a CUE's audio/data track sequence)
// matches declared's.
func matchesShape(shape []bool, declared []manifest.Track) bool {
	if len(shape) != len(declared) {
		return false
	}
	for i, d := range declared {
		if (d.Type == manifest.TrackAudio) != shape[i] {
			return false
		}
	}
	return true
}

// cueLabel is a cue path's AUTOBOOT.DBP disc label: its base name without
// extension (DOSBox Pure's ReadAutoBoot omits it, same as O*/S*).
func cueLabel(p string) string {
	return strings.TrimSuffix(path.Base(p), path.Ext(p))
}
