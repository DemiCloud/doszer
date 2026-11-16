package build

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/DemiCloud/doszer/internal/cue"
	"github.com/DemiCloud/doszer/internal/manifest"
	"github.com/DemiCloud/doszer/internal/status"
)

// Conversion is one disc image §6.4 converted or renamed.
type Conversion struct {
	Cue   string   // the converted CUE sheet's path
	Files []string // the files it references, in sheet order
}

// mediaJob produces one converted track file.
type mediaJob struct {
	kind  string // "iso", "wav" or "ogg"
	src   *fileEntry
	track cue.ResolvedTrack
	out   string // output file, set when the job runs
}

// sheetPlan is one CUE sheet's conversion, applied to the fileSet once
// every job has run.
type sheetPlan struct {
	cuePath   string
	cueEntry  *fileEntry
	text      []byte
	originals []string // paths the original sheet referenced
	outputs   []planOutput
}

// planOutput is one file the converted sheet references: an existing entry
// kept as it is (possibly renamed), or a job's output.
type planOutput struct {
	path  string
	entry *fileEntry // kept entry; nil when job is set
	job   *mediaJob
	mtime *fileEntry // entry whose modification time the output carries (§8.2)
}

// convertMedia applies §6.4 to every CUE sheet in fs whose files are all
// present. In a dry run it plans the result (entry paths, sheet text)
// without encoding anything; output files are then empty placeholders that
// writeArchive never sees. tmpDir is called only if a track must actually
// be written, and returns a directory for the converted files. oggQuality
// is libvorbis's quality (ffmpeg -q:a, -1 to 10) for Ogg audio tracks.
func convertMedia(fs *fileSet, audio string, oggQuality int, dryRun bool, tmpDir func() (string, error)) ([]Conversion, []string, error) {
	var plans []*sheetPlan
	var jobs []*mediaJob
	var warnings []string

	for _, cuePath := range fs.paths() {
		if !strings.EqualFold(path.Ext(cuePath), ".cue") {
			continue
		}
		plan, planJobs, err := planSheet(fs, cuePath, audio)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: not converted: %v", cuePath, err))
			continue
		}
		if plan != nil {
			plans = append(plans, plan)
			jobs = append(jobs, planJobs...)
		}
	}

	if err := checkOutputCollisions(fs, plans); err != nil {
		return nil, warnings, err
	}

	if !dryRun && len(jobs) > 0 {
		dir, err := tmpDir()
		if err != nil {
			return nil, warnings, err
		}
		if err := runJobs(jobs, dir, oggQuality); err != nil {
			return nil, warnings, err
		}
	}

	var conversions []Conversion
	for _, plan := range plans {
		for _, p := range plan.originals {
			fs.delete(p)
		}
		conv := Conversion{Cue: plan.cuePath}
		for _, o := range plan.outputs {
			e := o.entry
			switch {
			case e != nil:
			case dryRun:
				e = dataEntry([]byte{}, o.mtime.modified)
			default:
				e = diskEntry(o.job.out, o.mtime.modified)
			}
			fs.set(o.path, e)
			conv.Files = append(conv.Files, o.path)
		}
		fs.set(plan.cuePath, dataEntry(plan.text, plan.cueEntry.modified))
		conversions = append(conversions, conv)
	}

	return conversions, warnings, nil
}

