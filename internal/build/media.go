package build

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/DemiCloud/doszer/internal/cue"
	"github.com/DemiCloud/doszer/internal/manifest"
)

// resolvedMedia is a declared [[source.media]] entry matched to the CUE
// sheet in sourceDir whose tracks verify against it (§7.3).
type resolvedMedia struct {
	Dir    string // directory containing the CUE sheet (its FILEs' base)
	Tracks []cue.ResolvedTrack
}

// locateMedia matches every declared media entry to a candidate CUE sheet
// found under sourceDir, verifying all of its track hashes (§7.3). It
// returns an error naming the first media it cannot verify.
func locateMedia(sourceDir string, declared []manifest.Media) (map[string]resolvedMedia, error) {
	candidates, err := findCueSheets(sourceDir)
	if err != nil {
		return nil, err
	}

	resolved := make(map[string]resolvedMedia, len(declared))
	for _, media := range declared {
		m, err := matchMedia(media, candidates)
		if err != nil {
			return nil, err
		}
		resolved[media.ID] = m
	}
	return resolved, nil
}

// matchMedia finds the candidate whose tracks verify against media, per
// §7.3: any candidate with the same track count and types but a different
// hash is a more specific, and more likely, failure than "not found".
func matchMedia(media manifest.Media, candidates []string) (resolvedMedia, error) {
	var shapeMatch string

	for _, path := range candidates {
		tracks, err := resolveCueFile(path)
		if err != nil {
			continue // unreadable candidate: not a match, keep looking
		}
		if !sameShape(media.Tracks, tracks) {
			continue
		}
		shapeMatch = path

		mismatch, err := verifyTracks(media.Tracks, tracks)
		if err != nil {
			return resolvedMedia{}, fmt.Errorf("source.media %q: %s: %w", media.ID, path, err)
		}
		if mismatch == -1 {
			return resolvedMedia{Dir: filepath.Dir(path), Tracks: tracks}, nil
		}
	}

	if shapeMatch != "" {
		return resolvedMedia{}, fmt.Errorf("source.media %q: %s has the right track layout but a hash mismatch", media.ID, shapeMatch)
	}
	return resolvedMedia{}, fmt.Errorf("source.media %q: no matching disc image found under %d candidate cue sheet(s)", media.ID, len(candidates))
}

// sameShape reports whether a candidate's tracks have the same count and
// data/audio sequence as a declared media's tracks.
func sameShape(declared []manifest.Track, tracks []cue.ResolvedTrack) bool {
	if len(declared) != len(tracks) {
		return false
	}
	for i, d := range declared {
		if (d.Type == manifest.TrackAudio) != tracks[i].Track.IsAudio() {
			return false
		}
	}
	return true
}

// verifyTracks hashes every track and compares it to its declared hash,
// returning the index of the first mismatch, or -1 if all match.
func verifyTracks(declared []manifest.Track, tracks []cue.ResolvedTrack) (int, error) {
	for i, d := range declared {
		got, err := tracks[i].Hash()
		if err != nil {
			return 0, fmt.Errorf("track %d: %w", i+1, err)
		}
		if got != d.SHA1 {
			return i, nil
		}
	}
	return -1, nil
}

func resolveCueFile(path string) ([]cue.ResolvedTrack, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	sheet, err := cue.Parse(data)
	if err != nil {
		return nil, err
	}
	return cue.Resolve(sheet, filepath.Dir(path))
}

// findCueSheets lists every *.cue file under sourceDir.
func findCueSheets(sourceDir string) ([]string, error) {
	var found []string
	err := filepath.WalkDir(sourceDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.EqualFold(filepath.Ext(p), ".cue") {
			found = append(found, p)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("find cue sheets: %w", err)
	}
	return found, nil
}
