package platform

import (
	"os"
	"path/filepath"
	"testing"
)

// TestStart: what init starts a plain DOS game with - the one program
// left once helpers and disc programs are set aside, else the one named
// like its folder or the game - and the game's own batch file running it.
func TestStart(t *testing.T) {
	dos, w95 := Target{Platform: DOS}, Target{Win32, 4, 0}
	tests := []struct {
		name  string
		progs []Program
		want  string // "" for none
	}{
		{"one", []Program{{"GAME/GAME.EXE", dos}, {"GAME/SETUP.EXE", dos}, {"GAME/DOS4GW.EXE", dos}, {"cd.iso:INSTALL/GO.EXE", dos}}, "GAME/GAME.EXE"},
		{"named like its folder", []Program{{"DESCENT2/DESCENT2.EXE", dos}, {"DESCENT2/PCXVIEW.EXE", dos}}, "DESCENT2/DESCENT2.EXE"},
		{"named like the game", []Program{{"EXAMPLE/EXGAME.EXE", dos}, {"EXAMPLE/EDITOR.EXE", dos}}, "EXAMPLE/EXGAME.EXE"},
		{"ambiguous", []Program{{"A/RUN.EXE", dos}, {"A/EDIT.EXE", dos}}, ""},
		{"windows only", []Program{{"GAME.EXE", w95}}, ""},
	}
	for _, tt := range tests {
		s := Starters(tt.progs)
		got := ""
		if len(s) == 1 {
			got = s[0].Path
		} else if p := Likely(s, "Ex Game"); p != nil {
			got = p.Path
		}
		if got != tt.want {
			t.Errorf("%s: %q, want %q", tt.name, got, tt.want)
		}
	}

	dir := t.TempDir()
	write := func(rel, text string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(text), 0o644)
	}
	write("SIERRA/KQ.BAT", "@echo off\r\ncd \\sierra\\kq\r\nC:\\sierra\\kq\\sierra -o resource.cfg\r\n")
	write("SIERRA/KQ/KQ.BAT", "sierra -o resource.cfg\r\n")
	write("SIERRA/KQ/README.BAT", "type readme.txt | more\r\n")
	write("SIERRA/KQ/SETUP.BAT", "sierra -config\r\n") // a helper's name
	write("SIERRA/KQ/OTHER.BAT", "sierrax\r\n")        // another program
	if got := Launcher(dir, "SIERRA/KQ/SIERRA.EXE"); got != "SIERRA/KQ.BAT" {
		t.Errorf("Launcher = %q, want the shallowest batch file running it", got)
	}
	write("HOCUS/NEWINST.BAT", "@echo off\r\necho Press any key...\r\npause\r\nSETUP\r\nHOCUS\r\n")
	write("HOCUS/HOCUSG.BAT", "@ultramid -q  \r\n@__hpgrvs\r\n@ultramid -q -f\r\n")
	if got := Launcher(dir, "HOCUS/HOCUS.EXE"); got != "" {
		t.Errorf("Launcher = %q, want none: it pauses and runs setup first", got)
	}
	if got := Launcher(dir, "HOCUS/__HPGRVS.EXE"); got != "HOCUS/HOCUSG.BAT" {
		t.Errorf("Launcher = %q, want the batch file around the driver it loads", got)
	}
	if got := Launcher(dir, "HOCUS/ULTRAMID.EXE"); got != "" {
		t.Errorf("Launcher = %q, want none: the driver isn't its main command", got)
	}
	write("TWO/A.BAT", "game\r\n")
	write("TWO/B.BAT", "game.exe /x\r\n")
	if got := Launcher(dir, "TWO/GAME.EXE"); got != "" {
		t.Errorf("Launcher = %q, want none of two equally deep", got)
	}

	for _, tt := range []struct{ in, want string }{
		{"GAME/GAME.EXE", `C:\GAME\GAME.EXE`},
		{"game/go.bat", `C:\GAME\GO.BAT`},
		{"Long Name/GAME.EXE", ""},
		{"GAME/GAME.TAR.EXE", ""},
	} {
		got, ok := DOSPath(tt.in)
		if !ok {
			got = ""
		}
		if got != tt.want {
			t.Errorf("DOSPath(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestBatchMain(t *testing.T) {
	for _, tt := range []struct{ text, want string }{
		{"@echo off\r\ncd \\sierra\\kq\r\nC:\\sierra\\kq\\sierra -o resource.cfg\r\n", "sierra"},
		{"C:\r\ncd\\HOCUS\r\nHOCUS\r\n", "hocus"},
		{"@ultramid -q\r\n@__hpgrvs\r\n@ultramid -q -f\r\n", "__hpgrvs"},
		{"lh mouse\r\ncall game.bat\r\n", "game"},
		{"echo Insert the CD\r\npause\r\ngame\r\n", ""},
		{"setup\r\ngame\r\n", ""},
		{"@echo off\r\n:start\r\nif errorlevel 1 goto start\r\necho.\r\n", ""},
	} {
		if got := BatchMain(tt.text); got != tt.want {
			t.Errorf("BatchMain(%q) = %q, want %q", tt.text, got, tt.want)
		}
	}
}
