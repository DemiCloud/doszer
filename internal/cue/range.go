package cue

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ResolvedTrack is one track with its byte range resolved within its
// backing file.
type ResolvedTrack struct {
	Track     Track
	FilePath  string // absolute path to the backing file
	FrameSize int
	Start     int64 // byte offset of INDEX 01 (pregap excluded)
	End       int64 // byte offset where the track ends, exclusive
}

// Resolve resolves every track's byte range, in sheet order. File names are
// resolved relative to baseDir.
func Resolve(sheet *Sheet, baseDir string) ([]ResolvedTrack, error) {
	var out []ResolvedTrack

	for _, file := range sheet.Files {
		path := filepath.Join(baseDir, filepath.FromSlash(file.Name))
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", file.Name, err)
		}
		size := info.Size()

		var frameSize int
		for i, track := range file.Tracks {
			fs, err := track.FrameSize()
			if err != nil {
				return nil, err
			}
			if i == 0 {
				frameSize = fs
			} else if fs != frameSize {
				return nil, fmt.Errorf("%s: track %d's frame size differs from track %d's", file.Name, track.Number, file.Tracks[0].Number)
			}

			idx1, err := track.Index01()
			if err != nil {
				return nil, fmt.Errorf("%s: %w", file.Name, err)
			}
			start := int64(idx1.Frames) * int64(fs)

			end := size
			if i+1 < len(file.Tracks) {
				next := file.Tracks[i+1]
				boundary, err := next.boundaryIndex()
				if err != nil {
					return nil, fmt.Errorf("%s: %w", file.Name, err)
				}
				end = int64(boundary.Frames) * int64(fs)
			}

			out = append(out, ResolvedTrack{
				Track: track, FilePath: path, FrameSize: fs, Start: start, End: end,
			})
		}
	}

	return out, nil
}

// boundaryIndex returns a track's INDEX 00 (its pregap start) if present,
// else its INDEX 01: the point where the previous track's range ends.
func (t Track) boundaryIndex() (Index, error) {
	for _, idx := range t.Indexes {
		if idx.Number == 0 {
			return idx, nil
		}
	}
	return t.Index01()
}

// Copy writes rt's normalized content to w: raw samples for an audio track,
// user data only (sync/header/ECC stripped) for a data track (§7.2).
func (rt ResolvedTrack) Copy(w io.Writer) error {
	f, err := os.Open(rt.FilePath)
	if err != nil {
		return err
	}
	defer f.Close()

	r := io.NewSectionReader(f, rt.Start, rt.End-rt.Start)

	if rt.Track.IsAudio() {
		_, err = io.Copy(w, r)
		return err
	}

	offset, length, err := userDataLayout(rt.Track.Mode)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, newStripReader(r, rt.FrameSize, offset, length))
	return err
}

// Hash returns rt's normalized track hash (§7.2).
func (rt ResolvedTrack) Hash() (string, error) {
	h := sha1.New()
	if err := rt.Copy(h); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// userDataLayout returns a data track's user-data offset and length within
// each raw frame (§7.2): the whole frame if it's already 2048 bytes,
// otherwise Mode 1's or Mode 2 Form 1's header offset.
func userDataLayout(mode string) (offset, length int, err error) {
	kind, size, ok := strings.Cut(mode, "/")
	if !ok {
		return 0, 0, fmt.Errorf("unrecognized mode %q", mode)
	}
	switch {
	case size == "2048":
		return 0, 2048, nil
	case strings.EqualFold(kind, "MODE1") && size == "2352":
		return 16, 2048, nil
	case strings.EqualFold(kind, "MODE2") && size == "2352":
		return 24, 2048, nil // Mode 2 Form 1 assumed, per spec §7.2
	default:
		return 0, 0, fmt.Errorf("unsupported data track mode %q", mode)
	}
}

// stripReader reads frameSize-byte frames from r, yielding only each
// frame's [offset:offset+length) user-data slice.
type stripReader struct {
	r              io.Reader
	frame          []byte
	offset, length int
	buf            []byte
}

func newStripReader(r io.Reader, frameSize, offset, length int) *stripReader {
	return &stripReader{r: r, frame: make([]byte, frameSize), offset: offset, length: length}
}

func (s *stripReader) Read(p []byte) (int, error) {
	if len(s.buf) == 0 {
		if _, err := io.ReadFull(s.r, s.frame); err != nil {
			return 0, err
		}
		s.buf = s.frame[s.offset : s.offset+s.length]
	}
	n := copy(p, s.buf)
	s.buf = s.buf[n:]
	return n, nil
}
