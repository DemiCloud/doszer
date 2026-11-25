// Package build implements `doszer build`: spec §6's processing model,
// producing a deterministic archive per §8. [[extract]] (§5.7) and media
// identification (§7) and disc image conversion (§6.4) are implemented.
package build

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/DemiCloud/doszer/internal/interrupt"
	"github.com/DemiCloud/doszer/internal/manifest"
	"github.com/DemiCloud/doszer/internal/status"
	"github.com/DemiCloud/doszer/internal/zipw"
)

// Options configures a build.
type Options struct {
	SourceDir  string // Directory containing DOSZ.TOML.
	OutputPath string // Archive path to write; defaults to "<effective name>.dosz" in the working directory.
	Name       string // Overrides the manifest's effective name, if non-empty.
	DryRun     bool   // Compute and report the plan without writing an archive.
	Strict     bool   // Fail the build if any §11 warning condition occurs.
	OggQuality int    // libvorbis quality for Ogg audio tracks, -1 to 10 (internal/config's ogg_quality).

	// Provenance, if set, is recorded in the archive comment (§8.4).
	Provenance *Provenance

	// BeforeRename, if set, is called once the archive is written and
	// before it is renamed into place: Repack's caller closes its input
	// archive there, which Windows can't replace while it is open.
	BeforeRename func()
}

// Provenance is the build information an archive comment records.
type Provenance struct {
	Generator string // implementation and version, e.g. "doszer 1.0"
	Homepage  string // the implementation's URL, if any
}

// Comment returns the archive comment for an archive named name, built
// from manifest format format: the name, then what built it, then the
// homepage, CRLF-separated. No date: two builds of one source by one
// version are then the same file, comment included.
func (p *Provenance) Comment(name string, format int) string {
	lines := []string{
		name,
		fmt.Sprintf("Built by %s (DOSZ manifest format %d)", p.Generator, format),
	}
	if p.Homepage != "" {
		lines = append(lines, p.Homepage)
	}
	return strings.Join(lines, "\r\n") + "\r\n"
}

// Result reports what a build did or would do.
type Result struct {
	Manifest      *manifest.Manifest
	EffectiveName string

	Trimmed      []string // Paths removed by layout.trim.
	TrimWarnings []string // layout.trim patterns that matched nothing.

	Moved []Move // Paths renamed by flatten, sorted by old path.

	RewriteHits     []RewriteHit // Files each layout.rewrite rule changed.
	RewriteWarnings []string     // layout.rewrite rules that matched nothing.

	Converted     []Conversion // Disc images §6.4 converted or renamed.
	MediaWarnings []string     // media §7.3 could not find or fully verify; CUE sheets §6.4 could not convert.
	PathWarnings  []string     // collected paths differing only in case (§2), of which one was kept.

	Suggestions      []string // Rewrite rules suggested to cover paths flatten changed.
	BinaryReferences []string // Binary files referencing a path flatten changed.

	Entries []string // Final, sorted archive entry paths.

	OutputPath string // Where the archive was (or would be) written.

	fs *fileSet
}

// Move is one path renamed by flatten.
type Move struct {
	From string
	To   string
}

// Warnings returns every §11 SHOULD-warn condition hit during the build.
func (r *Result) Warnings() []string {
	w := append(append([]string{}, r.PathWarnings...), r.TrimWarnings...)
	w = append(w, r.RewriteWarnings...)
	return append(w, r.MediaWarnings...)
}

// Run executes the §6 build pipeline and, unless opts.DryRun, writes the
// archive.
func Run(opts Options) (*Result, error) {
	m, err := manifest.Load(opts.SourceDir)
	if err != nil {
		return nil, err
	}

	var tempFiles []*os.File
	defer func() {
		for _, f := range tempFiles {
			f.Close()
			os.Remove(f.Name())
		}
	}()
	if opts.OutputPath == "" {
		opts.OutputPath = effectiveName(m, opts) + ".dosz"
	}
	// Beside the output, like runPipeline's temporary files: an extracted
	// data track can be hundreds of megabytes, and /tmp may be RAM-backed.
	// The output itself is left out, should it be in the source (a build
	// run from the source directory): it is replaced, not packed.
	fs, pathWarnings, mediaWarnings, err := collect(opts.SourceDir, m, opts.OutputPath, &tempFiles)
	if err != nil {
		return nil, err
	}

	return runPipeline(fs, m, opts, pathWarnings, mediaWarnings)
}

