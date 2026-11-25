// Package info implements `doszer info`: what an archive is, read from it
// without changing or fully checking it (`doszer verify` checks).
package info

import (
	"archive/zip"
	"crypto/sha256"
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
	"strconv"
	"strings"
	"sync"

	"github.com/DemiCloud/doszer/internal/cue"
	"github.com/DemiCloud/doszer/internal/manifest"
	"github.com/DemiCloud/doszer/internal/platform"
)

// Report is what Read found.
type Report struct {
	Path    string
	Size    int64
	SHA256  string // the whole file
	Content string // SHA-256 of the files DOSBox Pure loads (contentHash)
	Comment string // the archive comment

	Manifest *manifest.Manifest // nil: none embedded
	Parents  []Parent           // the parent chain, nearest first

	Files                    int
	Deflated, Stored         Bytes
	Programs                 int
	Need                     platform.Need // what the programs need (platform.Needs)
	Discs                    []Disc
	Padmap, DOSYML, Frontend bool // PADMAP.DBP, DOS.YML, FRONTEND.DBP present

	Source bool // a source directory (ReadSource): no hashes, compression or parents
}

// Bytes is a count of entries and their sizes.
type Bytes struct {
	Files            int
	Size, Compressed int64
}

// Parent is one archive of the parent chain.
type Parent struct {
	Name  string // the marker's name
	Path  string // where it was looked for: beside the archive naming it
	Found bool
	Err   string // why the chain stops here, if it does early
}

// Disc is a disc image in the archive: a CUE sheet, or an ISO on its own.
type Disc struct {
	Path        string
	Data, Audio int   // tracks
	WAV, Ogg    int   // audio tracks by file format
	Size        int64 // its files' sizes
	Media       *manifest.Media
}

// Read reads an archive's report.
func Read(p string) (*Report, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	zr, err := zip.NewReader(f, fi.Size())
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	r := &Report{Path: p, Size: fi.Size(), Comment: zr.Comment}
	var progs []platform.Program
	h := sha256.New()
	if _, err := io.Copy(h, io.NewSectionReader(f, 0, fi.Size())); err != nil {
		return nil, err
	}
	r.SHA256 = hex.EncodeToString(h.Sum(nil))
	if r.Content, err = contentHash(zr.File); err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}

	byName := map[string]*zip.File{}
	for _, e := range zr.File {
		byName[strings.ToUpper(e.Name)] = e
		if strings.HasSuffix(e.Name, "/") {
			continue
		}
		r.Files++
		b := &r.Deflated
		if e.Method == zip.Store {
			b = &r.Stored
		}
		b.Files++
		b.Size += int64(e.UncompressedSize64)
		b.Compressed += int64(e.CompressedSize64)
		if platform.IsProgramName(e.Name) {
			rc, err := e.Open()
			if err == nil {
				if t := platform.Classify(e.Name, rc); t.Platform != platform.NotProgram {
					progs = append(progs, platform.Program{Path: e.Name, Target: t})
				}
				rc.Close()
			}
		}
	}
	r.Programs = len(progs)
	r.Need = platform.Needs(progs)
	_, r.Padmap = byName["PADMAP.DBP"]
	_, r.DOSYML = byName["DOS.YML"]
	_, r.Frontend = byName["FRONTEND.DBP"]

	if e := byName[strings.ToUpper(manifest.FileName)]; e != nil {
		data, err := readAll(e)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", manifest.FileName, err)
		}
		def := strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
		if r.Manifest, err = manifest.Parse(data, def); err != nil {
			return nil, fmt.Errorf("%s: embedded %s: %w", p, manifest.FileName, err)
		}
	}

	r.Discs = discs(zipEntries(zr.File))
	if r.Manifest != nil {
		matchMedia(r.Discs, r.Manifest.Source.Media)
	}
	r.Parents = parents(p, zr.File)
	return r, nil
}

