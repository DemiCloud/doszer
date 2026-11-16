// Package cue parses CUE sheets and resolves each track's byte range
// within its backing file, for media identification (spec §7) and
// conversion (§6.4).
package cue

import (
	"bufio"
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// FramesPerSecond is the CD timecode rate (75 frames/second, Red Book).
const FramesPerSecond = 75

// Sheet is a parsed CUE sheet: one or more FILEs, each with one or more
// TRACKs.
type Sheet struct {
	Files []File
}

// File is one `FILE "name" TYPE` stanza and its tracks.
type File struct {
	Name   string // as written in the sheet; resolved relative to the sheet
	Type   string // BINARY, WAVE, etc.
	Tracks []Track
}

// Track is one `TRACK NN MODE` stanza.
type Track struct {
	Number  int
	Mode    string // AUDIO, MODE1/2352, MODE2/2352, MODE1/2048, ...
	Pregap  int    // PREGAP command's length in frames (silence not stored in the FILE)
	Indexes []Index
}

// Index is one `INDEX NN MM:SS:FF` line.
type Index struct {
	Number int
	Frames int // absolute frame offset from the start of the track's FILE
}

// IsAudio reports whether the track is a CD-DA audio track.
func (t Track) IsAudio() bool {
	return strings.EqualFold(t.Mode, "AUDIO")
}

// FrameSize returns the track's on-disk bytes per frame: 2352 for AUDIO, or
// the size named by a MODEn/SIZE mode.
func (t Track) FrameSize() (int, error) {
	if t.IsAudio() {
		return 2352, nil
	}
	_, size, ok := strings.Cut(t.Mode, "/")
	if !ok {
		return 0, fmt.Errorf("track %d: unrecognized mode %q", t.Number, t.Mode)
	}
	n, err := strconv.Atoi(size)
	if err != nil {
		return 0, fmt.Errorf("track %d: unrecognized mode %q", t.Number, t.Mode)
	}
	return n, nil
}

// Index01 returns the track's INDEX 01 (its start, excluding any pregap).
func (t Track) Index01() (Index, error) {
	for _, idx := range t.Indexes {
		if idx.Number == 1 {
			return idx, nil
		}
	}
	return Index{}, fmt.Errorf("track %d: missing INDEX 01", t.Number)
}

// Index00 returns the track's INDEX 00 (its pregap start), if present.
func (t Track) Index00() (Index, bool) {
	for _, idx := range t.Indexes {
		if idx.Number == 0 {
			return idx, true
		}
	}
	return Index{}, false
}

// TotalPregap returns the track's whole pregap in frames: its PREGAP
// command plus the INDEX 00 to INDEX 01 span stored in its FILE.
func (t Track) TotalPregap() (int, error) {
	idx1, err := t.Index01()
	if err != nil {
		return 0, err
	}
	n := t.Pregap
	if idx0, ok := t.Index00(); ok {
		if idx0.Frames > idx1.Frames {
			return 0, fmt.Errorf("track %d: INDEX 00 after INDEX 01", t.Number)
		}
		n += idx1.Frames - idx0.Frames
	}
	return n, nil
}

// Parse parses CUE sheet data. A leading UTF-8 byte order mark, which
// some tools write, is skipped.
func Parse(data []byte) (*Sheet, error) {
	sheet := &Sheet{}

	line := 0
	scanner := bufio.NewScanner(bytes.NewReader(bytes.TrimPrefix(data, []byte("\xEF\xBB\xBF"))))
	for scanner.Scan() {
		line++
		fields, err := tokenize(scanner.Text())
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		if len(fields) == 0 {
			continue
		}

		switch strings.ToUpper(fields[0]) {
		case "FILE":
			if len(fields) != 3 {
				return nil, fmt.Errorf("line %d: FILE needs a name and a type", line)
			}
			sheet.Files = append(sheet.Files, File{Name: fields[1], Type: fields[2]})

		case "TRACK":
			if len(fields) != 3 {
				return nil, fmt.Errorf("line %d: TRACK needs a number and a mode", line)
			}
			if len(sheet.Files) == 0 {
				return nil, fmt.Errorf("line %d: TRACK before any FILE", line)
			}
			n, err := strconv.Atoi(fields[1])
			if err != nil {
				return nil, fmt.Errorf("line %d: bad track number %q", line, fields[1])
			}
			f := &sheet.Files[len(sheet.Files)-1]
			f.Tracks = append(f.Tracks, Track{Number: n, Mode: fields[2]})

		case "INDEX":
			if len(fields) != 3 {
				return nil, fmt.Errorf("line %d: INDEX needs a number and a timecode", line)
			}
			f := lastFile(sheet)
			if f == nil || len(f.Tracks) == 0 {
				return nil, fmt.Errorf("line %d: INDEX before any TRACK", line)
			}
			n, err := strconv.Atoi(fields[1])
			if err != nil {
				return nil, fmt.Errorf("line %d: bad index number %q", line, fields[1])
			}
			frames, err := parseTimecode(fields[2])
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", line, err)
			}
			t := &f.Tracks[len(f.Tracks)-1]
			t.Indexes = append(t.Indexes, Index{Number: n, Frames: frames})

		case "PREGAP":
			if len(fields) != 2 {
				return nil, fmt.Errorf("line %d: PREGAP needs a timecode", line)
			}
			f := lastFile(sheet)
			if f == nil || len(f.Tracks) == 0 {
				return nil, fmt.Errorf("line %d: PREGAP before any TRACK", line)
			}
			frames, err := parseTimecode(fields[1])
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", line, err)
			}
			f.Tracks[len(f.Tracks)-1].Pregap = frames

		default:
			// REM, FLAGS, CATALOG, and anything else: not needed to resolve
			// track byte ranges or to write a converted sheet, so ignored.
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return sheet, nil
}

func lastFile(s *Sheet) *File {
	if len(s.Files) == 0 {
		return nil
	}
	return &s.Files[len(s.Files)-1]
}

// parseTimecode parses a CUE MM:SS:FF timecode into an absolute frame count.
func parseTimecode(s string) (int, error) {
	parts := strings.Split(s, ":")
	if len(parts) != 3 {
		return 0, fmt.Errorf("bad timecode %q", s)
	}
	m, err1 := strconv.Atoi(parts[0])
	sec, err2 := strconv.Atoi(parts[1])
	f, err3 := strconv.Atoi(parts[2])
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, fmt.Errorf("bad timecode %q", s)
	}
	return (m*60+sec)*FramesPerSecond + f, nil
}

