package build

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/DemiCloud/doszer/internal/cue"
)

// dateMedia gives every file of each disc image in fs (§6.4: a CUE sheet
// and the files it references, all present) the disc's date (§8.2,
// discDate), so two exact copies of a disc build identical archives
// however and whenever each was dumped.
func dateMedia(fs *fileSet) {
	for p, d := range mediaDates(fs) {
		e, _ := fs.get(p)
		e.modified = d
	}
}

// MediaDates applies §8.2's disc image rule to an archive's entries: the
// modification time every disc image file must have, by entry name.
func MediaDates(files []*zip.File) map[string]time.Time {
	return mediaDates(zipFileSet(files, nil))
}

func mediaDates(fs *fileSet) map[string]time.Time {
	out := map[string]time.Time{}
	for _, cuePath := range fs.paths() {
		if !strings.EqualFold(path.Ext(cuePath), ".cue") {
			continue
		}
		e, _ := fs.get(cuePath)
		data, err := e.peek()
		if err != nil {
			continue
		}
		sheet, err := cue.Parse(data)
		if err != nil || len(sheet.Files) == 0 {
			continue
		}
		dir := path.Dir(cuePath)
		resolved, err := cue.ResolveWith(sheet, func(name string) (string, int64, error) {
			p := path.Join(dir, strings.ReplaceAll(name, `\`, "/"))
			e, ok := fs.get(p)
			if !ok {
				return "", 0, os.ErrNotExist
			}
			size, err := e.size()
			return p, size, err
		})
		if err != nil {
			continue // not a disc image: some file is missing
		}
		d := discDate(fs, resolved)
		out[cuePath] = d
		for _, rt := range resolved {
			out[rt.FilePath] = d
		}
	}
	return out
}

// discDate is the volume creation date in the primary volume descriptor
// of a disc's first data track (ECMA-119 §8.4.26), else Epoch.
func discDate(fs *fileSet, tracks []cue.ResolvedTrack) time.Time {
	for _, rt := range tracks {
		if rt.Track.IsAudio() {
			continue
		}
		if !rt.Verifiable() {
			return Epoch
		}
		off, length, err := rt.Track.DataLayout()
		if err != nil || length != 2048 {
			return Epoch
		}
		at := rt.Start + 16*int64(rt.FrameSize) + int64(off)
		if at+2048 > rt.End {
			return Epoch
		}
		e, _ := fs.get(rt.FilePath)
		rc, err := e.openRange(at, 2048)
		if err != nil {
			return Epoch
		}
		pvd := make([]byte, 2048)
		_, err = io.ReadFull(rc, pvd)
		rc.Close()
		if err != nil {
			return Epoch
		}
		return pvdDate(pvd)
	}
	return Epoch
}

// pvdDate parses a primary volume descriptor's volume creation date:
// "YYYYMMDDHHMMSScc" digits and a signed offset from UTC in 15-minute
// units. All-zero digits mean not specified (ECMA-119 §8.4.26.1).
func pvdDate(pvd []byte) time.Time {
	if len(pvd) < 830 || pvd[0] != 1 || !bytes.Equal(pvd[1:6], []byte("CD001")) {
		return Epoch
	}
	f := pvd[813:830]
	n := func(i, w int) int {
		v, err := strconv.Atoi(string(f[i : i+w]))
		if err != nil {
			return -1
		}
		return v
	}
	y, mo, d, h, mi, s, cs := n(0, 4), n(4, 2), n(6, 2), n(8, 2), n(10, 2), n(12, 2), n(14, 2)
	if y < 1 || mo < 1 || mo > 12 || d < 1 || d > 31 || h < 0 || h > 23 || mi < 0 || mi > 59 || s < 0 || s > 59 || cs < 0 {
		return Epoch
	}
	t := time.Date(y, time.Month(mo), d, h, mi, s, cs*int(time.Second/100), time.UTC)
	return t.Add(-time.Duration(int8(f[16])) * 15 * time.Minute)
}
