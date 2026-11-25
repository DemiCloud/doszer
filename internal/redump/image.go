package redump

import (
	"archive/zip"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	pathpkg "path"
	"path/filepath"
	"strings"

	"github.com/DemiCloud/doszer/internal/cue"
)

// Image is a disc image: a CUE sheet and its files, in a directory or a
// zip (as Redump's own downloads are), read as Redump stores the disc.
type Image struct {
	Cue    string // the CUE sheet, for messages: its path, or zip path and entry
	Tracks []ImageTrack
	close  func() error
}

// Close releases the image's zip, if any.
func (img *Image) Close() error {
	if img.close == nil {
		return nil
	}
	return img.close()
}

// ImageTrack is one track of a disc image as Redump stores it: a file of
// raw 2352-byte sectors running from the track's pregap (INDEX 00) to the
// next track's, whatever layout the image's CUE sheet uses - one file per
// track, or one file for the whole disc.
type ImageTrack struct {
	Number int
	Mode   string // the CUE track mode, e.g. MODE1/2352 or AUDIO
	Pregap int    // frames from the file's start to INDEX 01
	Size   int64

	fsys  fs.FS
	parts []part
}

// part is a byte range of an image file, or (name "") zero bytes: a
// pregap the image's sheet declares with PREGAP, which isn't stored in its
// files.
type part struct {
	name      string
	off, size int64
	mode1     bool  // 2048-byte sectors, read as raw Mode 1 sectors (Mode1)
	lba       int64 // the first sector's address, for mode1
}

// Open streams the track's bytes. A file that can't seek (a zip entry) is
// read from its start.
func (t ImageTrack) Open() (io.ReadCloser, error) {
	var readers []io.Reader
	var files []fs.File
	closeAll := func() {
		for _, f := range files {
			f.Close()
		}
	}
	for _, p := range t.parts {
		if p.name == "" {
			readers = append(readers, io.LimitReader(zeros{}, p.size))
			continue
		}
		f, err := t.fsys.Open(p.name)
		if err != nil {
			closeAll()
			return nil, err
		}
		files = append(files, f)
		var r io.Reader
		if ra, ok := f.(io.ReaderAt); ok {
			r = io.NewSectionReader(ra, p.off, p.size)
		} else {
			if _, err := io.CopyN(io.Discard, f, p.off); err != nil {
				closeAll()
				return nil, fmt.Errorf("%s: %w", p.name, err)
			}
			r = io.LimitReader(f, p.size)
		}
		if p.mode1 {
			r = &mode1Reader{r: r, lba: p.lba}
		}
		readers = append(readers, r)
	}
	return readCloser{io.MultiReader(readers...), closeAll}, nil
}

type zeros struct{}

func (zeros) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

type readCloser struct {
	io.Reader
	close func()
}

func (r readCloser) Close() error {
	r.close()
	return nil
}

// OpenImage opens a disc image: a CUE sheet, or a zip holding one (and its
// files). Its tracks are in Redump's layout: each runs from its INDEX 00
// (INDEX 01 if it has none) to the next track's, plus the silence a PREGAP
// command declares, as zeros; the first track starts at INDEX 01, as on
// the disc. Tracks must be raw sectors: BINARY files of 2352-byte frames,
// or WAVE files' PCM data, or a Mode 1 data track's 2048-byte sectors (an
// ISO, as an archive keeps it), rebuilt as raw sectors at their disc
// address (Mode1). A lossy (Ogg) track can't reproduce Redump's bytes, nor
// can a Mode 2 track kept as 2048-byte sectors (each sector's subheader is
// lost): it is read as Mode 1, and won't match.
//
// offset corrects an image whose audio wasn't corrected for the drive's
// read offset (ImgBurn's, for one): audio tracks are read offset samples
// later in the image's files (earlier if negative), across track and file
// boundaries, with zeros past either end. Data tracks don't move: their
// sectors are addressed by their headers, not by position. Silence a
// PREGAP command declares isn't in the files, so it doesn't move either.
func OpenImage(path string, offset int) (*Image, error) {
	if !strings.EqualFold(filepath.Ext(path), ".zip") {
		img, err := readImage(os.DirFS(filepath.Dir(path)), filepath.Base(path), offset)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		img.Cue = path
		return img, nil
	}
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	var cues []string
	for _, f := range zr.File {
		if strings.EqualFold(pathpkg.Ext(f.Name), ".cue") {
			cues = append(cues, f.Name)
		}
	}
	if len(cues) != 1 {
		zr.Close()
		return nil, fmt.Errorf("%s: %d CUE sheets, need exactly one", path, len(cues))
	}
	img, err := readImage(zr, cues[0], offset)
	if err != nil {
		zr.Close()
		return nil, fmt.Errorf("%s: %s: %w", path, cues[0], err)
	}
	img.Cue = path + ": " + cues[0]
	img.close = zr.Close
	return img, nil
}

