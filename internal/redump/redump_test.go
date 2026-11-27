package redump

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

const testDat = `<?xml version="1.0"?>
<datafile>
	<header><name>IBM - PC compatible</name><date>2026-06-15 06-43-57</date></header>
	<game name="Example Game (USA)">
		<rom name="Example Game (USA).cue" size="200" crc="0" md5="0" sha1="cccccccccccccccccccccccccccccccccccccccc"/>
		<rom name="Example Game (USA) (Track 1).bin" size="4" sha1="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"/>
		<rom name="Example Game (USA) (Track 2).bin" size="5" sha1="BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"/>
	</game>
	<game name="Example Game (Europe)">
		<rom name="Example Game (Europe) (Track 1).bin" size="4" sha1="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"/>
		<rom name="Example Game (Europe) (Track 2).bin" size="5" sha1="dddddddddddddddddddddddddddddddddddddddd"/>
		<rom name="Example Game (Europe) (Track 3).bin" size="5" sha1="eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"/>
	</game>
</datafile>`

func load(t *testing.T) *DB {
	t.Helper()
	p := filepath.Join(t.TempDir(), "pc.dat")
	os.WriteFile(p, []byte(testDat), 0o644)
	db, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestLoad(t *testing.T) {
	db := load(t)
	if db.Version != "2026-06-15 06-43-57" || db.Name != "IBM - PC compatible" || len(db.Discs) != 2 || len(db.Discs[0].Tracks) != 2 {
		t.Fatalf("db %+v", db)
	}
	if db.Discs[0].Tracks[1].SHA1 != "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" {
		t.Fatal("SHA-1 not normalized to lower case")
	}
}

func TestMatch(t *testing.T) {
	db := load(t)
	a := Track{Size: 4, SHA1: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	b := Track{Size: 5, SHA1: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
	bad := Track{Size: 5, SHA1: "ffffffffffffffffffffffffffffffffffffffff"}

	m, err := db.Match([]Track{a, b})
	if err != nil || !m.Verified || m.Disc.Name != "Example Game (USA)" {
		t.Fatalf("exact: %+v, %v", m, err)
	}
	if m.Link != "http://redump.org/discs/quicksearch/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/" {
		t.Fatalf("link %s", m.Link)
	}

	// A track that differs: the closest disc, and which track.
	m, err = db.Match([]Track{a, bad})
	if err != nil || m.Verified || m.Matching != 1 || len(m.Differ) != 1 || m.Differ[0] != 1 {
		t.Fatalf("one track differs: %+v, %v", m, err)
	}

	// The right size is part of a match.
	if m, err = db.Match([]Track{a, {Size: 6, SHA1: b.SHA1}}); err != nil || m.Verified {
		t.Fatalf("size mismatch verified: %+v, %v", m, err)
	}

	// Every track matching isn't enough if the disc has more.
	if m, err = db.Match([]Track{a, {Size: 5, SHA1: "dddddddddddddddddddddddddddddddddddddddd"}}); err != nil || m.Verified {
		t.Fatalf("missing track verified: %+v, %v", m, err)
	}

	if _, err := db.Match([]Track{bad}); err != ErrNoMatch {
		t.Fatalf("no match: %v", err)
	}
}

func TestHashTracks(t *testing.T) {
	p := filepath.Join(t.TempDir(), "t.bin")
	os.WriteFile(p, []byte("abc"), 0o644)
	ts, err := HashTracks([]string{p})
	if err != nil || ts[0].Size != 3 || ts[0].SHA1 != "a9993e364706816aba3e25717850c26c9cd0d89d" {
		t.Fatalf("%+v, %v", ts, err)
	}
}

// The compact form keeps everything a match needs.
func TestWriteParse(t *testing.T) {
	db := load(t)
	var buf bytes.Buffer
	if err := db.Write(&buf); err != nil {
		t.Fatal(err)
	}
	got, err := Parse(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != db.Version || got.Name != db.Name || len(got.Discs) != len(db.Discs) {
		t.Fatalf("round trip %+v", got)
	}
	for i, d := range db.Discs {
		for j, r := range d.Tracks {
			g := got.Discs[i].Tracks[j]
			if got.Discs[i].Name != d.Name || g.Size != r.Size || g.SHA1 != r.SHA1 {
				t.Fatalf("disc %d track %d: %+v, want %+v", i, j, g, r)
			}
		}
	}
	m, err := got.Match([]Track{{Size: 4, SHA1: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, {Size: 5, SHA1: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}})
	if err != nil || !m.Verified {
		t.Fatalf("%+v, %v", m, err)
	}
}

func TestSelectExternal(t *testing.T) {
	p := filepath.Join(t.TempDir(), "my.dat")
	os.WriteFile(p, []byte(testDat), 0o644)
	sel, err := Select(p)
	if err != nil || sel.Source != "external" || len(sel.DB.Discs) != 2 {
		t.Fatalf("%+v, %v", sel, err)
	}
}