// Entry is one already-collected file for Repack: its content and the
// modification time (§8.2) to carry forward - typically whatever was
// already recorded on the archive's own entry, or Epoch for an entry with
// no source of its own (e.g. a [[file]] entry layered on top).
//
// Exactly one of Data or Source should be set. Source lets Repack defer
// decompressing an entry until the pipeline actually reads or changes its
// content, so writeArchive can copy an untouched entry's already-compressed
// bytes straight through instead of decompressing and recompressing it.
type Entry struct {
	Data     []byte
	Source   *RawSource
	Modified time.Time
}

// Repack runs §6.2 onward (trim through the deterministic write) against an
// already-collected set of entries, rather than a source directory: used by
// `doszer repack` to rebuild an archive from nothing but its own embedded
// manifest, treating the archive's own content as the collected build.
// entries' keys are archive-relative paths; DOSZ.TOML must not be among
// them (§6.1 excludes it from collect, same as a source directory). Of two
// keys differing only in case, the later in sorted order is kept, with a
// warning.
func Repack(entries map[string]Entry, m *manifest.Manifest, opts Options) (*Result, error) {
	fs := newFileSet()
	var pathWarnings []string
	for _, p := range sortedKeys(entries) {
		e := entries[p]
		fe := dataEntry(e.Data, e.Modified)
		if e.Source != nil {
			fe = sourceEntry(e.Source, e.Modified)
		}
		if w := fs.collected(p, fe); w != "" {
			pathWarnings = append(pathWarnings, w)
		}
	}
	// The archive's disc images are verified as a source's would be (§7.3);
	// [[extract]] already happened, so a missing media is only a warning.
	media, mediaWarnings, err := locateMedia(fs, m.Source.Media, nil, nil)
	if err != nil {
		return nil, err
	}
	// [icon] media is read again if the archive has it; else addIcon
	// keeps the archive's DOSZ.ICO.
	if m.Icon != nil && m.Icon.Media != "" {
		if _, ok := media[m.Icon.Media]; ok {
			var tempFiles []*os.File
			defer func() {
				for _, f := range tempFiles {
					f.Close()
					os.Remove(f.Name())
				}
			}()
			out := opts.OutputPath
			if out == "" {
				out = effectiveName(m, opts) + ".dosz"
			}
			if fs.mediaIcon, err = iconFromMedia(media, m.Icon, filepath.Dir(out), &tempFiles); err != nil {
				return nil, err
			}
		} else {
			mediaWarnings = append(mediaWarnings, fmt.Sprintf("icon: media %q not found: keeping the archive's %s", m.Icon.Media, IconName))
		}
	}
	return runPipeline(fs, m, opts, pathWarnings, mediaWarnings)
}

// runPipeline runs §6.2 onward against fs, shared by Run and Repack.
// pathWarnings are collect's (fileSet.collected), mediaWarnings §7.3's,
// from verifying fs's media.
func runPipeline(fs *fileSet, m *manifest.Manifest, opts Options, pathWarnings, mediaWarnings []string) (_ *Result, err error) {
	effectiveName := effectiveName(m, opts)
	outputPath := opts.OutputPath
	if outputPath == "" {
		outputPath = effectiveName + ".dosz"
	}

	res := &Result{Manifest: m, EffectiveName: effectiveName, OutputPath: outputPath, PathWarnings: pathWarnings, fs: fs}

	status.Step("Applying the manifest")
	res.Trimmed, res.TrimWarnings = trim(fs, m.Layout.Trim)

	moved, dropped, err := flatten(fs, m.Layout.Flatten)
	if err != nil {
		return nil, err
	}
	for _, from := range sortedKeys(moved) {
		res.Moved = append(res.Moved, Move{From: from, To: moved[from]})
	}

	if err := renameMediaCue(fs, m.Source.Media, &m.Boot, effectiveName); err != nil {
		return nil, err
	}

	// Converted tracks, zipw's spools and the archive itself are written
	// next to the output, which needs room for them anyway, rather than to a
	// possibly RAM-backed /tmp; the directory lives until the archive is
	// renamed into place.
	tmpDir, cleanup := tempDirFor(outputPath)
	defer func() {
		if cerr := cleanup(); err == nil {
			err = cerr
		}
	}()
	if opts.OggQuality < -1 || opts.OggQuality > 10 {
		return nil, fmt.Errorf("ogg_quality %d is out of range (-1 to 10)", opts.OggQuality)
	}
	var convertWarnings []string
	res.Converted, convertWarnings, err = convertMedia(fs, m.AudioEncoding(), opts.OggQuality, opts.DryRun, tmpDir)
	if err != nil {
		return nil, err
	}
	res.MediaWarnings = append(mediaWarnings, convertWarnings...)
	if !opts.DryRun {
		dateMedia(fs)
	}

	hits, rewriteWarnings, err := applyRewrite(fs, m.Layout.Rewrite)
	if err != nil {
		return nil, err
	}
	res.RewriteHits = hits
	res.RewriteWarnings = rewriteWarnings

	// After the manifest's own rules, so only references they leave are
	// suggested.
	if opts.DryRun {
		res.Suggestions, res.BinaryReferences = suggestRewrites(fs, dropped)
	}

	if err := normalizeText(fs); err != nil {
		return nil, err
	}

	exclude(fs)

	if err := ValidateArchiveContent(fs.paths(), m.Model()); err != nil {
		return nil, err
	}

	if err := addIcon(fs, m, fs.mediaIcon); err != nil {
		return nil, err
	}
	if err := addRuntimeFiles(fs, m, effectiveName); err != nil {
		return nil, err
	}

	res.Entries = fs.paths()

	if opts.Strict {
		if warnings := res.Warnings(); len(warnings) > 0 {
			return res, fmt.Errorf("strict mode: %s", strings.Join(warnings, "; "))
		}
	}

	if opts.DryRun {
		return res, nil
	}
	return res, writeOutput(fs, m, opts, res, tmpDir)
}

