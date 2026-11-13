package zipw

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestWriterDeterministicHeaders(t *testing.T) {
	var buf bytes.Buffer
	w := New(&buf)

	custom := time.Date(1994, 3, 15, 12, 30, 0, 0, time.UTC)
	if err := w.Add("A.TXT", strings.NewReader("hello"), false, custom); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := w.Add("B.OGG", strings.NewReader("ogg data"), true, time.Time{}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	r, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}

	if len(r.File) != 2 {
		t.Fatalf("got %d entries, want 2", len(r.File))
	}

	byName := map[string]*zip.File{}
	for _, f := range r.File {
		byName[f.Name] = f
		if f.ExternalAttrs != 0 {
			t.Errorf("%s: ExternalAttrs = %d, want 0", f.Name, f.ExternalAttrs)
		}
		if len(f.Extra) != 0 {
			t.Errorf("%s: Extra = %v, want none", f.Name, f.Extra)
		}
	}

	// A custom modification time round-trips through DOS encoding exactly
	// (2-second resolution).
	if got := byName["A.TXT"].Modified.UTC().Format("2006-01-02T15:04:05"); got != "1994-03-15T12:30:00" {
		t.Errorf("A.TXT: Modified = %s, want 1994-03-15T12:30:00", got)
	}
	// The zero time.Time clamps to the DOS epoch.
	if got := byName["B.OGG"].Modified.UTC().Format("2006-01-02T15:04:05"); got != "1980-01-01T00:00:00" {
		t.Errorf("B.OGG: Modified = %s, want 1980-01-01T00:00:00 (clamped)", got)
	}

	if byName["A.TXT"].Method != zip.Deflate {
		t.Error("A.TXT should be Deflate")
	}
	if byName["B.OGG"].Method != zip.Store {
		t.Error("B.OGG should be Store")
	}
}

func TestToDOSTimeClampsOutOfRangeYears(t *testing.T) {
	tooEarly := time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)
	date, timeField := toDOSTime(tooEarly)
	wantDate, wantTime := toDOSTime(dosEpoch)
	if date != wantDate || timeField != wantTime {
		t.Errorf("toDOSTime(1970) = (%d,%d), want the epoch's (%d,%d)", date, timeField, wantDate, wantTime)
	}

	tooLate := time.Date(3000, 1, 1, 0, 0, 0, 0, time.UTC)
	date, _ = toDOSTime(tooLate)
	if year := 1980 + int(date>>9); year != dosMaxYear {
		t.Errorf("toDOSTime(3000) year = %d, want %d", year, dosMaxYear)
	}
}
