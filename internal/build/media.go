package build

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/DemiCloud/doszer/internal/cue"
	"github.com/DemiCloud/doszer/internal/interrupt"
	"github.com/DemiCloud/doszer/internal/manifest"
	"github.com/DemiCloud/doszer/internal/status"
)

// resolvedMedia is a declared [[source.media]] entry matched to the disc
// image in the build whose tracks verify against it (§7.3).
type resolvedMedia struct {
	Image  string              // the disc image's path in the build
	Tracks []cue.ResolvedTrack // FilePath is a path in fs
	fs     *fileSet

	// isoFile is a temporary file holding the first data track's
	// normalized content, written while it was hashed; "" if none was.
	isoFile string
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
	path    string
	tracks  []cue.ResolvedTrack
	hashes  []string // computed on demand, by track index
	isoFile string   // see resolvedMedia.isoFile
}

// teeTarget asks locateMedia to keep a copy of the first data track of
// each image it hashes for a media an [[extract]] reads, so extracting
// needn't read the track again: temporary files go in dir and are
// appended to *files for the caller to remove.
type teeTarget struct {
	dir   string
	files *[]*os.File
}

// hash returns track i's normalized hash, computing it once. If tee is
// set, the normalized content is also written to it.
func (d *discImage) hash(fs *fileSet, i int, tee io.Writer) (string, error) {
	if d.hashes[i] == "" {
		rc, err := openTrack(fs, d.tracks[i])
		if err != nil {
			return "", err
		}
		defer rc.Close()
		n, err := d.tracks[i].Normalize(rc)
		if err != nil {
			return "", err
		}
		h := sha1.New()
		w := io.Writer(h)
		if tee != nil {
			w = io.MultiWriter(h, tee)
		}
		if _, err := io.Copy(w, n); err != nil {
			return "", err
		}
		d.hashes[i] = hex.EncodeToString(h.Sum(nil))
	}
	return d.hashes[i], nil
}

// streamed reports whether any of the image's tracks is in a compressed
// archive entry, which is read from its start in one stream (openRange)
// rather than at random, so its tracks must be hashed in order.
func (d *discImage) streamed(fs *fileSet) bool {
	for _, t := range d.tracks {
		if e, ok := fs.get(t.FilePath); ok && e.data == nil && e.source != nil {
			return true
		}
	}
	return false
}

// directISO returns where t's normalized content already lies, whole and
// unchanged, in a file on disk: a data track of 2048-byte sectors in a
// file collected from the source directory. [[extract]] reads such a track
// in place.
func directISO(fs *fileSet, t cue.ResolvedTrack) (path string, off, size int64, ok bool) {
	e, found := fs.get(t.FilePath)
	if !found || t.Track.IsAudio() || t.FrameSize != 2048 || e.data != nil || e.source != nil || e.ranged {
		return "", 0, 0, false
	}
	size = t.End - t.Start
	return e.diskPath, t.Start, size - size%2048, true
}

