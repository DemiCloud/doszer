package cue

import (
	"bufio"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"errors"
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
	FilePath  string // backing file: an absolute path from Resolve, or ResolveWith's key
	FileType  string // the FILE command's type: BINARY, WAVE, OGG, ...
	FrameSize int
	Start     int64 // byte offset of INDEX 01 (pregap excluded)
	End       int64 // byte offset where the track ends, exclusive
}

// Resolve resolves every track's byte range, in sheet order. File names are
// resolved relative to baseDir.
func Resolve(sheet *Sheet, baseDir string) ([]ResolvedTrack, error) {
	return ResolveOpen(sheet, func(name string) (string, int64, error) {
		path := filepath.Join(baseDir, filepath.FromSlash(name))
		info, err := os.Stat(path)
		if err != nil {
			return "", 0, err
		}
		return path, info.Size(), nil
	}, func(key string) (io.ReadCloser, error) { return os.Open(key) })
}

// ResolveOpen is ResolveWith, plus locating a WAVE file's tracks within its
// PCM data: a WAVE file's INDEX times count from its first sample, not its
// first byte. open streams a file by the key resolve returned for it.
func ResolveOpen(sheet *Sheet, resolve FileResolver, open func(key string) (io.ReadCloser, error)) ([]ResolvedTrack, error) {
	wavData := map[string]int64{} // WAVE file -> offset of its PCM data
	tracks, err := ResolveWith(sheet, func(name string) (string, int64, error) {
		key, size, err := resolve(name)
		if err != nil || !isWave(sheet, name) {
			return key, size, err
		}
		rc, err := open(key)
		if err != nil {
			return "", 0, err
		}
		defer rc.Close()
		off, size, err := WAVDataFrom(rc)
		if err != nil {
			return "", 0, err
		}
		wavData[key] = off
		return key, size, nil
	})
	for i := range tracks {
		if off, ok := wavData[tracks[i].FilePath]; ok {
			tracks[i].Start += off
			tracks[i].End += off
		}
	}
	return tracks, err
}

func isWave(sheet *Sheet, name string) bool {
	for _, f := range sheet.Files {
		if f.Name == name {
			return strings.EqualFold(f.Type, "WAVE")
		}
	}
	return false
}

// WAVData locates a WAV file's PCM samples: the byte offset and length of
// its "data" chunk. It requires 16-bit stereo PCM, the CD-DA layout §7.2
// hashes.
func WAVData(path string) (offset, size int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	off, size, err := WAVDataFrom(bufio.NewReader(f))
	if err != nil {
		return 0, 0, fmt.Errorf("%s: %w", path, err)
	}
	return off, size, nil
}

// WAVDataFrom is WAVData reading the file from the start of r.
func WAVDataFrom(r io.Reader) (offset, size int64, err error) {
	head := make([]byte, 12)
	if _, err := io.ReadFull(r, head); err != nil || string(head[0:4]) != "RIFF" || string(head[8:12]) != "WAVE" {
		return 0, 0, errors.New("not a RIFF WAVE file")
	}
	pos := int64(12)
	chunk := make([]byte, 8)
	for {
		if _, err := io.ReadFull(r, chunk); err != nil {
			return 0, 0, errors.New("no data chunk")
		}
		pos += 8
		id, n := string(chunk[0:4]), int64(binary.LittleEndian.Uint32(chunk[4:]))
		if id == "data" {
			return pos, n, nil
		}
		skip := (n + 1) &^ 1
		if id == "fmt " {
			fmtc := make([]byte, 16)
			if _, err := io.ReadFull(r, fmtc); err != nil {
				return 0, 0, errors.New("short fmt chunk")
			}
			le := binary.LittleEndian
			if le.Uint16(fmtc[0:]) != 1 || le.Uint16(fmtc[2:]) != 2 || le.Uint16(fmtc[14:]) != 16 {
				return 0, 0, errors.New("not 16-bit stereo PCM")
			}
			skip -= 16
		}
		if _, err := io.CopyN(io.Discard, r, skip); err != nil {
			return 0, 0, errors.New("no data chunk")
		}
		pos += (n + 1) &^ 1
	}
}

// FileResolver maps a FILE name, as written in a sheet, to a key
// identifying the backing file (stored as ResolvedTrack.FilePath) and that
// file's size in bytes.
type FileResolver func(name string) (key string, size int64, err error)

// ResolveWith is Resolve against files located by resolve rather than on
// disk, e.g. files inside an archive or a build in progress.
func ResolveWith(sheet *Sheet, resolve FileResolver) ([]ResolvedTrack, error) {
	var out []ResolvedTrack

	for _, file := range sheet.Files {
		path, size, err := resolve(file.Name)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", file.Name, err)
		}

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
				Track: track, FilePath: path, FileType: file.Type, FrameSize: fs, Start: start, End: end,
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

	r, err := rt.Normalize(io.NewSectionReader(f, rt.Start, rt.End-rt.Start))
	if err != nil {
		return err
	}
	_, err = io.Copy(w, r)
	return err
}

// Normalize wraps r, which reads rt's raw bytes from Start to End, to yield
// rt's normalized content (§7.2): audio unchanged, a data track's user data
// only. This is exactly the content §6.4 converts a track to.
func (rt ResolvedTrack) Normalize(r io.Reader) (io.Reader, error) {
	if rt.Track.IsAudio() {
		return r, nil
	}
	offset, length, err := userDataLayout(rt.Track.Mode)
	if err != nil {
		return nil, err
	}
	return newStripReader(r, rt.FrameSize, offset, length), nil
}

// Verifiable reports whether rt's file stores the track's content exactly
// (§7.3): raw sectors (BINARY) or PCM samples (WAVE). A track in a lossy
// audio file (OGG, MP3, ...) cannot reproduce its normalized hash, and its
// byte range means nothing.
func (rt ResolvedTrack) Verifiable() bool {
	return strings.EqualFold(rt.FileType, "BINARY") || strings.EqualFold(rt.FileType, "WAVE")
}

// HashFrom returns rt's normalized track hash (§7.2), reading its raw bytes
// from Start to End from r.
func (rt ResolvedTrack) HashFrom(r io.Reader) (string, error) {
	n, err := rt.Normalize(r)
	if err != nil {
		return "", err
	}
	h := sha1.New()
	if _, err := io.Copy(h, n); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
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