// writeOutput writes fs to res.OutputPath. The archive is written beside
// the output and renamed into place only once complete: a failed build
// never leaves a partial archive or destroys an existing one - including
// the input, when repack or Refresh writes over it.
func writeOutput(fs *fileSet, m *manifest.Manifest, opts Options, res *Result, tmpDir func() (string, error)) error {
	dir, err := tmpDir()
	if err != nil {
		return err
	}
	partial := filepath.Join(dir, "output.dosz")
	out, err := os.Create(partial)
	if err != nil {
		return err
	}
	comment := ""
	if opts.Provenance != nil {
		comment = opts.Provenance.Comment(res.EffectiveName, m.Format)
	}
	status.Step("Writing %s", filepath.Base(res.OutputPath))
	if err := writeArchive(fs, m, out, dir, comment); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if opts.BeforeRename != nil {
		opts.BeforeRename()
	}
	return os.Rename(partial, res.OutputPath)
}

// tempDirFor returns a function creating, once, a temporary directory
// beside outputPath, and a cleanup removing it.
func tempDirFor(outputPath string) (get func() (string, error), cleanup func() error) {
	var tmp string
	unregister := func() {}
	get = func() (string, error) {
		var err error
		if tmp == "" {
			tmp, err = os.MkdirTemp(filepath.Dir(outputPath), ".doszer-build-")
			if err == nil {
				unregister = interrupt.Remove(tmp)
			}
		}
		return tmp, err
	}
	cleanup = func() error {
		if tmp == "" {
			return nil
		}
		defer unregister()
		return os.RemoveAll(tmp)
	}
	return get, cleanup
}

// Refresh regenerates only §6.8's runtime files from m - AUTOBOOT.DBP,
// the parent marker, PADMAP.DBP, DOS.YML, DOSZ.ICO and DOSZ.TOML - and writes
// entries with them to opts.OutputPath: for changing an archive's boot
// settings in place. Every other entry is written as it is, copied
// through in its compressed form when its store decision (§8.1) allows,
// so the result is the archive repack would produce from m.
func Refresh(entries map[string]Entry, m *manifest.Manifest, opts Options) (_ *Result, err error) {
	fs := newFileSet()
	for _, p := range sortedKeys(entries) {
		e := entries[p]
		fe := dataEntry(e.Data, e.Modified)
		if e.Source != nil {
			fe = sourceEntry(e.Source, e.Modified)
		}
		fs.collected(p, fe)
	}
	name := effectiveName(m, opts)
	res := &Result{Manifest: m, EffectiveName: name, OutputPath: opts.OutputPath, fs: fs}
	if err := ValidateArchiveContent(fs.paths(), m.Model()); err != nil {
		return nil, err
	}
	if err := addIcon(fs, m, nil); err != nil {
		return nil, err
	}
	if err := addRuntimeFiles(fs, m, name); err != nil {
		return nil, err
	}
	res.Entries = fs.paths()
	if opts.DryRun {
		return res, nil
	}
	tmpDir, cleanup := tempDirFor(res.OutputPath)
	defer func() {
		if cerr := cleanup(); err == nil {
			err = cerr
		}
	}()
	return res, writeOutput(fs, m, opts, res, tmpDir)
}