// planSheet plans one CUE sheet's conversion. It returns a nil plan if the
// file isn't a CUE sheet or is already in its converted form, and an error
// if it is a CUE sheet that can't be converted (reported as a warning).
func planSheet(fs *fileSet, cuePath, audio string) (*sheetPlan, []*mediaJob, error) {
	cueEntry, _ := fs.get(cuePath)
	data, err := cueEntry.bytes()
	if err != nil {
		return nil, nil, err
	}
	sheet, err := cue.Parse(data)
	if err != nil || len(sheet.Files) == 0 {
		return nil, nil, nil // not a CUE sheet
	}

	dir := path.Dir(cuePath)
	filePath := func(name string) string {
		return path.Join(dir, strings.ReplaceAll(name, `\`, "/"))
	}
	resolved, err := cue.ResolveWith(sheet, func(name string) (string, int64, error) {
		p := filePath(name)
		e, ok := fs.get(p)
		if !ok {
			return "", 0, os.ErrNotExist
		}
		size, err := e.size()
		return p, size, err
	})
	if err != nil {
		return nil, nil, err
	}

	base := cueLabel(cuePath)
	plan := &sheetPlan{cuePath: cuePath, cueEntry: cueEntry}
	var jobs []*mediaJob
	var out cue.Sheet
	changed := false
	firstData := true
	k := 0 // index into resolved, which lists tracks in sheet order

	for _, f := range sheet.Files {
		src := filePath(f.Name)
		srcEntry, _ := fs.get(src)
		plan.originals = append(plan.originals, src)

		switch strings.ToUpper(f.Type) {
		case "BINARY":
		case "MOTOROLA":
			return nil, nil, fmt.Errorf("%s: big-endian (MOTOROLA) files are not supported", f.Name)
		default:
			// Already-encoded audio (WAVE, OGG, ...): kept unchanged.
			for _, t := range f.Tracks {
				if !t.IsAudio() {
					return nil, nil, fmt.Errorf("%s: data track %d in a %s file", f.Name, t.Number, f.Type)
				}
			}
			name := path.Base(src)
			if len(f.Tracks) == 1 {
				name = trackFileName(base, f.Tracks[0].Number, strings.TrimPrefix(path.Ext(src), "."))
			}
			outPath := path.Join(dir, name)
			changed = changed || outPath != src
			plan.outputs = append(plan.outputs, planOutput{path: outPath, entry: srcEntry, mtime: srcEntry})
			out.Files = append(out.Files, cue.File{Name: name, Type: f.Type, Tracks: f.Tracks})
			k += len(f.Tracks)
			continue
		}

		for _, t := range f.Tracks {
			rt := resolved[k]
			k++

			pregap, err := t.TotalPregap()
			if err != nil {
				return nil, nil, err
			}
			nt := cue.Track{Number: t.Number, Mode: "AUDIO", Pregap: pregap, Indexes: []cue.Index{{Number: 1}}}

			var name, fileType string
			o := planOutput{mtime: srcEntry}
			if t.IsAudio() {
				ext := "ogg"
				fileType = "OGG"
				if audio == manifest.AudioWAV {
					ext, fileType = "wav", "WAVE"
				}
				name = trackFileName(base, t.Number, ext)
				o.job = &mediaJob{kind: ext, src: srcEntry, track: rt}
			} else {
				if _, _, err := t.DataLayout(); err != nil {
					return nil, nil, err
				}
				nt.Mode = "MODE1/2048"
				fileType = "BINARY"
				name = trackFileName(base, t.Number, "iso")
				if firstData {
					name = base + ".iso"
					firstData = false
				}
				size, _ := srcEntry.size()
				if rt.FrameSize == 2048 && len(f.Tracks) == 1 && rt.Start == 0 && rt.End == size && size%2048 == 0 {
					o.entry = srcEntry // already exactly the track's user data
				} else {
					o.job = &mediaJob{kind: "iso", src: srcEntry, track: rt}
				}
			}

			o.path = path.Join(dir, name)
			if o.job != nil {
				jobs = append(jobs, o.job)
				changed = true
			} else if o.path != src {
				changed = true
			}
			plan.outputs = append(plan.outputs, o)
			out.Files = append(out.Files, cue.File{Name: name, Type: fileType, Tracks: []cue.Track{nt}})
		}
	}

	plan.text = out.Format()
	if !changed && bytes.Equal(plan.text, data) {
		return nil, nil, nil // already converted
	}
	return plan, jobs, nil
}

// trackFileName is a converted track's file name other than the first data
// track's (§6.4): "<base> (Track NN).<ext>".
func trackFileName(base string, number int, ext string) string {
	return fmt.Sprintf("%s (Track %02d).%s", base, number, ext)
}

// checkOutputCollisions rejects a conversion whose output path would
// replace a file the sheet didn't reference, or another sheet's output.
func checkOutputCollisions(fs *fileSet, plans []*sheetPlan) error {
	claimed := map[string]string{} // lower-case output path -> cue path
	var errs []error
	for _, plan := range plans {
		own := map[string]bool{strings.ToLower(plan.cuePath): true}
		for _, p := range plan.originals {
			own[strings.ToLower(p)] = true
		}
		for _, o := range plan.outputs {
			lower := strings.ToLower(o.path)
			if other, ok := claimed[lower]; ok {
				errs = append(errs, fmt.Errorf("%s and %s both convert to %s (§6.4)", other, plan.cuePath, o.path))
				continue
			}
			claimed[lower] = plan.cuePath
			if _, exists := fs.get(o.path); exists && !own[lower] {
				errs = append(errs, fmt.Errorf("%s: converted track %s would replace an existing file (§6.4)", plan.cuePath, o.path))
			}
		}
	}
	return errors.Join(errs...)
}

// runJobs runs every job in parallel, writing each output to a numbered
// file in dir.
func runJobs(jobs []*mediaJob, dir string, oggQuality int) error {
	for _, j := range jobs {
		if j.kind == "ogg" {
			if _, err := exec.LookPath("ffmpeg"); err != nil {
				return fmt.Errorf("ffmpeg is required to encode Ogg Vorbis audio tracks (set [pack].audio = \"wav\" to avoid it): %w", err)
			}
			break
		}
	}

	if err := materializeShared(jobs, dir); err != nil {
		return err
	}

	status.Step("Converting disc images")
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs []error
	finished := 0
	sem := make(chan struct{}, runtime.NumCPU())

	for i, j := range jobs {
		j.out = filepath.Join(dir, fmt.Sprintf("%04d.%s", i, j.kind))
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			err := j.run(oggQuality)
			mu.Lock()
			if err != nil {
				errs = append(errs, fmt.Errorf("track %d (%s): %w", j.track.Track.Number, j.track.FilePath, err))
			}
			finished++
			status.Count(finished, len(jobs))
			mu.Unlock()
		}()
	}
	wg.Wait()
	return errors.Join(errs...)
}

// materializeShared decompresses, once, every compressed source (an
// archive entry being repacked) that more than one job reads, to a file in
// dir that those jobs then read instead. Each job reads its own track
// concurrently, and a compressed stream can only be read from its start,
// so without this every track would decompress the file up to its own
// start again.
func materializeShared(jobs []*mediaJob, dir string) error {
	count := map[*fileEntry]int{}
	for _, j := range jobs {
		if j.src.data == nil && j.src.source != nil {
			count[j.src]++
		}
	}
	disk := map[*fileEntry]*fileEntry{}
	for i, j := range jobs {
		if count[j.src] < 2 {
			continue
		}
		if _, ok := disk[j.src]; !ok {
			if len(disk) == 0 {
				status.Step("Decompressing disc images")
			}
			p := filepath.Join(dir, fmt.Sprintf("source-%04d", i))
			if err := writeEntry(j.src, p); err != nil {
				return fmt.Errorf("%s: %w", j.track.FilePath, err)
			}
			disk[j.src] = diskEntry(p, j.src.modified)
		}
		j.src = disk[j.src]
	}
	return nil
}

// writeEntry streams e's content to a new file at p.
func writeEntry(e *fileEntry, p string) error {
	r, err := e.reader()
	if err != nil {
		return err
	}
	if c, ok := r.(io.Closer); ok {
		defer c.Close()
	}
	f, err := os.Create(p)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// run writes the job's output file: the track's normalized content
// (§7.2), as an ISO image, a WAV file, or Ogg Vorbis encoded by ffmpeg.
func (j *mediaJob) run(oggQuality int) error {
	rt := j.track
	length := rt.End - rt.Start
	if j.kind != "iso" {
		length -= length % 4 // whole 16-bit stereo samples only
	}

	rc, err := j.src.openRange(rt.Start, length)
	if err != nil {
		return err
	}
	defer rc.Close()
	r, err := rt.Normalize(rc)
	if err != nil {
		return err
	}

	if j.kind == "ogg" {
		return encodeOgg(r, j.out, oggQuality)
	}

	f, err := os.Create(j.out)
	if err != nil {
		return err
	}
	w := bufio.NewWriterSize(f, 1<<20)
	if j.kind == "wav" {
		writeWAVHeader(w, uint32(length))
	}
	n, err := io.Copy(w, r)
	if err == nil && j.kind == "wav" && n != length {
		err = fmt.Errorf("short read: %d of %d bytes", n, length)
	}
	if err == nil {
		err = w.Flush()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// writeWAVHeader writes a canonical 44-byte RIFF header for dataLen bytes
// of 16-bit, 44.1 kHz stereo PCM: the format DOSBox Pure's
// CDROM_Interface_Image::AudioFile plays without resampling
// (src/dos/cdrom_image.cpp).
func writeWAVHeader(w io.Writer, dataLen uint32) {
	const rate, channels, bits = 44100, 2, 16
	le := binary.LittleEndian
	h := make([]byte, 44)
	copy(h[0:], "RIFF")
	le.PutUint32(h[4:], 36+dataLen)
	copy(h[8:], "WAVEfmt ")
	le.PutUint32(h[16:], 16)
	le.PutUint16(h[20:], 1) // PCM
	le.PutUint16(h[22:], channels)
	le.PutUint32(h[24:], rate)
	le.PutUint32(h[28:], rate*channels*bits/8)
	le.PutUint16(h[32:], channels*bits/8)
	le.PutUint16(h[34:], bits)
	copy(h[36:], "data")
	le.PutUint32(h[40:], dataLen)
	w.Write(h)
}

// encodeOgg encodes raw CD-DA (16-bit little-endian, 44.1 kHz stereo) from
// r to an Ogg Vorbis file. -fflags +bitexact makes the Ogg muxer use fixed
// stream serial numbers instead of random ones, and -flags +bitexact keeps
// the encoder's version string out of the stream, so the same ffmpeg and
// libvorbis produce the same bytes.
func encodeOgg(r io.Reader, out string, quality int) error {
	cmd := exec.Command("ffmpeg", "-nostdin", "-loglevel", "error",
		"-f", "s16le", "-ar", "44100", "-ac", "2", "-i", "pipe:0",
		"-map_metadata", "-1",
		"-c:a", "libvorbis", "-q:a", fmt.Sprint(quality),
		"-flags", "+bitexact", "-fflags", "+bitexact",
		"-f", "ogg", "-y", out)
	cmd.Stdin = r
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// UnconvertedMedia returns every CUE sheet among an archive's files that
// §6.4 would still change - its tracks aren't in their converted form or
// aren't named as §6.4 requires - so verify can apply §6.4 to an archive
// with the same rules build uses. It reads only the CUE sheets themselves.
func UnconvertedMedia(files []*zip.File, audio string) []string {
	fs := zipFileSet(files, nil)

	var out []string
	for _, p := range fs.paths() {
		if !strings.EqualFold(path.Ext(p), ".cue") {
			continue
		}
		if plan, _, err := planSheet(fs, p, audio); err == nil && plan != nil {
			out = append(out, p)
		}
	}
	return out
}

// ArchiveMedia applies §7.3 to an archive's own disc images against
// declared, as repack does, returning its warnings: a media not found, or
// tracks in lossy files that couldn't be verified. Entries are streamed.
//
// It also returns, by entry name, the result of the CRC-32 check of every
// entry it read through to its end, so a caller checking every entry's
// CRC-32 (verify) doesn't decompress a disc image a second time.
func ArchiveMedia(files []*zip.File, declared []manifest.Media) (warnings []string, crc map[string]error, err error) {
	var mu sync.Mutex
	crc = map[string]error{}
	fs := zipFileSet(files, func(name string, err error) {
		mu.Lock()
		crc[name] = err
		mu.Unlock()
	})
	_, warnings, err = locateMedia(fs, declared, nil, nil)
	return warnings, crc, err
}

// zipFileSet is a fileSet over an archive's entries, read on demand. If
// readThrough is set, it is called once for each entry a stream reads to
// its end, with the result of archive/zip's CRC-32 check.
func zipFileSet(files []*zip.File, readThrough func(name string, err error)) *fileSet {
	fs := newFileSet()
	for _, f := range files {
		if strings.HasSuffix(f.Name, "/") {
			continue
		}
		open := f.Open
		if readThrough != nil {
			open = func() (io.ReadCloser, error) {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				return &endChecker{rc: rc, left: int64(f.UncompressedSize64), done: func(err error) { readThrough(f.Name, err) }}, nil
			}
		}
		fs.set(f.Name, sourceEntry(&RawSource{
			UncompressedSize: f.UncompressedSize64,
			Decompressed: func() ([]byte, error) {
				rc, err := open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(rc)
			},
			Open: open,
		}, Epoch))
	}
	return fs
}

// endChecker reads an archive entry's stream and reports, once, how it
// ended: archive/zip checks an entry's CRC-32 only when a read reaches
// io.EOF, which a reader limited to the entry's exact size (openRange's)
// never asks for, so endChecker reads one byte further itself.
type endChecker struct {
	rc       io.ReadCloser
	left     int64
	done     func(error)
	reported bool
}

func (c *endChecker) Read(p []byte) (int, error) {
	if c.left <= 0 && c.reported {
		return 0, io.EOF
	}
	n, err := c.rc.Read(p)
	c.left -= int64(n)
	switch {
	case c.reported:
	case err == io.EOF:
		c.report(nil) // archive/zip has checked the size and CRC-32
	case err != nil:
		c.report(err)
	case c.left <= 0:
		var probe [1]byte
		k, perr := c.rc.Read(probe[:])
		switch {
		case k > 0:
			c.report(zip.ErrFormat) // longer than its recorded size
		case perr == io.EOF:
			c.report(nil)
		case perr != nil:
			c.report(perr)
		}
	}
	return n, err
}

func (c *endChecker) report(err error) {
	c.reported = true
	c.done(err)
}

func (c *endChecker) Close() error { return c.rc.Close() }
