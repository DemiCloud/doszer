package output

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	for _, tt := range []struct {
		in      string
		want    Format
		wantErr bool
	}{
		{"", Auto, false},
		{"json", JSON, false},
		{"plain", Plain, false},
		{"JSON", "", true},
		{"xml", "", true},
	} {
		got, err := Parse(tt.in)
		if got != tt.want || (err != nil) != tt.wantErr {
			t.Errorf("Parse(%q) = %q, %v", tt.in, got, err)
		}
	}
}

func TestResolve(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if got := Auto.Resolve(f); got != Plain {
		t.Errorf("Auto on a file = %q, want plain", got)
	}
	if got := Fancy.Resolve(f); got != Fancy {
		t.Errorf("Fancy on a file = %q, want fancy", got)
	}
}

type record struct {
	Name  string   `json:"name" yaml:"name" toml:"name"`
	Size  int64    `json:"size" yaml:"size" toml:"size"`
	Notes string   `json:"notes,omitempty" yaml:"notes,omitempty" toml:"notes,omitempty"`
	Tags  []string `json:"tags,omitempty" yaml:"tags,omitempty" toml:"tags,omitempty"`
}

type doc struct {
	Archives []record `json:"archives" yaml:"archives" toml:"archives"`
}

func TestEncode(t *testing.T) {
	v := doc{[]record{{Name: "Example Game.dosz", Size: 42, Tags: []string{"a"}}}}
	for _, tt := range []struct {
		f    Format
		want string
	}{
		{JSON, "{\n  \"archives\": [\n    {\n      \"name\": \"Example Game.dosz\",\n      \"size\": 42,\n      \"tags\": [\n        \"a\"\n      ]\n    }\n  ]\n}\n"},
		{YAML, "archives:\n  - name: Example Game.dosz\n    size: 42\n    tags:\n      - a\n"},
		{TOML, "[[archives]]\nname = 'Example Game.dosz'\nsize = 42\ntags = ['a']\n"},
	} {
		var b bytes.Buffer
		if err := Encode(&b, tt.f, v); err != nil {
			t.Fatalf("%s: %v", tt.f, err)
		}
		if b.String() != tt.want {
			t.Errorf("%s:\n%s\nwant:\n%s", tt.f, b.String(), tt.want)
		}
	}
	if err := Encode(&bytes.Buffer{}, Plain, v); err == nil {
		t.Error("Encode(plain) succeeded")
	}
}

func TestEncodeCSV(t *testing.T) {
	var b bytes.Buffer
	if err := EncodeCSV(&b, []string{"file", "ok"}, [][]string{{"A, B.dosz", "true"}}); err != nil {
		t.Fatal(err)
	}
	if want := "file,ok\n\"A, B.dosz\",true\n"; b.String() != want {
		t.Errorf("got %q", b.String())
	}
	if !strings.HasPrefix(b.String(), "file,") {
		t.Error("no header")
	}
}