// effectiveName is the manifest's name, or opts.Name if set (§8.3).
func effectiveName(m *manifest.Manifest, opts Options) string {
	if opts.Name != "" {
		return opts.Name
	}
	return m.Name
}

// writeArchive writes every file in fs to w as a deterministic ZIP (§8). An
// entry whose content was never read or changed by the pipeline, and whose
// store decision (§8.1) still matches the compression method it already
// had, is copied through in its already-compressed form rather than being
// decompressed and recompressed - repack's main cost on an archive most of
// whose content a given run leaves untouched.
//
// spoolDir holds zipw's temporary files for large compressed entries.
//
// Entries are compressed concurrently, up to one per CPU ahead of the one
// being written, and written in path order: the output is the same as
// compressing them one at a time.
func writeArchive(fs *fileSet, m *manifest.Manifest, w io.Writer, spoolDir, comment string) error {
	zw := zipw.New(w)
	zw.TempDir = spoolDir
	if comment != "" {
		if err := zw.SetComment(comment); err != nil {
			return err
		}
	}

	type job struct {
		path  string
		e     *fileEntry
		store bool
		raw   *RawSource // copied through instead of prepared
		done  chan prepResult
	}

	paths := fs.paths()
	jobs := make(chan *job, len(paths))
	slots := make(chan struct{}, runtime.GOMAXPROCS(0)) // prepared but not yet written
	stop := make(chan struct{})
	go func() {
		defer close(jobs)
		for _, p := range paths {
			e, _ := fs.get(p)
			j := &job{path: p, e: e, store: ShouldStore(p, m.Pack.Store), done: make(chan prepResult, 1)}
			wantMethod := uint16(zip.Deflate)
			if j.store {
				wantMethod = zip.Store
			}
			if src := e.rawPassthrough(); src != nil && src.Method == wantMethod {
				j.raw = src
				jobs <- j
				continue
			}
			// Checked on its own first: once stop is closed, a select over
			// both would still pick a free slot half the time.
			select {
			case <-stop:
				return
			default:
			}
			select {
			case slots <- struct{}{}:
			case <-stop:
				return
			}
			go func() {
				r, err := j.e.reader()
				if err != nil {
					j.done <- prepResult{err: err}
					return
				}
				c, _ := r.(io.Closer)
				prep, err := zw.Prepare(r, j.store)
				if err != nil && c != nil {
					c.Close()
				}
				j.done <- prepResult{prep, c, err}
			}()
			jobs <- j
		}
	}()

	var err error
	written := 0
	for j := range jobs {
		if err != nil {
			// Release whatever was already being prepared.
			if j.raw == nil {
				if res := <-j.done; res.err == nil {
					res.prep.Close()
					if res.closer != nil {
						res.closer.Close()
					}
				}
			}
			continue
		}
		if err = writeJob(zw, j.path, j.e, j.raw, j.done); err != nil {
			err = fmt.Errorf("%s: %w", j.path, err)
			close(stop)
		}
		if j.raw == nil {
			<-slots
		}
		written++
		status.Count(written, len(paths))
	}
	if err != nil {
		return err
	}
	return zw.Close()
}

// prepResult is an entry writeArchive compressed ahead of writing it.
type prepResult struct {
	prep   *zipw.Prepared
	closer io.Closer // the entry's reader, open until it is written
	err    error
}

// writeJob writes one of writeArchive's entries: raw copied through, or
// else the prepared result from done.
func writeJob(zw *zipw.Writer, p string, e *fileEntry, raw *RawSource, done chan prepResult) error {
	if raw != nil {
		body, err := raw.Raw()
		if err != nil {
			return err
		}
		err = zw.AddRaw(p, raw.Method, raw.CRC32, raw.CompressedSize, raw.UncompressedSize, body, e.modified)
		if c, ok := body.(io.Closer); ok {
			c.Close()
		}
		return err
	}
	res := <-done
	if res.err != nil {
		return res.err
	}
	err := zw.WritePrepared(p, res.prep, e.modified)
	if res.closer != nil {
		res.closer.Close()
	}
	return err
}

// ShouldStore reports whether a path must use the Store method (§8.1):
// an extension listed in pack.store, or .ogg always.
func ShouldStore(p string, storeExts []string) bool {
	ext := strings.TrimPrefix(path.Ext(p), ".")
	if strings.EqualFold(ext, "ogg") {
		return true
	}
	for _, se := range storeExts {
		if strings.EqualFold(ext, se) {
			return true
		}
	}
	return false
}
