// Package redump verifies disc dumps against Redump's database
// (redump.org), whose entries are discs dumped and cross-checked by
// several people on different drives: a dump whose every track matches
// one is known to be exact. The database is Redump's public "IBM - PC
// compatible" datfile: a snapshot embedded at build time (`make dat`), a
// newer copy fetched at run time and cached when the network allows, or a
// datfile the user names (Open).
package redump

import (
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha1"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// DatURL is Redump's IBM PC compatible datfile (a zip holding one Logiqx
// XML datfile).
const DatURL = "http://redump.org/datfile/pc/"

// UserAgent identifies doszer's requests to redump.org.
var UserAgent = "doszer"

// Rom is one file of a disc entry.
type Rom struct {
	Name string
	Size int64
	SHA1 string
}

// Disc is one datfile entry: a disc and its track files (the CUE sheet,
// whose file names differ from dump to dump, left out).
type Disc struct {
	Name   string
	Tracks []Rom
}

// DB is a loaded datfile.
type DB struct {
	Version string // the datfile's date, e.g. 2026-06-15 06-43-57
	Discs   []Disc
	bySHA1  map[string][]int
}

// Download fetches Redump's current datfile and returns it loaded.
func Download() (*DB, error) {
	req, err := http.NewRequest("GET", DatURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent)
	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", DatURL, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 256<<20))
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Parse reads a datfile: Logiqx XML, a zip holding one (as Redump
// serves it), or the compact form Write produces.
func Parse(data []byte) (*DB, error) {
	switch {
	case bytes.HasPrefix(data, []byte("PK\x03\x04")):
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if !strings.HasSuffix(strings.ToLower(f.Name), ".dat") {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return parseXML(rc)
		}
		return nil, errors.New("no datfile in the zip")
	case bytes.HasPrefix(data, []byte{0x1f, 0x8b}):
		return parseCompact(bytes.NewReader(data))
	}
	return parseXML(bytes.NewReader(data))
}

// Load reads a datfile (Parse) from path.
func Load(path string) (*DB, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	db, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return db, nil
}

func parseXML(r io.Reader) (*DB, error) {
	db := &DB{bySHA1: map[string][]int{}}
	dec := xml.NewDecoder(r)
	var cur *Disc
	var inDate bool
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "date":
				inDate = cur == nil
			case "game":
				db.Discs = append(db.Discs, Disc{Name: attr(t, "name")})
				cur = &db.Discs[len(db.Discs)-1]
			case "rom":
				if cur == nil || strings.HasSuffix(strings.ToLower(attr(t, "name")), ".cue") {
					continue
				}
				var size int64
				fmt.Sscan(attr(t, "size"), &size)
				cur.Tracks = append(cur.Tracks, Rom{Name: attr(t, "name"), Size: size, SHA1: strings.ToLower(attr(t, "sha1"))})
			}
		case xml.CharData:
			if inDate {
				db.Version = strings.TrimSpace(string(t))
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "date":
				inDate = false
			case "game":
				cur = nil
			}
		}
	}
	return db.index()
}

// index builds the SHA-1 index.
func (db *DB) index() (*DB, error) {
	if len(db.Discs) == 0 {
		return nil, errors.New("no discs")
	}
	db.bySHA1 = map[string][]int{}
	for i, d := range db.Discs {
		for _, r := range d.Tracks {
			db.bySHA1[r.SHA1] = append(db.bySHA1[r.SHA1], i)
		}
	}
	return db, nil
}

// Write writes db in a compact form, about a third of Redump's zip: a
// gzipped list of "V<tab>version", then per disc "D<tab>name" and per
// track "<size><tab><sha1>". Track file names aren't kept: a match is by
// size and SHA-1.
func (db *DB) Write(w io.Writer) error {
	zw, err := gzip.NewWriterLevel(w, gzip.BestCompression)
	if err != nil {
		return err
	}
	bw := bufio.NewWriter(zw)
	fmt.Fprintf(bw, "V\t%s\n", db.Version)
	for _, d := range db.Discs {
		fmt.Fprintf(bw, "D\t%s\n", d.Name)
		for _, r := range d.Tracks {
			fmt.Fprintf(bw, "%d\t%s\n", r.Size, r.SHA1)
		}
	}
	if err := bw.Flush(); err != nil {
		return err
	}
	return zw.Close()
}

