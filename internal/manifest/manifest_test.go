package manifest

import (
	"strings"
	"testing"
)

const validFull = `
format = 1
name   = "Example Game"
notes  = "Test manifest."
x-tool = "ignored"

[boot]
os     = "dosbox_pure/Win98"
parent = "Shared.shell"
disc   = "Example Game (Disc 1)"
skip   = 120

[layout]
trim    = ["**/*.HLP", "**/UNINST*.*"]
flatten = true

[[layout.rewrite]]
from  = 'C:\STUDIO\GAME'
to    = 'D:\GAME'
files = ["**/*.INI"]

[pack]
store = ["iso", "bin"]
audio = "wav"

[[source.media]]
id     = "disc1"
label  = "Example Game (Disc 1).cue"
redump = "http://redump.org/disc/0/"
x-note = "ignored"
tracks = [
  { type = "data",  sha1 = "0123456789abcdef0123456789abcdef01234567" },
  { type = "audio", sha1 = "89abcdef0123456789abcdef0123456789abcdef" },
]

[[extract]]
media = "disc1"
from  = "/RESOURCE"
to    = "GAME/RESOURCE"

[[file]]
path = "WINDOWS.BAT"
text = "@ECHO OFF\nD:\\GAME\\GAME.EXE\n"
`

func TestParseValid(t *testing.T) {
	m, err := Parse([]byte(validFull), "source-dir")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if m.Name != "Example Game" {
		t.Errorf("Name = %q, want %q", m.Name, "Example Game")
	}
	if m.Model() != ModelOS {
		t.Errorf("Model = %v, want %v", m.Model(), ModelOS)
	}
	if m.AudioEncoding() != AudioWAV {
		t.Errorf("AudioEncoding = %q, want %q", m.AudioEncoding(), AudioWAV)
	}
	if m.Boot.Skip == nil || *m.Boot.Skip != 120 {
		t.Errorf("Boot.Skip = %v, want 120", m.Boot.Skip)
	}
	if got := len(m.Source.Media[0].Tracks); got != 2 {
		t.Errorf("tracks = %d, want 2", got)
	}
}

func TestParseDefaults(t *testing.T) {
	m, err := Parse([]byte("format = 1\n"), "My Game")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if m.Name != "My Game" {
		t.Errorf("Name = %q, want default %q", m.Name, "My Game")
	}
	if m.Model() != ModelPlain {
		t.Errorf("Model = %v, want %v", m.Model(), ModelPlain)
	}
	if m.AudioEncoding() != AudioOgg {
		t.Errorf("AudioEncoding = %q, want %q", m.AudioEncoding(), AudioOgg)
	}
}

func TestParseDiscAlone(t *testing.T) {
	if _, err := Parse([]byte("format = 1\n[boot]\ndisc = \"Disc 2\"\n"), "g"); err != nil {
		t.Fatalf("disc without a startup mode should be valid: %v", err)
	}
}

func TestParseInvalid(t *testing.T) {
	tests := []struct {
		name    string
		toml    string
		wantErr string
	}{
		{"wrong format", "format = 2", "format: must be 1"},
		{"missing format", `name = "x"`, "format: must be 1"},
		{"unknown top-level key", "format = 1\nnmae = \"x\"", `unknown key "nmae"`},
		{"unknown nested key", "format = 1\n[boot]\nparnet = \"a.shell\"", `unknown key "boot.parnet"`},
		{"unknown key in array table", "format = 1\n[[extract]]\nmedia = \"a\"\nfrom = \"/\"\nto = \"x\"\nsrc = \"y\"", `unknown key "extract[0].src"`},
		{"two startup modes", "format = 1\n[boot]\nos = \"a/Win98\"\nshell = \"a/Win31\"", "at most one of exec, shell and os"},
		{"os with extension", "format = 1\n[boot]\nos = \"dosbox_pure/Win98.vhd\"", "must not include the file extension"},
		{"shell with backslash", "format = 1\n[boot]\nshell = 'dosbox_pure\\Win31'", "must use / as separator"},
		{"absolute os", "format = 1\n[boot]\nos = \"/abs/Win98\"", "must be relative to the system directory"},
		{"parent with path", "format = 1\n[boot]\nparent = \"x/Shared.shell\"", "must be a bare file name"},
		{"parent without extension", "format = 1\n[boot]\nparent = \"Shared\"", "must include an extension"},
		{"skip without startup", "format = 1\n[boot]\nskip = 10", "requires one of exec, shell or os"},
		{"negative skip", "format = 1\n[boot]\nexec = 'C:\\G.EXE'\nskip = -1", "must not be negative"},
		{"trim with dotdot", "format = 1\n[layout]\ntrim = [\"../x\"]", "must not contain '..'"},
		{"trim absolute", "format = 1\n[layout]\ntrim = [\"/x\"]", "must be relative"},
		{"rewrite without files", "format = 1\n[[layout.rewrite]]\nfrom = \"a\"\nto = \"b\"", "files: must not be empty"},
		{"store with dot", "format = 1\n[pack]\nstore = [\".iso\"]", "without a leading dot"},
		{"bad audio", "format = 1\n[pack]\naudio = \"flac\"", `pack.audio: must be "ogg" or "wav"`},
		{"duplicate media id", "format = 1\n[[source.media]]\nid = \"a\"\ntracks = [{type=\"data\",sha1=\"0123456789abcdef0123456789abcdef01234567\"}]\n[[source.media]]\nid = \"a\"\ntracks = [{type=\"data\",sha1=\"0123456789abcdef0123456789abcdef01234567\"}]", `duplicate id "a"`},
		{"uppercase sha1", "format = 1\n[[source.media]]\nid = \"a\"\ntracks = [{type=\"data\",sha1=\"0123456789ABCDEF0123456789abcdef01234567\"}]", "40 lowercase hexadecimal"},
		{"bad track type", "format = 1\n[[source.media]]\nid = \"a\"\ntracks = [{type=\"video\",sha1=\"0123456789abcdef0123456789abcdef01234567\"}]", `must be "data" or "audio"`},
		{"extract unknown media", "format = 1\n[[extract]]\nmedia = \"nope\"\nfrom = \"/\"\nto = \"x\"", `unknown media id "nope"`},
		{"file without text", "format = 1\n[[file]]\npath = \"A.BAT\"", "text: must be set"},
		{"invalid toml", "format = = 1", "DOSZ.TOML"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.toml), "g")
			if err == nil {
				t.Fatalf("Parse succeeded, want error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestParseReportsAllErrors(t *testing.T) {
	_, err := Parse([]byte("format = 1\n[boot]\nskip = -1\nparent = \"x/y\""), "g")
	if err == nil {
		t.Fatal("Parse succeeded, want errors")
	}

	for _, want := range []string{"must not be negative", "requires one of", "bare file name"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q is missing %q", err, want)
		}
	}
}
