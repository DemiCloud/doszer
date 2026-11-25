package install

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"fmt"
	"io"
	"io/fs"
	"path"
	"path/filepath"
	"strings"
)

// copyImportedDLLs copies into the extracted folder the DLLs the
// installer added to, or changed in, Windows' own folders (WINDOWS,
// WINDOWS\SYSTEM) that the folder's programs import: the archive boots
// the OS image as it was, without them, and Windows looks for a program's
// DLLs in its own folder first. Each goes beside the program importing
// it, and the DLLs it imports in turn are followed too.
//
// progs holds the extracted folder's programs, at their drive paths; cfs
// is C: after the session, and cChanges its changes. A copy already in
// the source with different content is a conflict needing opts.Force;
// nothing is written in a dry run. rep.DLLs lists the copies.
func copyImportedDLLs(rep *Report, progs, cfs fs.FS, cChanges []Change, opts Options) error {
	if rep.Extracted == nil {
		return nil
	}
	cands := map[string]Change{} // by lower-case file name
	for _, c := range cChanges {
		if c.Kind == Deleted || !strings.EqualFold(path.Ext(c.Path), ".dll") {
			continue
		}
		if dir := strings.ToLower(path.Dir(c.Path)); dir == "windows" || dir == "windows/system" {
			cands[strings.ToLower(path.Base(c.Path))] = c
		}
	}
	if len(cands) == 0 {
		return nil
	}

	type item struct {
		fsys fs.FS
		path string // where to read it
		dir  string // the folder it's in, or will be copied to
	}
	present := map[string]bool{}
	var queue []item
	for _, p := range append(append([]string(nil), rep.Extracted.Written...), rep.Extracted.Unchanged...) {
		present[strings.ToLower(p)] = true
		switch strings.ToLower(path.Ext(p)) {
		case ".exe", ".dll", ".ocx":
			queue = append(queue, item{progs, p, path.Dir(p)})
		}
	}
	type copyJob struct {
		from Change
		to   string
	}
	var jobs []copyJob
	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		for _, lib := range importsOf(it.fsys, it.path) {
			c, ok := cands[strings.ToLower(lib)]
			if !ok {
				continue
			}
			to := path.Join(it.dir, path.Base(c.Path))
			if present[strings.ToLower(to)] {
				continue
			}
			present[strings.ToLower(to)] = true
			jobs = append(jobs, copyJob{c, to})
			queue = append(queue, item{cfs, c.Path, it.dir})
		}
	}

	for _, j := range jobs {
		target := filepath.Join(opts.Source, filepath.FromSlash(j.to))
		same, exists, err := sameContent(cfs, j.from.Path, target)
		if err != nil {
			return err
		}
		if exists && !same && !opts.Force {
			return fmt.Errorf("would overwrite %s, which differs (use --force)", target)
		}
		if !same && !opts.DryRun {
			if err := copyOut(cfs, j.from, target); err != nil {
				return err
			}
		}
		rep.DLLs = append(rep.DLLs, DLLCopy{From: j.from.Path, To: j.to})
	}
	return nil
}

// DLLCopy is a DLL copyImportedDLLs copied, as drive paths: from C:,
// to the extracted folder.
type DLLCopy struct{ From, To string }

// importsOf lists the DLLs a program imports, or nothing if it isn't one
// (or can't be read).
var importsOf = func(fsys fs.FS, p string) []string {
	f, err := fsys.Open(p)
	if err != nil {
		return nil
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 256<<20))
	if err != nil {
		return nil
	}
	return peImports(data)
}

// peImports reads the DLL names in a PE file's import and delay-import
// directories (PE/COFF: IMAGE_IMPORT_DESCRIPTOR, IMAGE_DELAYLOAD_DESCRIPTOR).
// debug/pe's ImportedLibraries returns nothing for PE, and its
// ImportedSymbols leaves out DLLs whose functions are all imported by
// ordinal (MFC42.DLL, DSOUND.DLL), so the descriptors are read directly.
func peImports(data []byte) []string {
	pf, err := pe.NewFile(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	defer pf.Close()
	var dirs []pe.DataDirectory
	var imageBase uint64
	switch oh := pf.OptionalHeader.(type) {
	case *pe.OptionalHeader32:
		dirs, imageBase = oh.DataDirectory[:min(oh.NumberOfRvaAndSizes, 16)], uint64(oh.ImageBase)
	case *pe.OptionalHeader64:
		dirs, imageBase = oh.DataDirectory[:min(oh.NumberOfRvaAndSizes, 16)], oh.ImageBase
	default:
		return nil
	}
	offset := func(rva uint32) (int, bool) {
		for _, s := range pf.Sections {
			size := max(s.VirtualSize, s.Size)
			if rva >= s.VirtualAddress && rva < s.VirtualAddress+size {
				off := int(rva - s.VirtualAddress + s.Offset)
				return off, off < len(data)
			}
		}
		return 0, false
	}
	u32 := func(off int) uint32 {
		if off < 0 || off+4 > len(data) {
			return 0
		}
		return binary.LittleEndian.Uint32(data[off:])
	}
	cstr := func(rva uint32) string {
		off, ok := offset(rva)
		if !ok {
			return ""
		}
		end := bytes.IndexByte(data[off:], 0)
		if end < 0 || end > 260 {
			return ""
		}
		return string(data[off : off+end])
	}
	var libs []string
	seen := map[string]bool{}
	add := func(name string) {
		if name != "" && !seen[strings.ToLower(name)] {
			seen[strings.ToLower(name)] = true
			libs = append(libs, name)
		}
	}
	walk := func(dir, size int, nameAt func(off int) uint32) {
		if dir >= len(dirs) || dirs[dir].VirtualAddress == 0 {
			return
		}
		start, ok := offset(dirs[dir].VirtualAddress)
		if !ok {
			return
		}
		for off, n := start, 0; off+size <= len(data) && n < 4096; off, n = off+size, n+1 {
			if bytes.Count(data[off:off+size], []byte{0}) == size {
				break // the all-zero terminator
			}
			add(cstr(nameAt(off)))
		}
	}
	walk(1, 20, func(off int) uint32 { return u32(off + 12) }) // Name
	walk(13, 32, func(off int) uint32 {                        // DllNameRVA, a VA in old-style descriptors
		name := u32(off + 4)
		if u32(off)&1 == 0 && uint64(name) > imageBase {
			name -= uint32(imageBase)
		}
		return name
	})
	return libs
}