func parseCompact(r io.Reader) (*DB, error) {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return nil, err
	}
	db := &DB{}
	sc := bufio.NewScanner(zr)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		k, v, _ := strings.Cut(sc.Text(), "\t")
		switch {
		case k == "V":
			db.Version = v
		case k == "D":
			db.Discs = append(db.Discs, Disc{Name: v})
		case len(db.Discs) > 0:
			size, err := strconv.ParseInt(k, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("compact datfile: %q", sc.Text())
			}
			d := &db.Discs[len(db.Discs)-1]
			d.Tracks = append(d.Tracks, Rom{Name: fmt.Sprintf("Track %d", len(d.Tracks)+1), Size: size, SHA1: v})
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return db.index()
}

func attr(e xml.StartElement, name string) string {
	for _, a := range e.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

// Track is one track file of a dump.
type Track struct {
	Path string
	Size int64
	SHA1 string
	// Mode2SHA1 is a data track rebuilt from 2048-byte sectors as Mode 2
	// Form 1 instead of Mode 1 (SHA1): the sectors don't say which the
	// disc used. Empty for a track read as it is.
	Mode2SHA1 string
}

// Matches reports whether the track's SHA-1, or its Mode 2 rebuild's,
// is sha1.
func (t Track) Matches(sha1 string) bool {
	return t.SHA1 == sha1 || (t.Mode2SHA1 != "" && t.Mode2SHA1 == sha1)
}

// HashTracks hashes a dump's track files.
func HashTracks(paths []string) ([]Track, error) {
	var out []Track
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			return nil, err
		}
		h := sha1.New()
		n, err := io.Copy(h, f)
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		out = append(out, Track{Path: p, Size: n, SHA1: hex.EncodeToString(h.Sum(nil))})
	}
	return out, nil
}

// Match is how a dump compares with the database.
type Match struct {
	Verified bool   // every track matches Disc's, and Disc has no others
	Disc     *Disc  // the matching entry, or the closest one (most tracks matching)
	Matching int    // tracks matching Disc's
	Differ   []int  // indexes of the dump's tracks that match none of Disc's
	Link     string // the disc's Redump search page
}

// ErrNoMatch is returned by Match when no track matches any disc: a bad
// dump, or a disc not in Redump yet.
var ErrNoMatch = errors.New("no track matches a Redump disc")

// Match compares a dump's tracks with the database: by SHA-1 and size,
// track for track, regardless of file names.
func (db *DB) Match(tracks []Track) (*Match, error) {
	counts := map[int]int{}
	for _, t := range tracks {
		seen := map[int]bool{}
		for _, d := range append(append([]int{}, db.bySHA1[t.SHA1]...), db.bySHA1[t.Mode2SHA1]...) {
			if !seen[d] && hasRom(db.Discs[d], t) {
				seen[d] = true
				counts[d]++
			}
		}
	}
	best, bestCount := -1, 0
	for d, c := range counts {
		exact := c == len(tracks) && len(db.Discs[d].Tracks) == len(tracks)
		if best < 0 || c > bestCount || (c == bestCount && exact) || (c == bestCount && d < best) {
			best, bestCount = d, c
		}
	}
	if best < 0 {
		return nil, ErrNoMatch
	}
	disc := &db.Discs[best]
	m := &Match{Disc: disc, Matching: bestCount, Link: "http://redump.org/discs/quicksearch/" + disc.Tracks[0].SHA1 + "/"}
	for i, t := range tracks {
		if !hasRom(*disc, t) {
			m.Differ = append(m.Differ, i)
		}
	}
	m.Verified = len(m.Differ) == 0 && len(disc.Tracks) == len(tracks)
	return m, nil
}

func hasRom(d Disc, t Track) bool {
	for _, r := range d.Tracks {
		if t.Matches(r.SHA1) && r.Size == t.Size {
			return true
		}
	}
	return false
}

// Hit is a track of a disc, found by its SHA-1.
type Hit struct {
	Disc  string
	Track int // 1-based
	Size  int64
}

// Lookup finds every disc with a track whose SHA-1 is sha1.
func (db *DB) Lookup(sha1 string) []Hit {
	sha1 = strings.ToLower(sha1)
	var hits []Hit
	seen := map[int]bool{}
	for _, d := range db.bySHA1[sha1] {
		if seen[d] {
			continue
		}
		seen[d] = true
		for i, r := range db.Discs[d].Tracks {
			if r.SHA1 == sha1 {
				hits = append(hits, Hit{Disc: db.Discs[d].Name, Track: i + 1, Size: r.Size})
			}
		}
	}
	return hits
}
