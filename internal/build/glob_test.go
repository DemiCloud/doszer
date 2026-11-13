package build

import "testing"

func TestMatchGlob(t *testing.T) {
	tests := []struct {
		pattern string
		path    string
		want    bool
	}{
		{"**/*.HLP", "readme.hlp", true},
		{"**/*.HLP", "GAME/HELP/readme.hlp", true},
		{"**/*.HLP", "GAME/readme.txt", false},
		{"**/UNINST*.*", "GAME/UNINST32.EXE", true},
		{"**/UNINST*.*", "GAME/SUB/UNINST.BAT", true},
		{"**/UNINST*.*", "GAME/INSTALL.EXE", false},
		{"GAME/**", "GAME/A/B/C.TXT", true},
		{"GAME/**", "GAME/A.TXT", true},
		{"GAME/**", "OTHER/A.TXT", false},
		{"*.INI", "config.ini", true},
		{"*.INI", "SUB/config.ini", false},
		{"CONFIG.SYS", "CONFIG.SYS", true},
		{"CONFIG.SYS", "config.sys", true},
		{"a/**/b", "a/b", true},
		{"a/**/b", "a/x/b", true},
		{"a/**/b", "a/x/y/b", true},
		{"a/**/b", "a/x/y", false},
	}

	for _, tt := range tests {
		t.Run(tt.pattern+" vs "+tt.path, func(t *testing.T) {
			if got := matchGlob(tt.pattern, tt.path); got != tt.want {
				t.Errorf("matchGlob(%q, %q) = %v, want %v", tt.pattern, tt.path, got, tt.want)
			}
		})
	}
}