// ReadSource reads a source directory's report: what init and build
// start from, before the manifest's trim, extract and conversions. No
// hashes or compression: there is no archive yet.
func ReadSource(dir string) (*Report, error) {
	r := &Report{Path: dir, Source: true}
	var files []entry
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if rel == manifest.FileName {
			return nil
		}
		files = append(files, entry{rel, fi.Size(), func() ([]byte, error) { return os.ReadFile(p) }})
		r.Files++
		r.Stored.Size += fi.Size()
		switch strings.ToUpper(rel) {
		case "PADMAP.DBP":
			r.Padmap = true
		case "DOS.YML":
			r.DOSYML = true
		case "FRONTEND.DBP":
			r.Frontend = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	progs, err := platform.Scan(dir)
	if err != nil {
		return nil, err
	}
	r.Programs = len(progs)
	r.Need = platform.Needs(progs)
	if data, err := os.ReadFile(filepath.Join(dir, manifest.FileName)); err == nil {
		abs, _ := filepath.Abs(dir)
		if r.Manifest, err = manifest.Parse(data, filepath.Base(abs)); err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Join(dir, manifest.FileName), err)
		}
	}
	r.Discs = discs(files)
	if r.Manifest != nil {
		matchMedia(r.Discs, r.Manifest.Source.Media)
	}
	return r, nil
}

