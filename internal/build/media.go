package build

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
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
	if strings.EqualFold(filepath.Ext(path), ".iso") {
		// A bare ISO image is one data track of 2048-byte sectors: the
		// same media as a BIN/CUE dump of that disc (§7.1).
		sheet := &cue.Sheet{Files: []cue.File{{Name: filepath.Base(path), Type: "BINARY", Tracks: []cue.Track{{
			Number: 1, Mode: "MODE1/2048", Indexes: []cue.Index{{Number: 1}},
		}}}}}
		return cue.Resolve(sheet, filepath.Dir(path))
	}
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

// findCueSheets lists every disc image under sourceDir: each *.cue file,
// and each *.iso file (a single data track with no CUE sheet).
func findCueSheets(sourceDir string) ([]string, error) {
	var found []string
	err := filepath.WalkDir(sourceDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ext := filepath.Ext(p); !d.IsDir() && (strings.EqualFold(ext, ".cue") || strings.EqualFold(ext, ".iso")) {
			found = append(found, p)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("find cue sheets: %w", err)
	}
	return found, nil
}

// IdentifyMedia hashes every disc image (CUE sheet) under sourceDir into
// a [[source.media]] entry (§5.6, §7.2), for `init` to write: ids disc1,
// disc2, ... in path order, labelled with the CUE sheet's name. A CUE
// sheet that can't be read or resolved is skipped and reported in
// skipped; one with the same tracks as an earlier one is left out.
func IdentifyMedia(sourceDir string) (media []manifest.Media, skipped []string, err error) {
	cues, err := findCueSheets(sourceDir)
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(cues)
	seen := map[string]bool{}
	for _, p := range cues {
		rel, _ := filepath.Rel(sourceDir, p)
		tracks, err := resolveCueFile(p)
		if err != nil {
			skipped = append(skipped, fmt.Sprintf("%s: %v", filepath.ToSlash(rel), err))
			continue
		}
		m := manifest.Media{
			ID:    fmt.Sprintf("disc%d", len(media)+1),
			Label: strings.TrimSuffix(filepath.Base(p), filepath.Ext(p)),
		}
		var key strings.Builder
		for i, t := range tracks {
			sum, err := t.Hash()
			if err != nil {
				return nil, skipped, fmt.Errorf("%s: track %d: %w", filepath.ToSlash(rel), i+1, err)
			}
			typ := manifest.TrackData
			if t.Track.IsAudio() {
				typ = manifest.TrackAudio
			}
			m.Tracks = append(m.Tracks, manifest.Track{Type: typ, SHA1: sum})
			key.WriteString(typ + sum)
		}
		if seen[key.String()] {
			continue
		}
		seen[key.String()] = true
		media = append(media, m)
	}
	return media, skipped, nil
}