// locateMedia matches every declared media entry to a disc image in fs,
// verifying its track hashes (§7.3). A media that no image matches is an
// error if one of extracts references it, a warning otherwise; so is an
// image that can't be read, since it might have been the match.
//
// tee, if set, keeps the first data track of images hashed for a media
// extracts reference (teeTarget).
func locateMedia(fs *fileSet, declared []manifest.Media, extracts []manifest.Extract, tee *teeTarget) (map[string]resolvedMedia, []string, error) {
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
		mediaTee := tee
		if !used[media.ID] {
			mediaTee = nil
		}
		img, unverified, near, err := matchMedia(fs, media, images, mediaTee)
		if err != nil {
			return nil, warnings, fmt.Errorf("source.media %q: %w", media.ID, err)
		}
		if img != nil {
			resolved[media.ID] = resolvedMedia{Image: img.path, Tracks: img.tracks, fs: fs, isoFile: img.isoFile}
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
func matchMedia(fs *fileSet, media manifest.Media, images []*discImage, tee *teeTarget) (match *discImage, unverified int, near []string, err error) {
	for _, img := range images {
		if !sameShape(media.Tracks, img.tracks) {
			continue
		}
		ok, skipped, err := verifyTracks(fs, media.Tracks, img, tee)
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
// hash and counts the tracks it skipped. Tracks read from disk are hashed
// concurrently, one per CPU; an image in a compressed archive entry is
// hashed in order, so it is decompressed once, stopping at the first
// mismatch.
func verifyTracks(fs *fileSet, declared []manifest.Track, img *discImage, tee *teeTarget) (ok bool, skipped int, err error) {
	if img.hashes == nil {
		img.hashes = make([]string, len(img.tracks))
	}
	var todo []int
	for i := range declared {
		if !img.tracks[i].Verifiable() {
			skipped++
			continue
		}
		todo = append(todo, i)
	}

	teeIdx, teeFile, err := img.startTee(fs, tee)
	if err != nil {
		return false, 0, err
	}
	defer func() {
		if teeFile != nil && (!ok || err != nil) {
			os.Remove(teeFile.Name()) // not this media: free the space now
		}
	}()
	writerFor := func(i int) io.Writer {
		if i == teeIdx {
			return teeFile
		}
		return nil
	}

	if img.streamed(fs) {
		for _, i := range todo {
			got, err := img.hash(fs, i, writerFor(i))
			if err != nil {
				return false, 0, fmt.Errorf("track %d: %w", i+1, err)
			}
			if got != declared[i].SHA1 {
				return false, 0, nil
			}
		}
	} else {
		errs := make([]error, len(img.tracks))
		var wg sync.WaitGroup
		sem := make(chan struct{}, runtime.GOMAXPROCS(0))
		for _, i := range todo {
			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer func() { <-sem; wg.Done() }()
				if _, err := img.hash(fs, i, writerFor(i)); err != nil {
					errs[i] = fmt.Errorf("track %d: %w", i+1, err)
				}
			}()
		}
		wg.Wait()
		if err := errors.Join(errs...); err != nil {
			return false, 0, err
		}
		for _, i := range todo {
			if img.hashes[i] != declared[i].SHA1 {
				return false, 0, nil
			}
		}
	}
	if teeFile != nil {
		img.isoFile = teeFile.Name()
	}
	return true, skipped, nil
}

// startTee creates the file the image's first data track is copied to
// while it is hashed, if tee asks for one and the track will be hashed
// now: not already hashed, and not readable in place (directISO). It
// returns that track's index, or -1.
func (img *discImage) startTee(fs *fileSet, tee *teeTarget) (int, *os.File, error) {
	if tee == nil || img.isoFile != "" {
		return -1, nil, nil
	}
	for i, t := range img.tracks {
		if t.Track.IsAudio() {
			continue
		}
		if img.hashes[i] != "" || !t.Verifiable() {
			return -1, nil, nil
		}
		if _, _, _, ok := directISO(fs, t); ok {
			return -1, nil, nil
		}
		f, err := os.CreateTemp(tee.dir, ".doszer-media-*.iso")
		if err != nil {
			return -1, nil, err
		}
		*tee.files = append(*tee.files, f)
		interrupt.Remove(f.Name()) // left registered: removing a removed file is harmless
		return i, f, nil
	}
	return -1, nil, nil
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
		data, err := e.peek()
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
// skipped; one with the same tracks as an earlier one is left out. A bare
// ISO image counts only if no CUE sheet references it: an ISO+WAV rip's
// data track is part of its CUE sheet's disc, not a disc of its own.
// Every track of every image is hashed concurrently, one per CPU.
func IdentifyMedia(sourceDir string) (media []manifest.Media, skipped []string, err error) {
	cues, err := findCueSheets(sourceDir)
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(cues)
	referenced := map[string]bool{} // lower-case paths of the files CUE sheets use
	for _, p := range cues {
		if !strings.EqualFold(filepath.Ext(p), ".iso") {
			tracks, _ := resolveCueFile(p)
			for _, t := range tracks {
				referenced[strings.ToLower(filepath.Clean(t.FilePath))] = true
			}
		}
	}

	type image struct {
		rel    string
		path   string
		tracks []cue.ResolvedTrack
		sums   []string
		errs   []error
	}
	var images []*image
	for _, p := range cues {
		rel, _ := filepath.Rel(sourceDir, p)
		rel = filepath.ToSlash(rel)
		if strings.EqualFold(filepath.Ext(p), ".iso") && referenced[strings.ToLower(filepath.Clean(p))] {
			continue
		}
		tracks, err := resolveCueFile(p)
		if err != nil {
			skipped = append(skipped, fmt.Sprintf("%s: %v", rel, err))
			continue
		}
		if lossy := countUnverifiable(tracks); lossy > 0 {
			skipped = append(skipped, fmt.Sprintf("%s: %d track(s) in lossy audio files, which can't be hashed", rel, lossy))
			continue
		}
		images = append(images, &image{rel: rel, path: p, tracks: tracks,
			sums: make([]string, len(tracks)), errs: make([]error, len(tracks))})
	}

	status.Step("Identifying disc images")
	total := 0
	for _, img := range images {
		total += len(img.tracks)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	done := 0
	sem := make(chan struct{}, runtime.GOMAXPROCS(0))
	for _, img := range images {
		for i, t := range img.tracks {
			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer func() { <-sem; wg.Done() }()
				img.sums[i], img.errs[i] = t.Hash()
				mu.Lock()
				done++
				status.Count(done, total)
				mu.Unlock()
			}()
		}
	}
	wg.Wait()

	seen := map[string]bool{}
	for _, img := range images {
		m := manifest.Media{
			ID:    fmt.Sprintf("disc%d", len(media)+1),
			Label: strings.TrimSuffix(filepath.Base(img.path), filepath.Ext(img.path)),
		}
		var key strings.Builder
		for i, t := range img.tracks {
			if img.errs[i] != nil {
				return nil, skipped, fmt.Errorf("%s: track %d: %w", img.rel, i+1, img.errs[i])
			}
			typ := manifest.TrackData
			if t.Track.IsAudio() {
				typ = manifest.TrackAudio
			}
			m.Tracks = append(m.Tracks, manifest.Track{Type: typ, SHA1: img.sums[i]})
			key.WriteString(typ + img.sums[i])
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
