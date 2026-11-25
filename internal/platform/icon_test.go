package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAutorunIcon(t *testing.T) {
	tests := []struct {
		inf   string
		path  string
		index int
		ok    bool
	}{
		{"[autorun]\r\nopen=autorun.exe\r\nicon=autorun.ico\r\n", "autorun.ico", 0, true},
		{"[AutoRun]\nICON = \"\\Setup\\Game.exe\", 2\n", "Setup/Game.exe", 2, true},
		{"[Sierra]\nicon=x.ico\n", "", 0, false},               // not [autorun]
		{"[autorun]\nicon=game.exe,-101\n", "", 0, false},      // a resource ID
		{"[autorun]\nicon=..\\x.ico\n", "", 0, false},          // outside the disc
		{"[autorun]\nicon=C:\\WINDOWS\\x.ico\n", "", 0, false}, // a drive
		{"[autorun]\nopen=setup.exe\n", "", 0, false},
	}
	for _, tt := range tests {
		p, i, ok := AutorunIcon([]byte(tt.inf))
		if p != tt.path || i != tt.index || ok != tt.ok {
			t.Errorf("%q: got %q %d %v, want %q %d %v", tt.inf, p, i, ok, tt.path, tt.index, tt.ok)
		}
	}
}

func TestSuggestIconFromSourceAutorun(t *testing.T) {
	ico := "\x00\x00\x01\x00\x01\x00\x10\x10\x00\x00\x01\x00\x20\x00\x04\x00\x00\x00\x16\x00\x00\x00DATA"
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "Art"), 0o755)
	os.WriteFile(filepath.Join(dir, "Art", "Game.ico"), []byte(ico), 0o644)
	os.WriteFile(filepath.Join(dir, "AUTORUN.INF"), []byte("[autorun]\r\nicon=ART\\GAME.ICO\r\n"), 0o644)
	c := SuggestIcon(dir, "Game", nil)
	if c == nil || c.Path != "Art/Game.ico" || c.Disc != "" || c.From != "AUTORUN.INF" {
		t.Fatalf("SuggestIcon = %+v", c)
	}

	os.WriteFile(filepath.Join(dir, "Art", "Game.ico"), []byte("not an icon"), 0o644)
	if c := SuggestIcon(dir, "Game", nil); c != nil {
		t.Errorf("SuggestIcon chose an unreadable icon: %+v", c)
	}
}