// FormatTimecode formats an absolute frame count as a CUE MM:SS:FF timecode.
func FormatTimecode(frames int) string {
	return fmt.Sprintf("%02d:%02d:%02d", frames/(60*FramesPerSecond), frames/FramesPerSecond%60, frames%FramesPerSecond)
}

// Format writes s as CUE sheet text with CRLF line endings: each FILE, its
// TRACKs, and each track's PREGAP (if non-zero) and INDEX lines. Commands
// Parse ignores are not written.
func (s *Sheet) Format() []byte {
	var b strings.Builder
	for _, f := range s.Files {
		fmt.Fprintf(&b, "FILE \"%s\" %s\r\n", f.Name, f.Type)
		for _, t := range f.Tracks {
			fmt.Fprintf(&b, "  TRACK %02d %s\r\n", t.Number, t.Mode)
			if t.Pregap > 0 {
				fmt.Fprintf(&b, "    PREGAP %s\r\n", FormatTimecode(t.Pregap))
			}
			for _, idx := range t.Indexes {
				fmt.Fprintf(&b, "    INDEX %02d %s\r\n", idx.Number, FormatTimecode(idx.Frames))
			}
		}
	}
	return []byte(b.String())
}

// tokenize splits a CUE line into fields, treating a "quoted string" as one
// field.
func tokenize(line string) ([]string, error) {
	var fields []string
	i, n := 0, len(line)
	for i < n {
		for i < n && (line[i] == ' ' || line[i] == '\t') {
			i++
		}
		if i >= n {
			break
		}
		if line[i] == '"' {
			end := strings.IndexByte(line[i+1:], '"')
			if end == -1 {
				return nil, fmt.Errorf("unterminated quoted string")
			}
			fields = append(fields, line[i+1:i+1+end])
			i += end + 2
			continue
		}
		start := i
		for i < n && line[i] != ' ' && line[i] != '\t' {
			i++
		}
		fields = append(fields, line[start:i])
	}
	return fields, nil
}
