package main

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"

	"github.com/DemiCloud/doszer/internal/build"
	"github.com/DemiCloud/doszer/internal/install"
	"github.com/DemiCloud/doszer/internal/manifest"
	"github.com/DemiCloud/doszer/internal/redump"
	"github.com/DemiCloud/doszer/internal/runlog"
)

// sourceFiles are a source's files a run attaches as they were before
// and, if it changed them, after: what init, start and install wrote.
var sourceFiles = []string{manifest.FileName, "WINDOWS.BAT"}

// before holds sourceFiles' content when the run started, by name.
var before = map[string][]byte{}

// recordSourceBefore attaches source's files as the run found them.
func recordSourceBefore(source string) {
	for _, name := range sourceFiles {
		if data, err := os.ReadFile(filepath.Join(source, name)); err == nil {
			before[name] = data
			runlog.Attach(name+".before", data)
		}
	}
	setName(before[manifest.FileName])
}

// setName names the run's game from a manifest, unless it has a name.
func setName(data []byte) {
	if data == nil {
		return
	}
	if m, err := manifest.Parse(data, ""); err == nil && m.Name != "" {
		runlog.Update(func(r *runlog.Report) {
			if r.Name == "" {
				r.Name = m.Name
			}
		})
	}
}

// recordSourceAfter attaches the source's files the run changed, as it
// left them.
func recordSourceAfter() {
	if !runlog.Active() {
		return
	}
	var source string
	runlog.Update(func(r *runlog.Report) { source = r.Source })
	if source == "" {
		return
	}
	for _, name := range sourceFiles {
		data, err := os.ReadFile(filepath.Join(source, name))
		if err != nil || bytes.Equal(data, before[name]) {
			continue
		}
		runlog.Attach(name+".after", data)
		if name == manifest.FileName {
			setName(data)
		}
	}
}

// recordBuild records what build or repack wrote: the plan, the media
// matched, the archive's entries and its embedded manifest.
func recordBuild(res *build.Result) {
	if !runlog.Active() {
		return
	}
	b := &runlog.Build{Trimmed: res.Trimmed, Suggestions: res.Suggestions}
	for _, f := range res.Collected {
		b.Input = append(b.Input, runlog.File{Path: f.Path, Size: f.Size, Modified: f.Modified})
	}
	for _, m := range res.Moved {
		b.Moved = append(b.Moved, runlog.Move{From: m.From, To: m.To})
	}
	for i, rule := range res.Manifest.Layout.Rewrite {
		rw := runlog.Rewrite{From: rule.From, To: rule.To}
		for _, h := range res.RewriteHits {
			if h.Rule == i {
				rw.Files = append(rw.Files, h.Path)
			}
		}
		if len(rw.Files) > 0 {
			b.Rewrites = append(b.Rewrites, rw)
		}
	}
	for _, c := range res.Converted {
		b.Converted = append(b.Converted, runlog.Conversion{Cue: c.Cue, Files: c.Files})
	}
	for _, m := range res.Manifest.Source.Media {
		md := runlog.Media{ID: m.ID, Label: m.Label, Redump: m.Redump, RedumpName: m.RedumpName, Image: res.Media[m.ID]}
		for _, t := range m.Tracks {
			md.Tracks = append(md.Tracks, runlog.Track{Type: t.Type, SHA1: t.SHA1, RedumpSHA1: t.RedumpSHA1})
		}
		b.Media = append(b.Media, md)
	}
	a := recordArchive(res.OutputPath)
	b.Archive = a
	runlog.Update(func(r *runlog.Report) {
		r.Build, r.Name = b, res.EffectiveName
	})
}

// recordArchive records path as the run's archive, with its entries and
// embedded manifest, and returns what was read of it (nil if it
// couldn't be).
func recordArchive(path string) *runlog.Archive {
	if !runlog.Active() {
		return nil
	}
	a, err := runlog.ReadArchive(path)
	if err != nil {
		runlog.Logf("reading %s: %v", path, err)
		return nil
	}
	runlog.Update(func(r *runlog.Report) {
		r.Archive = a.Path
		if r.Build == nil {
			r.Build = &runlog.Build{}
		}
		r.Build.Archive = a
	})
	if data := archiveFile(a.Path, manifest.FileName); data != nil {
		runlog.Attach(manifest.FileName+".archive", data)
		setName(data)
	}
	return a
}

// archiveFile returns an archive entry's content, or nil.
func archiveFile(path, name string) []byte {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil
		}
		defer rc.Close()
		data, err := io.ReadAll(io.LimitReader(rc, 16<<20))
		if err != nil {
			return nil
		}
		return data
	}
	return nil
}

// recordInstall records what install found and extracted, and the
// choices it made itself.
func recordInstall(rep *install.Report, opts install.Options) {
	if !runlog.Active() {
		return
	}
	in := &runlog.Install{From: opts.From, Drive: rep.Drive, Dir: rep.Dir, Skipped: rep.Skipped, Kept: rep.Kept, Programs: rep.Programs, Registry: rep.Registry}
	change := func(cs []install.Change) []runlog.Change {
		var out []runlog.Change
		for _, c := range cs {
			out = append(out, runlog.Change{Path: c.Path, Kind: c.Kind.String(), Size: c.Size, Modified: c.Modified})
		}
		return out
	}
	in.Changes, in.Other = change(rep.Changes), change(rep.Other)
	if x := rep.Extracted; x != nil {
		in.Extracted, in.Unchanged = x.Written, x.Unchanged
	}
	for _, d := range rep.DLLs {
		in.DLLs = append(in.DLLs, runlog.Move{From: d.From, To: d.To})
	}
	if r := rep.Retry; r != nil {
		for _, f := range r.Folders {
			in.Folders = append(in.Folders, f.Name)
		}
	}
	runlog.Update(func(r *runlog.Report) { r.Install = in })
	if rep.Detected {
		runlog.Decide("drive", rep.Drive, "D: was unchanged")
	}
	if opts.Dir == "" && rep.DirWhy != "" {
		dir := rep.Dir
		if dir == "" {
			dir = "(the root)"
		}
		runlog.Decide("folder extracted", dir, rep.DirWhy)
	}
	if rep.Exec != "" {
		runlog.Decide("boot.exec", rep.Exec, "the manifest had none: the program the install added")
	}
}

// recordDump records a dumped or imported image's tracks and how they
// compared with Redump (m nil: no track matched).
func recordDump(d *runlog.Dump, tracks []redump.Track, sel *redump.Selected, m *redump.Match) {
	if !runlog.Active() || d == nil {
		return
	}
	for _, t := range tracks {
		d.Tracks = append(d.Tracks, runlog.DumpTrack{Path: t.Path, Size: t.Size, SHA1: t.SHA1, Mode2SHA1: t.Mode2SHA1})
	}
	if sel != nil {
		d.Datfile = &runlog.Datfile{Provider: "Redump", Name: sel.DB.Name, Version: sel.DB.Version, Source: sel.Source, Path: sel.Path}
	}
	if m != nil && m.Disc != nil {
		d.Redump, d.Verified, d.Matching, d.Link = m.Disc.Name, m.Verified, m.Matching, m.Link
	}
	runlog.Update(func(r *runlog.Report) { r.Dump = d })
}
