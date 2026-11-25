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
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
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

	r.Discs = discs(zr.File, byName)
	if r.Manifest != nil {
		matchMedia(r.Discs, r.Manifest.Source.Media)
	}
	r.Parents = parents(p, zr.File)
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

// discs lists the archive's disc images: every CUE sheet, and every ISO no
// sheet refers to.
func discs(files []*zip.File, byName map[string]*zip.File) []Disc {
	var out []Disc
	used := map[string]bool{}
	for _, e := range files {
		if !strings.EqualFold(path.Ext(e.Name), ".cue") {
			continue
		}
		data, err := readAll(e)
		if err != nil {
			continue
		}
		sheet, err := cue.Parse(data)
		if err != nil {
			continue
		}
		d := Disc{Path: e.Name}
		dir := path.Dir(e.Name)
		for _, cf := range sheet.Files {
			name := path.Join(dir, strings.ReplaceAll(cf.Name, `\`, "/"))
			used[strings.ToUpper(name)] = true
			if f := byName[strings.ToUpper(name)]; f != nil {
				d.Size += int64(f.UncompressedSize64)
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
		if strings.EqualFold(path.Ext(e.Name), ".iso") && !used[strings.ToUpper(e.Name)] {
			out = append(out, Disc{Path: e.Name, Data: 1, Size: int64(e.UncompressedSize64)})
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
	line("File", "%s", filepath.Base(r.Path))
	line("Size", "%s", size(r.Size))
	line("SHA-256", "%s (this file)", r.SHA256)
	line("Content", "%s (its files but DOSZ.TOML: §9)", r.Content)

	m := r.Manifest
	if m == nil {
		line("Manifest", "none embedded (not built by a DOSZ implementation)")
	} else {
		line("Name", "%s", m.Name)
		for _, kv := range bootLines(&m.Boot) {
			line(kv[0], "%s", kv[1])
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
	line("Packed", "%s (%s)", size(total.Compressed), ratio(total))
	if r.Deflated.Files > 0 {
		line("", "deflated  %-11s %10s to %s (%s)", plural(r.Deflated.Files, "file"), size(r.Deflated.Size), size(r.Deflated.Compressed), ratio(r.Deflated))
	}
	if r.Stored.Files > 0 {
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
			switch {
			case d.Ogg == d.Audio:
				a += " (Ogg Vorbis)"
			case d.WAV == d.Audio:
				a += " (WAV)"
			default:
				a += fmt.Sprintf(" (%d WAV, %d Ogg Vorbis)", d.WAV, d.Ogg)
			}
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
