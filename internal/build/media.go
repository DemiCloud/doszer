package build

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/DemiCloud/doszer/internal/cue"
	"github.com/DemiCloud/doszer/internal/manifest"
	"github.com/DemiCloud/doszer/internal/status"
)

// resolvedMedia is a declared [[source.media]] entry matched to the disc
// image in the build whose tracks verify against it (§7.3).
type resolvedMedia struct {
	Image  string              // the disc image's path in the build
	Tracks []cue.ResolvedTrack // FilePath is a path in fs
	fs     *fileSet
}

// openTrack streams t's raw bytes, from Start to End.
func (m resolvedMedia) openTrack(t cue.ResolvedTrack) (io.ReadCloser, error) {
	return openTrack(m.fs, t)
}

func openTrack(fs *fileSet, t cue.ResolvedTrack) (io.ReadCloser, error) {
	e, ok := fs.get(t.FilePath)
	if !ok {
		return nil, fmt.Errorf("%s: %w", t.FilePath, os.ErrNotExist)
	}
	return e.openRange(t.Start, t.End-t.Start)
}

// discImage is a candidate for §7.3: a CUE sheet or ISO image in the build.
type discImage struct {
	path   string
	tracks []cue.ResolvedTrack
	hashes []string // computed on demand, by track index
}

// hash returns track i's normalized hash, computing it once.
func (d *discImage) hash(fs *fileSet, i int) (string, error) {
	if d.hashes == nil {
		d.hashes = make([]string, len(d.tracks))
	}
	if d.hashes[i] == "" {
		rc, err := openTrack(fs, d.tracks[i])
		if err != nil {
			return "", err
		}
		defer rc.Close()
		if d.hashes[i], err = d.tracks[i].HashFrom(rc); err != nil {
			return "", err
		}
	}
	return d.hashes[i], nil
}

// locateMedia matches every declared media entry to a disc image in fs,
// verifying its track hashes (§7.3). A media that no image matches is an
// error if one of extracts references it, a warning otherwise; so is an
// image that can't be read, since it might have been the match.
func locateMedia(fs *fileSet, declared []manifest.Media, extracts []manifest.Extract) (map[string]resolvedMedia, []string, error) {
	if len(declared) == 0 {
		return nil, nil, nil
	}
	status.Step("Identifying disc images")
	images, unreadable := discImages(fs)
	used := map[string]bool{}
	for _, ex := range extracts {
		used[ex.Media] = true
	}

	resolved := make(map[string]resolvedMedia, len(declared))
	var warnings []string
	var errs []error
	for _, media := range declared {
		img, unverified, near, err := matchMedia(fs, media, images)
		if err != nil {
			return nil, warnings, fmt.Errorf("source.media %q: %w", media.ID, err)
		}
		if img != nil {
			resolved[media.ID] = resolvedMedia{Image: img.path, Tracks: img.tracks, fs: fs}
			if unverified > 0 {
				warnings = append(warnings, fmt.Sprintf("source.media %q: %s: %d track(s) in lossy audio files not verified", media.ID, img.path, unverified))
			}
			continue
		}
		msg := fmt.Sprintf("source.media %q: no matching disc image among %d in the build", media.ID, len(images))
		if len(near) > 0 {
			msg += fmt.Sprintf(" (same track layout, different hashes: %s)", strings.Join(near, ", "))
		}
		if used[media.ID] {
			errs = append(errs, errors.New(msg))
		} else {
			warnings = append(warnings, msg)
		}
	}
	if len(resolved) < len(declared) {
		warnings = append(warnings, unreadable...)
	}
	return resolved, warnings, errors.Join(errs...)
}

