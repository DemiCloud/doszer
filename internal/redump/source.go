package redump

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// The datfile snapshot `make dat` stages in data/ (gitignored; the
// MANIFEST placeholder keeps the directory for go:embed).
//
//go:embed data
var embedded embed.FS

const embeddedName = "data/pc.dat.gz"

// refreshAfter is how long after the last check Select tries to fetch a
// newer datfile: Redump adds discs continually, and a disc dumped today
// may be verified by an entry added this week.
const refreshAfter = 7 * 24 * time.Hour

// Embedded loads the snapshot built into doszer, or returns nil if this
// build has none.
func Embedded() (*DB, error) {
	data, err := fs.ReadFile(embedded, embeddedName)
	if err != nil {
		return nil, nil
	}
	return Parse(data)
}

// CachePath is where a datfile fetched at run time is kept.
func CachePath() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cache, "doszer", "redump-pc.dat.gz"), nil
}

// Cached loads the cached datfile, or returns nil if there is none.
func Cached() (*DB, time.Time, error) {
	p, err := CachePath()
	if err != nil {
		return nil, time.Time{}, err
	}
	fi, err := os.Stat(p)
	if err != nil {
		return nil, time.Time{}, nil
	}
	db, err := Load(p)
	return db, fi.ModTime(), err
}

// Update fetches Redump's current datfile into the cache.
func Update() (*DB, error) {
	db, err := Download()
	if err != nil {
		return nil, err
	}
	p, err := CachePath()
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := db.Write(&buf); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return nil, err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return nil, err
	}
	return db, os.Rename(tmp, p)
}

// touchCache records a check that found nothing newer.
func touchCache() {
	if p, err := CachePath(); err == nil {
		now := time.Now()
		os.Chtimes(p, now, now)
	}
}

// Selected is the datfile Select chose and where it came from.
type Selected struct {
	DB     *DB
	Source string // "external", "cached" or "embedded"
	Path   string // the external datfile
	// Note says why a newer datfile couldn't be had, when that matters.
	Note string
}

// Select chooses the datfile to verify against: external, when the user
// names one; else the newer of the cached and embedded ones, first
// fetching Redump's current one when the last fetch (or the build, with
// no cache) was more than refreshAfter ago and the network allows.
func Select(external string) (*Selected, error) {
	if external != "" {
		db, err := Load(external)
		if err != nil {
			return nil, err
		}
		return &Selected{DB: db, Source: "external", Path: external}, nil
	}
	emb, err := Embedded()
	if err != nil {
		return nil, fmt.Errorf("embedded datfile: %w", err)
	}
	cached, checked, err := Cached()
	if err != nil {
		cached = nil // unreadable: fetch afresh
	}
	sel := &Selected{}
	if cached == nil || time.Since(checked) > refreshAfter {
		if db, err := Update(); err == nil {
			cached = db
		} else if cached != nil || emb != nil {
			touchCache()
			sel.Note = "couldn't fetch Redump's current datfile (" + err.Error() + "); using an older one"
		} else {
			return nil, fmt.Errorf("no Redump datfile: none built in, and fetching one failed: %w", err)
		}
	}
	switch {
	case cached != nil && (emb == nil || cached.Version >= emb.Version):
		sel.DB, sel.Source = cached, "cached"
	default:
		sel.DB, sel.Source = emb, "embedded"
	}
	return sel, nil
}

// Current is the datfile Select would use, without fetching: for status.
func Current(external string) (*Selected, error) {
	if external != "" {
		db, err := Load(external)
		if err != nil {
			return nil, err
		}
		return &Selected{DB: db, Source: "external", Path: external}, nil
	}
	emb, err := Embedded()
	if err != nil {
		return nil, err
	}
	cached, _, _ := Cached()
	switch {
	case cached != nil && (emb == nil || cached.Version >= emb.Version):
		return &Selected{DB: cached, Source: "cached"}, nil
	case emb != nil:
		return &Selected{DB: emb, Source: "embedded"}, nil
	}
	return nil, nil
}