// contentHash is the SHA-256 of what §9's equivalence compares: one line
// per file but DOSZ.TOML, in byte-wise path order, as sha256sum prints it
// ("<SHA-256 of the content>  <path>\n"). The archive comment and the
// manifest (notes included) don't affect it, so neither do rebuilds or
// edits to the manifest that leave the files alone. Reproducible outside
// doszer: unzip the archive, then, in it,
// find . -type f ! -name DOSZ.TOML | LC_ALL=C sort | sed 's|^./||' |
// xargs -d '\n' sha256sum | sha256sum.
func contentHash(files []*zip.File) (string, error) {
	var es []*zip.File
	for _, e := range files {
		if !strings.HasSuffix(e.Name, "/") && !strings.EqualFold(e.Name, manifest.FileName) {
			es = append(es, e)
		}
	}
	sort.Slice(es, func(i, j int) bool { return es[i].Name < es[j].Name })
	sums := make([]string, len(es))
	errs := make([]error, len(es))
	var wg sync.WaitGroup
	sem := make(chan struct{}, runtime.GOMAXPROCS(0))
	for i, e := range es {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			rc, err := e.Open()
			if err != nil {
				errs[i] = err
				return
			}
			defer rc.Close()
			h := sha256.New()
			if _, err := io.Copy(h, rc); err != nil {
				errs[i] = fmt.Errorf("%s: %w", e.Name, err)
				return
			}
			sums[i] = hex.EncodeToString(h.Sum(nil))
		}()
	}
	wg.Wait()
	h := sha256.New()
	for i, e := range es {
		if errs[i] != nil {
			return "", errs[i]
		}
		fmt.Fprintf(h, "%s  %s\n", sums[i], e.Name)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func readAll(e *zip.File) ([]byte, error) {
	rc, err := e.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// entry is a file of an archive or source directory, for discs.
type entry struct {
	name string // slash-separated
	size int64
	read func() ([]byte, error)
}

func zipEntries(files []*zip.File) []entry {
	out := make([]entry, 0, len(files))
	for _, f := range files {
		if strings.HasSuffix(f.Name, "/") {
			continue
		}
		out = append(out, entry{f.Name, int64(f.UncompressedSize64), func() ([]byte, error) { return readAll(f) }})
	}
	return out
}

// discs lists the disc images: every CUE sheet, and every ISO no sheet
// refers to.
func discs(files []entry) []Disc {
	byName := map[string]entry{}
	for _, e := range files {
		byName[strings.ToUpper(e.name)] = e
	}
	var out []Disc
	used := map[string]bool{}
	for _, e := range files {
		if !strings.EqualFold(path.Ext(e.name), ".cue") || e.size > 1<<20 {
			continue
		}
		data, err := e.read()
		if err != nil {
			continue
		}
		sheet, err := cue.Parse(data)
		if err != nil {
			continue
		}
		d := Disc{Path: e.name}
		dir := path.Dir(e.name)
		for _, cf := range sheet.Files {
			name := path.Join(dir, strings.ReplaceAll(cf.Name, `\`, "/"))
			used[strings.ToUpper(name)] = true
			if f, ok := byName[strings.ToUpper(name)]; ok {
				d.Size += f.size
			}
			for _, t := range cf.Tracks {
				if t.Mode != "AUDIO" {
					d.Data++
					continue
				}
				d.Audio++
				switch strings.ToLower(path.Ext(cf.Name)) {
				case ".ogg":
					d.Ogg++
				case ".wav":
					d.WAV++
				}
			}
		}
		out = append(out, d)
	}
	for _, e := range files {
		if strings.EqualFold(path.Ext(e.name), ".iso") && !used[strings.ToUpper(e.name)] {
			out = append(out, Disc{Path: e.name, Data: 1, Size: e.size})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// matchMedia pairs discs with declared media by file name: build names a
// sheet matching a media <name>.cue or "<name> (<id>).cue" (§6.4).
func matchMedia(ds []Disc, media []manifest.Media) {
	for i := range ds {
		base := strings.TrimSuffix(path.Base(ds[i].Path), path.Ext(ds[i].Path))
		for j := range media {
			m := &media[j]
			if len(media) == 1 || strings.HasSuffix(base, "("+m.ID+")") || strings.EqualFold(base, m.Label) {
				ds[i].Media = m
				break
			}
		}
	}
}

// parents follows the parent chain from the archive at p, each parent
// looked for beside the archive naming it, as DOSBox Pure does
// (src/dos/drive_zip.cpp).
func parents(p string, files []*zip.File) []Parent {
	var out []Parent
	seen := map[string]bool{}
	if abs, err := filepath.Abs(p); err == nil {
		seen[abs] = true
	}
	for {
		names := markers(files)
		if len(names) == 0 {
			return out
		}
		if len(names) > 1 {
			out = append(out, Parent{Name: strings.Join(names, ", "), Err: "more than one parent marker: DOSBox Pure refuses the archive"})
			return out
		}
		next := Parent{Name: names[0], Path: filepath.Join(filepath.Dir(p), names[0])}
		abs, _ := filepath.Abs(next.Path)
		if seen[abs] {
			next.Err = "loops back to an archive already in the chain"
			return append(out, next)
		}
		seen[abs] = true
		zr, err := zip.OpenReader(next.Path)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				next.Err = err.Error()
			}
			return append(out, next)
		}
		next.Found = true
		out = append(out, next)
		files = zr.File
		p = next.Path
		defer zr.Close()
	}
}

// markers returns the names of the parents the archive's root parent
// markers (empty "<name>.parent" files) name.
func markers(files []*zip.File) []string {
	var out []string
	for _, e := range files {
		if !strings.Contains(e.Name, "/") && strings.HasSuffix(strings.ToLower(e.Name), ".parent") && e.UncompressedSize64 == 0 && len(e.Name) > 7 {
			out = append(out, e.Name[:len(e.Name)-7])
		}
	}
	return out
}

// Print writes r for a person to read.
func Print(w io.Writer, r *Report) {
	line := func(k, format string, a ...any) {
		if k != "" {
			k += ":"
		}
		fmt.Fprintf(w, "%-10s %s\n", k, fmt.Sprintf(format, a...))
	}
	if r.Source {
		line("Source", "%s (before the manifest's trim, extract and conversions)", r.Path)
	} else {
		line("File", "%s", filepath.Base(r.Path))
		line("Size", "%s", size(r.Size))
		line("SHA-256", "%s (this file)", r.SHA256)
		line("Content", "%s (its files but DOSZ.TOML: §9)", r.Content)
	}

	m := r.Manifest
	switch {
	case m == nil && r.Source:
		line("Manifest", "none yet (doszer init writes one)")
	case m == nil:
		line("Manifest", "none embedded (not built by a DOSZ implementation)")
	default:
		line("Name", "%s", m.Name)
		for _, kv := range bootLines(&m.Boot) {
			line(kv[0], "%s", kv[1])
		}
		if r.Source && m.Boot.Parent != "" {
			line("Parent", "%s", m.Boot.Parent)
		}
	}
	for i, p := range r.Parents {
		k := ""
		if i == 0 {
			k = "Parent"
		}
		state := "found"
		switch {
		case p.Err != "":
			state = p.Err
		case !p.Found:
			state = "not found beside it"
		}
		line(k, "%s%s (%s)", strings.Repeat("  ", i), p.Name, state)
	}
	if r.Programs > 0 {
		line("Programs", "%d", r.Programs)
		line("OS", "%s", r.Need)
		if r.Need.Setup.Platform != platform.NotProgram {
			line("Setup", "%s", r.Need.Setup)
		}
	}

	total := Bytes{Files: r.Files, Size: r.Deflated.Size + r.Stored.Size, Compressed: r.Deflated.Compressed + r.Stored.Compressed}
	line("Contents", "%s, %s", plural(total.Files, "file"), size(total.Size))
	if !r.Source {
		line("Packed", "%s (%s)", size(total.Compressed), ratio(total))
	}
	if r.Deflated.Files > 0 {
		line("", "deflated  %-11s %10s to %s (%s)", plural(r.Deflated.Files, "file"), size(r.Deflated.Size), size(r.Deflated.Compressed), ratio(r.Deflated))
	}
	if r.Stored.Files > 0 && !r.Source {
		line("", "stored    %-11s %10s", plural(r.Stored.Files, "file"), size(r.Stored.Size))
	}

	for i, d := range r.Discs {
		k := ""
		if i == 0 {
			k = "Discs"
		}
		var tracks []string
		if d.Data > 0 {
			tracks = append(tracks, plural(d.Data, "data track"))
		}
		if d.Audio > 0 {
			a := plural(d.Audio, "audio track")
			var kinds []string
			for _, k := range []struct {
				n    int
				name string
			}{{d.WAV, "WAV"}, {d.Ogg, "Ogg Vorbis"}, {d.Audio - d.WAV - d.Ogg, "raw"}} {
				switch {
				case k.n == d.Audio:
					kinds = []string{k.name}
				case k.n > 0 && k.n < d.Audio:
					kinds = append(kinds, fmt.Sprintf("%d %s", k.n, k.name))
				}
			}
			a += " (" + strings.Join(kinds, ", ") + ")"
			tracks = append(tracks, a)
		}
		line(k, "%s: %s, %s", d.Path, strings.Join(tracks, ", "), size(d.Size))
		if md := d.Media; md != nil {
			label := md.ID
			if md.Label != "" {
				label = md.Label + " (" + md.ID + ")"
			}
			line("", "  declared as %s: %s (§7.2)", label, plural(len(md.Tracks), "track hash"))
			if md.RedumpName != "" {
				line("", "  Redump: %s (matched when the manifest was written)", md.RedumpName)
			}
			if md.Redump != "" {
				line("", "  Redump link: %s", md.Redump)
			}
			for n, t := range md.Tracks {
				line("", "    %2d %-5s %s", n+1, t.Type, t.SHA1)
				if t.RedumpSHA1 != "" {
					line("", "             Redump %s", t.RedumpSHA1)
				}
			}
		}
	}
	if m != nil {
		for _, md := range m.Source.Media {
			found := false
			for _, d := range r.Discs {
				if d.Media != nil && d.Media.ID == md.ID {
					found = true
				}
			}
			if !found {
				line("Media", "%s declared, no disc image in the archive", md.ID)
			}
		}
	}

	var extra []string
	if r.Padmap {
		extra = append(extra, "PADMAP.DBP")
	}
	if r.DOSYML {
		extra = append(extra, "DOS.YML")
	}
	if len(extra) > 0 {
		line("Gamepad", "%s", strings.Join(extra, ", "))
	}

	if m != nil && strings.TrimSpace(m.Notes) != "" {
		fmt.Fprintln(w, "Notes:")
		for _, l := range strings.Split(strings.TrimSpace(m.Notes), "\n") {
			fmt.Fprintf(w, "  %s\n", strings.TrimRight(l, "\r"))
		}
	}
	if c := strings.TrimSpace(r.Comment); c != "" {
		fmt.Fprintln(w, "Comment:")
		for _, l := range strings.Split(strings.ReplaceAll(c, "\r\n", "\n"), "\n") {
			fmt.Fprintf(w, "  %s\n", l)
		}
	}
}

// bootLines describes the boot model (§4): the model on one line, what it
// names on the next.
func bootLines(b *manifest.Boot) [][2]string {
	var out [][2]string
	switch {
	case b.OS != "":
		out = append(out, [2]string{"Boot", "OS image"}, [2]string{"Image", b.OS})
	case b.Shell != "":
		out = append(out, [2]string{"Boot", "Shell"}, [2]string{"Shell", b.Shell})
	case b.Exec != "":
		out = append(out, [2]string{"Boot", "DOS"}, [2]string{"Starts", b.Exec})
	default:
		out = append(out, [2]string{"Boot", "DOS, start menu"})
	}
	if b.Disc != "" {
		out = append(out, [2]string{"Mounts", b.Disc})
	}
	return out
}

func plural(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	if strings.HasSuffix(what, "sh") {
		return fmt.Sprintf("%d %ses", n, what)
	}
	return fmt.Sprintf("%d %ss", n, what)
}

func ratio(b Bytes) string {
	if b.Size == 0 {
		return "empty"
	}
	return fmt.Sprintf("%.1f%%", float64(b.Compressed)*100/float64(b.Size))
}

func size(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.2f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
}

// Data is a Report for --format: plain values, named alike in JSON, YAML
// and TOML.
type Data struct {
	File          string       `json:"file" yaml:"file" toml:"file"`
	Source        bool         `json:"source,omitempty" yaml:"source,omitempty" toml:"source,omitempty"` // a source directory: no hashes or compression
	Size          int64        `json:"size" yaml:"size" toml:"size"`
	SHA256        string       `json:"sha256,omitempty" yaml:"sha256,omitempty" toml:"sha256,omitempty"`
	ContentSHA256 string       `json:"content_sha256,omitempty" yaml:"content_sha256,omitempty" toml:"content_sha256,omitempty"`
	Name          string       `json:"name,omitempty" yaml:"name,omitempty" toml:"name,omitempty"`
	Manifest      bool         `json:"manifest" yaml:"manifest" toml:"manifest"`
	Boot          string       `json:"boot,omitempty" yaml:"boot,omitempty" toml:"boot,omitempty"` // dos, menu, shell, os
	Exec          string       `json:"exec,omitempty" yaml:"exec,omitempty" toml:"exec,omitempty"`
	Shell         string       `json:"shell,omitempty" yaml:"shell,omitempty" toml:"shell,omitempty"`
	OSImage       string       `json:"os_image,omitempty" yaml:"os_image,omitempty" toml:"os_image,omitempty"`
	Disc          string       `json:"disc,omitempty" yaml:"disc,omitempty" toml:"disc,omitempty"`
	Parents       []ParentData `json:"parents,omitempty" yaml:"parents,omitempty" toml:"parents,omitempty"`
	Programs      int          `json:"programs" yaml:"programs" toml:"programs"`
	OS            string       `json:"os,omitempty" yaml:"os,omitempty" toml:"os,omitempty"`
	Setup         string       `json:"setup,omitempty" yaml:"setup,omitempty" toml:"setup,omitempty"`
	Files         int          `json:"files" yaml:"files" toml:"files"`
	Bytes         int64        `json:"bytes" yaml:"bytes" toml:"bytes"`
	Packed        int64        `json:"packed" yaml:"packed" toml:"packed"`
	Deflated      BytesData    `json:"deflated" yaml:"deflated" toml:"deflated"`
	Stored        BytesData    `json:"stored" yaml:"stored" toml:"stored"`
	Discs         []DiscData   `json:"discs,omitempty" yaml:"discs,omitempty" toml:"discs,omitempty"`
	Gamepad       []string     `json:"gamepad,omitempty" yaml:"gamepad,omitempty" toml:"gamepad,omitempty"`
	Notes         string       `json:"notes,omitempty" yaml:"notes,omitempty" toml:"notes,omitempty"`
	Comment       string       `json:"comment,omitempty" yaml:"comment,omitempty" toml:"comment,omitempty"`
}

// ParentData is a Parent for --format.
type ParentData struct {
	Name  string `json:"name" yaml:"name" toml:"name"`
	Found bool   `json:"found" yaml:"found" toml:"found"`
	Error string `json:"error,omitempty" yaml:"error,omitempty" toml:"error,omitempty"`
}

// BytesData is a Bytes for --format.
type BytesData struct {
	Files  int   `json:"files" yaml:"files" toml:"files"`
	Bytes  int64 `json:"bytes" yaml:"bytes" toml:"bytes"`
	Packed int64 `json:"packed" yaml:"packed" toml:"packed"`
}

// DiscData is a Disc for --format.
type DiscData struct {
	Path        string      `json:"path" yaml:"path" toml:"path"`
	DataTracks  int         `json:"data_tracks" yaml:"data_tracks" toml:"data_tracks"`
	AudioTracks int         `json:"audio_tracks" yaml:"audio_tracks" toml:"audio_tracks"`
	WAV         int         `json:"wav" yaml:"wav" toml:"wav"`
	Ogg         int         `json:"ogg" yaml:"ogg" toml:"ogg"`
	Bytes       int64       `json:"bytes" yaml:"bytes" toml:"bytes"`
	Media       string      `json:"media,omitempty" yaml:"media,omitempty" toml:"media,omitempty"`
	RedumpName  string      `json:"redump_name,omitempty" yaml:"redump_name,omitempty" toml:"redump_name,omitempty"`
	Redump      string      `json:"redump,omitempty" yaml:"redump,omitempty" toml:"redump,omitempty"`
	Tracks      []TrackData `json:"tracks,omitempty" yaml:"tracks,omitempty" toml:"tracks,omitempty"`
}

// TrackData is a declared track hash for --format.
type TrackData struct {
	Type       string `json:"type" yaml:"type" toml:"type"`
	SHA1       string `json:"sha1" yaml:"sha1" toml:"sha1"`
	RedumpSHA1 string `json:"redump_sha1,omitempty" yaml:"redump_sha1,omitempty" toml:"redump_sha1,omitempty"`
}

// Data returns r for --format.
func (r *Report) Data() Data {
	d := Data{
		File: filepath.Base(r.Path), Source: r.Source, Size: r.Size, SHA256: r.SHA256, ContentSHA256: r.Content,
		Programs: r.Programs, Files: r.Files,
		Bytes:    r.Deflated.Size + r.Stored.Size,
		Packed:   r.Deflated.Compressed + r.Stored.Compressed,
		Deflated: BytesData{r.Deflated.Files, r.Deflated.Size, r.Deflated.Compressed},
		Stored:   BytesData{r.Stored.Files, r.Stored.Size, r.Stored.Compressed},
		Comment:  strings.TrimSpace(strings.ReplaceAll(r.Comment, "\r\n", "\n")),
	}
	if m := r.Manifest; m != nil {
		d.Manifest, d.Name, d.Notes = true, m.Name, strings.TrimSpace(m.Notes)
		b := m.Boot
		d.Exec, d.Shell, d.OSImage, d.Disc = b.Exec, b.Shell, b.OS, b.Disc
		switch {
		case b.OS != "":
			d.Boot = "os"
		case b.Shell != "":
			d.Boot = "shell"
		case b.Exec != "":
			d.Boot = "dos"
		default:
			d.Boot = "menu"
		}
	}
	for _, p := range r.Parents {
		d.Parents = append(d.Parents, ParentData{p.Name, p.Found, p.Err})
	}
	if r.Programs > 0 {
		d.OS = r.Need.String()
		if r.Need.Setup.Platform != platform.NotProgram {
			d.Setup = r.Need.Setup.String()
		}
	}
	for _, x := range r.Discs {
		dd := DiscData{Path: x.Path, DataTracks: x.Data, AudioTracks: x.Audio, WAV: x.WAV, Ogg: x.Ogg, Bytes: x.Size}
		if md := x.Media; md != nil {
			dd.Media, dd.RedumpName, dd.Redump = md.ID, md.RedumpName, md.Redump
			for _, t := range md.Tracks {
				dd.Tracks = append(dd.Tracks, TrackData{t.Type, t.SHA1, t.RedumpSHA1})
			}
		}
		d.Discs = append(d.Discs, dd)
	}
	if r.Padmap {
		d.Gamepad = append(d.Gamepad, "PADMAP.DBP")
	}
	if r.DOSYML {
		d.Gamepad = append(d.Gamepad, "DOS.YML")
	}
	return d
}

// CSVHeader and CSVRow are Data as one CSV row: the scalar fields.
var CSVHeader = []string{"file", "size", "sha256", "content_sha256", "name", "boot", "exec", "shell", "os_image", "disc", "parent", "programs", "os", "setup", "files", "bytes", "packed", "discs"}

// CSVRow returns d's CSVHeader fields.
func (d Data) CSVRow() []string {
	parent := ""
	if len(d.Parents) > 0 {
		parent = d.Parents[0].Name
	}
	i := strconv.Itoa
	i64 := func(n int64) string { return strconv.FormatInt(n, 10) }
	return []string{d.File, i64(d.Size), d.SHA256, d.ContentSHA256, d.Name, d.Boot, d.Exec, d.Shell, d.OSImage, d.Disc, parent, i(d.Programs), d.OS, d.Setup, i(d.Files), i64(d.Bytes), i64(d.Packed), i(len(d.Discs))}
}
