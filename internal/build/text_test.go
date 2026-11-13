package build

import (
	"bytes"
	"testing"
)

func TestIsTextFile(t *testing.T) {
	tests := []struct {
		path    string
		content []byte
		want    bool
	}{
		{"AUTOEXEC.BAT", []byte("echo hi"), true},
		{"config.ini", []byte("x=1"), true},
		{"SETUP.CONF", []byte("x=1"), true},
		{"CONFIG.SYS", []byte("DEVICE=HIMEM.SYS"), true},
		{"config.sys", []byte("DEVICE=HIMEM.SYS"), true},
		{"GAME.EXE", []byte("MZ..."), false},
		{"DATA/AUTOEXEC.BAT", []byte("echo hi"), true},
		{"AUTOEXEC.BAT", []byte("echo\x00hi"), false}, // NUL byte disqualifies
	}

	for _, tt := range tests {
		if got := isTextFile(tt.path, tt.content); got != tt.want {
			t.Errorf("isTextFile(%q, %q) = %v, want %v", tt.path, tt.content, got, tt.want)
		}
	}
}

func TestNormalizeCRLF(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"LF only", "a\nb\n", "a\r\nb\r\n"},
		{"CRLF already", "a\r\nb\r\n", "a\r\nb\r\n"},
		{"missing trailing newline", "a\nb", "a\r\nb\r\n"},
		{"mixed", "a\r\nb\nc", "a\r\nb\r\nc\r\n"},
		{"empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeCRLF([]byte(tt.in)); !bytes.Equal(got, []byte(tt.want)) {
				t.Errorf("normalizeCRLF(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestNormalizeTextOnlyTouchesTextFiles(t *testing.T) {
	fs := newFileSet()
	fs.set("AUTOEXEC.BAT", dataEntry([]byte("echo hi")))
	fs.set("GAME.EXE", dataEntry([]byte("binary\nwith\nlf")))

	if err := normalizeText(fs); err != nil {
		t.Fatalf("normalizeText: %v", err)
	}

	bat, _ := fs.get("AUTOEXEC.BAT")
	if got, _ := bat.bytes(); string(got) != "echo hi\r\n" {
		t.Errorf("AUTOEXEC.BAT = %q, want %q", got, "echo hi\r\n")
	}

	exe, _ := fs.get("GAME.EXE")
	if got, _ := exe.bytes(); string(got) != "binary\nwith\nlf" {
		t.Errorf("GAME.EXE was modified: %q", got)
	}
}