func readImage(fsys fs.FS, cueName string, offset int) (*Image, error) {
	data, err := fs.ReadFile(fsys, cueName)
	if err != nil {
		return nil, err
	}
	sheet, err := cue.Parse(data)
	if err != nil {
		return nil, err
	}
	if len(sheet.Files) == 0 {
		return nil, errors.New("no FILE")
	}
	dir := pathpkg.Dir(cueName)
	resolved, err := cue.ResolveOpen(sheet, func(name string) (string, int64, error) {
		p := pathpkg.Join(dir, strings.ReplaceAll(name, `\`, "/"))
		info, err := fs.Stat(fsys, p)
		if err != nil {
			return "", 0, err
		}
		return p, info.Size(), nil
	}, func(key string) (io.ReadCloser, error) { return fsys.Open(key) })
	if err != nil {
		return nil, err
	}

	img := &Image{}
	var lba int64 // the next track's first sector, in Redump's layout
	for i, rt := range resolved {
		t := rt.Track
		if !rt.Verifiable() {
			return nil, fmt.Errorf("track %d is in a %s file, which can't reproduce the disc's bytes", t.Number, rt.FileType)
		}
		fsize := int64(rt.FrameSize)
		mode1 := fsize == 2048 && t.Mode != "AUDIO"
		if fsize != 2352 && !mode1 {
			return nil, fmt.Errorf("track %d has %d-byte sectors, not the disc's raw 2352", t.Number, rt.FrameSize)
		}
		start := rt.Start
		inFile := 0 // pregap frames stored in the file
		if idx0, ok := t.Index00(); ok && i > 0 {
			idx1, _ := t.Index01()
			inFile = idx1.Frames - idx0.Frames
			start -= int64(inFile) * fsize
		}
		it := ImageTrack{Number: t.Number, Mode: t.Mode, fsys: fsys}
		if t.Pregap > 0 && i > 0 {
			n := int64(t.Pregap) * 2352
			it.parts = append(it.parts, part{size: n})
			it.Size += n
		}
		frames := (rt.End - start) / fsize
		it.parts = append(it.parts, part{name: rt.FilePath, off: start, size: frames * fsize, mode1: mode1, lba: lba + it.Size/2352})
		it.Size += frames * 2352
		it.Pregap = inFile
		if i > 0 {
			it.Pregap += t.Pregap
		}
		lba += it.Size / 2352
		img.Tracks = append(img.Tracks, it)
	}
	if offset != 0 {
		for _, t := range img.Tracks {
			for _, p := range t.parts {
				if p.mode1 {
					return nil, fmt.Errorf("track %d has 2048-byte sectors: read offset correction needs raw sectors", t.Number)
				}
			}
		}
		shiftAudio(img.Tracks, int64(offset)*4)
	}
	return img, nil
}

// ArchiveImages opens the disc images in a .dosz archive (or any zip):
// every CUE sheet, and every ISO no sheet refers to, as a disc of one
// Mode 1 data track. Archives keep data tracks as 2048-byte sectors,
// rebuilt here as raw ones, and audio as WAV or Ogg Vorbis; an image with
// Ogg Vorbis tracks can't reproduce Redump's bytes and is left out, its
// reason in skipped.
func ArchiveImages(path string) (images []*Image, skipped []string, closeAll func() error, err error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, nil, nil, err
	}
	used := map[string]bool{}
	for _, f := range zr.File {
		if !strings.EqualFold(pathpkg.Ext(f.Name), ".cue") {
			continue
		}
		data, err := fs.ReadFile(zr, f.Name)
		if err != nil {
			zr.Close()
			return nil, nil, nil, err
		}
		if sheet, err := cue.Parse(data); err == nil {
			for _, cf := range sheet.Files {
				used[strings.ToUpper(pathpkg.Join(pathpkg.Dir(f.Name), strings.ReplaceAll(cf.Name, `\`, "/")))] = true
			}
		}
		img, err := readImage(zr, f.Name, 0)
		if err != nil {
			skipped = append(skipped, fmt.Sprintf("%s: %v", f.Name, err))
			continue
		}
		img.Cue = f.Name
		images = append(images, img)
	}
	for _, f := range zr.File {
		if !strings.EqualFold(pathpkg.Ext(f.Name), ".iso") || used[strings.ToUpper(f.Name)] {
			continue
		}
		frames := int64(f.UncompressedSize64) / 2048
		images = append(images, &Image{Cue: f.Name, Tracks: []ImageTrack{{
			Number: 1, Mode: "MODE1/2048", Size: frames * 2352, fsys: zr,
			parts: []part{{name: f.Name, size: frames * 2048, mode1: true}},
		}}})
	}
	return images, skipped, zr.Close, nil
}

// Hash hashes the image's tracks for Match. Each Track's Path names the
// image's CUE sheet and the track number.
func (img *Image) Hash() ([]Track, error) {
	var out []Track
	for _, t := range img.Tracks {
		rc, err := t.Open()
		if err != nil {
			return nil, err
		}
		h := sha1.New()
		n, err := io.Copy(h, rc)
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: track %d: %w", img.Cue, t.Number, err)
		}
		out = append(out, Track{Path: fmt.Sprintf("%s, track %d", pathpkg.Base(filepath.ToSlash(img.Cue)), t.Number), Size: n, SHA1: hex.EncodeToString(h.Sum(nil))})
	}
	return out, nil
}

// TrackFileName is Redump's (and redumper's) name for track n of a disc
// image with count tracks: "<base>.bin" for a single track, else
// "<base> (Track N).bin", N two-digit when the disc has ten or more.
func TrackFileName(base string, n, count int) string {
	switch {
	case count == 1:
		return base + ".bin"
	case count >= 10:
		return fmt.Sprintf("%s (Track %02d).bin", base, n)
	}
	return fmt.Sprintf("%s (Track %d).bin", base, n)
}

// FormatImage is the CUE sheet for tracks written as TrackFileName(base,
// ...) files, as redumper writes it.
func FormatImage(base string, tracks []ImageTrack) []byte {
	var s cue.Sheet
	for _, t := range tracks {
		ct := cue.Track{Number: t.Number, Mode: strings.ToUpper(t.Mode)}
		if t.Pregap > 0 {
			ct.Indexes = append(ct.Indexes, cue.Index{Number: 0})
		}
		ct.Indexes = append(ct.Indexes, cue.Index{Number: 1, Frames: t.Pregap})
		s.Files = append(s.Files, cue.File{Name: TrackFileName(base, t.Number, len(tracks)), Type: "BINARY", Tracks: []cue.Track{ct}})
	}
	return s.Format()
}

// shiftAudio moves every audio track's file-backed bytes by shift bytes
// within the image's file contents taken as one stream (every track's
// file-backed part, in order).
func shiftAudio(tracks []ImageTrack, shift int64) {
	var stream []part
	var total int64
	starts := make([]int64, len(tracks)) // stream offset of each track's file-backed part
	for i, t := range tracks {
		for _, p := range t.parts {
			if p.name != "" {
				starts[i] = total
				stream = append(stream, p)
				total += p.size
			}
		}
	}
	for i := range tracks {
		t := &tracks[i]
		if !strings.EqualFold(t.Mode, "AUDIO") {
			continue
		}
		for j, p := range t.parts {
			if p.name == "" {
				continue
			}
			parts := slice(stream, starts[i]+shift, p.size)
			t.parts = append(append(append([]part(nil), t.parts[:j]...), parts...), t.parts[j+1:]...)
			break
		}
	}
}

// slice returns size bytes of stream from from, with zeros for whatever
// lies outside it.
func slice(stream []part, from, size int64) []part {
	var out []part
	if from < 0 {
		n := min(-from, size)
		out = append(out, part{size: n})
		from, size = 0, size-n
	}
	var pos int64
	for _, p := range stream {
		if size == 0 {
			break
		}
		if from >= pos && from < pos+p.size {
			n := min(pos+p.size-from, size)
			out = append(out, part{name: p.name, off: p.off + from - pos, size: n})
			from, size = from+n, size-n
		}
		pos += p.size
	}
	if size > 0 {
		out = append(out, part{size: size})
	}
	return out
}