// matchMedia returns the first image matching media (§7.3) and how many of
// its tracks couldn't be verified, or nil and the images that have
// media's track layout but different hashes.
func matchMedia(fs *fileSet, media manifest.Media, images []*discImage) (match *discImage, unverified int, near []string, err error) {
	for _, img := range images {
		if !sameShape(media.Tracks, img.tracks) {
			continue
		}
		ok, skipped, err := verifyTracks(fs, media.Tracks, img)
		if err != nil {
			return nil, 0, nil, fmt.Errorf("%s: %w", img.path, err)
		}
		if ok {
			return img, skipped, nil, nil
		}
		near = append(near, img.path)
	}
	return nil, 0, near, nil
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

// verifyTracks compares every verifiable track's hash to its declared
// hash, stopping at the first mismatch, and counts the tracks it skipped.
func verifyTracks(fs *fileSet, declared []manifest.Track, img *discImage) (ok bool, skipped int, err error) {
	for i, d := range declared {
		if !img.tracks[i].Verifiable() {
			skipped++
			continue
		}
		got, err := img.hash(fs, i)
		if err != nil {
			return false, 0, fmt.Errorf("track %d: %w", i+1, err)
		}
		if got != d.SHA1 {
			return false, 0, nil
		}
	}
	return true, skipped, nil
}

// discImages resolves every disc image in fs: each CUE sheet, and each ISO
// image as one data track. One that can't be resolved is reported in
// unreadable instead.
func discImages(fs *fileSet) (images []*discImage, unreadable []string) {
	for _, p := range fs.paths() {
		ext := strings.ToLower(path.Ext(p))
		if ext != ".cue" && ext != ".iso" {
			continue
		}
		tracks, err := resolveImage(fs, p)
		if err != nil {
			unreadable = append(unreadable, fmt.Sprintf("%s: not usable as media: %v", p, err))
			continue
		}
		images = append(images, &discImage{path: p, tracks: tracks})
	}
	return images, unreadable
}

// resolveImage resolves a CUE sheet or ISO image in fs, its tracks'
// FilePaths being paths in fs.
func resolveImage(fs *fileSet, p string) ([]cue.ResolvedTrack, error) {
	var sheet *cue.Sheet
	if strings.EqualFold(path.Ext(p), ".iso") {
		sheet = isoSheet(path.Base(p))
	} else {
		e, _ := fs.get(p)
		data, err := e.bytes()
		if err != nil {
			return nil, err
		}
		if sheet, err = cue.Parse(data); err != nil {
			return nil, err
		}
	}
	dir := path.Dir(p)
	return cue.ResolveOpen(sheet, func(name string) (string, int64, error) {
		fp := path.Join(dir, strings.ReplaceAll(name, `\`, "/"))
		e, ok := fs.get(fp)
		if !ok {
			return "", 0, os.ErrNotExist
		}
		size, err := e.size()
		return fp, size, err
	}, func(key string) (io.ReadCloser, error) {
		e, _ := fs.get(key)
		size, err := e.size()
		if err != nil {
			return nil, err
		}
		return e.openRange(0, size)
	})
}

// isoSheet is the CUE sheet equivalent to a bare ISO image: one data track
// of 2048-byte sectors, the same media as a BIN/CUE dump of that disc
// (§7.1).
func isoSheet(name string) *cue.Sheet {
	return &cue.Sheet{Files: []cue.File{{Name: name, Type: "BINARY", Tracks: []cue.Track{{
		Number: 1, Mode: "MODE1/2048", Indexes: []cue.Index{{Number: 1}},
	}}}}}
}

func resolveCueFile(path string) ([]cue.ResolvedTrack, error) {
	if strings.EqualFold(filepath.Ext(path), ".iso") {
		return cue.Resolve(isoSheet(filepath.Base(path)), filepath.Dir(path))
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
		status.Step("Identifying %s", filepath.Base(p))
		tracks, err := resolveCueFile(p)
		if err != nil {
			skipped = append(skipped, fmt.Sprintf("%s: %v", filepath.ToSlash(rel), err))
			continue
		}
		if lossy := countUnverifiable(tracks); lossy > 0 {
			skipped = append(skipped, fmt.Sprintf("%s: %d track(s) in lossy audio files, which can't be hashed", filepath.ToSlash(rel), lossy))
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

func countUnverifiable(tracks []cue.ResolvedTrack) int {
	n := 0
	for _, t := range tracks {
		if !t.Verifiable() {
			n++
		}
	}
	return n
}
