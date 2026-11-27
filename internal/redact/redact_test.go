package redact

import (
	"strings"
	"testing"
)

func TestString(t *testing.T) {
	linux := For("/home/alice", "alice", "workstation")
	windows := For(`C:\Users\Alice`, "Alice", "DESKTOP-42")
	tests := []struct {
		name string
		r    *Redactor
		in   string
		want string
	}{
		{"home", linux, "built /home/alice/games/x.dosz", "built ~/games/x.dosz"},
		{"home alone", linux, "cd /home/alice", "cd ~"},
		{"home prefix only", linux, "/home/alicex/y", "/home/<user>/y"},
		{"other user", linux, "from /home/bob/roms", "from /home/<user>/roms"},
		{"user word", linux, "owner alice, host workstation", "owner <user>, host <host>"},
		{"user inside word", linux, "malice", "malice"},
		{"windows home", windows, `C:\Users\Alice\roms\a.dosz`, `~\roms\a.dosz`},
		{"windows home slashes", windows, `C:/Users/Alice/roms`, `~/roms`},
		{"windows home from wsl", windows, "/mnt/c/Users/Alice/roms", "~/roms"},
		{"windows other user", windows, `D:\Users\Bob\x and d:\\users\\bob\\y`, `D:\Users\<user>\x and d:\\users\\<user>\\y`},
		{"wsl other user", linux, "/mnt/d/Users/Bob/x", "/mnt/d/Users/<user>/x"},
		{"macos", linux, "at /Users/carol/x", "at /Users/<user>/x"},
		{"host", windows, "on desktop-42.", "on <host>."},
		{"nothing", linux, "D:/RetroDECK/bios", "D:/RetroDECK/bios"},
		{"short name kept", For("", "al", "pc"), "al and pc", "al and pc"},
	}
	for _, tt := range tests {
		if got := tt.r.String(tt.in); got != tt.want {
			t.Errorf("%s: %q = %q, want %q", tt.name, tt.in, got, tt.want)
		}
	}
}

func TestJSON(t *testing.T) {
	r := For(`C:\Users\Alice`, "Alice", "")
	got, err := r.JSON([]byte(`{"dir":"C:\\Users\\Alice\\src","C:\\Users\\Alice":[1,"x"]}`))
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if strings.Contains(s, "Alice") || !strings.Contains(s, `"~\\src"`) || !strings.Contains(s, `"~": [`) {
		t.Errorf("JSON:\n%s", s)
	}
}
