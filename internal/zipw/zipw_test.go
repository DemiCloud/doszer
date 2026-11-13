package zipw

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

func TestWriterDeterministicHeaders(t *testing.T) {
	var buf bytes.Buffer
	w := New(&buf)

	if err := w.Add("A.TXT", strings.NewReader("hello"), false); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := w.Add("B.OGG", strings.NewReader("ogg data"), true); err != nil {
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

	for _, f := range r.File {
		if got := f.Modified.UTC().Format("2006-01-02T15:04:05"); got != "1980-01-01T00:00:00" {
			t.Errorf("%s: Modified = %s, want 1980-01-01T00:00:00", f.Name, got)
		}
		if f.ExternalAttrs != 0 {
			t.Errorf("%s: ExternalAttrs = %d, want 0", f.Name, f.ExternalAttrs)
		}
		if len(f.Extra) != 0 {
			t.Errorf("%s: Extra = %v, want none", f.Name, f.Extra)
		}
	}

	byName := map[string]*zip.File{}
	for _, f := range r.File {
		byName[f.Name] = f
	}
	if byName["A.TXT"].Method != zip.Deflate {
		t.Error("A.TXT should be Deflate")
	}
	if byName["B.OGG"].Method != zip.Store {
		t.Error("B.OGG should be Store")
	}
}
