package flow

import (
	"strings"
	"testing"

	"github.com/DemiCloud/doszer/internal/install"
)

func lines(steps []Step) string {
	var out []string
	for _, s := range steps {
		if s.Prompt != "" {
			out = append(out, "# "+s.Prompt)
			continue
		}
		out = append(out, strings.Join(s.Args, " "))
	}
	return strings.Join(out, "\n")
}

func TestPrepare(t *testing.T) {
	tests := []struct {
		name string
		o    Options
		want string
	}{
		{"two discs", Options{Name: "Game", Source: "src", Drive: "E:", Discs: 2, Init: true},
			"dump --drive E: src\n# " + DiscPrompt(2, 2, "E:") + "\ndump --drive E: src\ninit --force --name Game src"},
		{"files, choices", Options{Name: "Game", Source: "src", Files: []string{"a.cue", "b.zip"}, Init: true, Boot: "os", Audio: "wav", SystemDir: "sys"},
			"dump --from a.cue src\ndump --from b.zip src\ninit --force --name Game --boot os --audio wav --system-dir sys src"},
		{"ogg is the default", Options{Name: "Game", Source: "src", Files: []string{"a.iso"}, Init: true, Audio: "ogg"},
			"dump --from a.iso src\ninit --force --name Game src"},
		{"folder with a manifest", Options{Name: "Game", Source: "dir", Folder: true}, ""},
		{"folder without", Options{Name: "Game", Source: "dir", Folder: true, Init: true}, "init --force --name Game dir"},
	}
	for _, tt := range tests {
		if got := lines(Prepare(tt.o)); got != tt.want {
			t.Errorf("%s:\n%s\nwant\n%s", tt.name, got, tt.want)
		}
	}
}

func TestLaterSteps(t *testing.T) {
	ins := Install("src", "sys", "GAME", "r.json")
	if got := lines(ins); got != "# "+InstallPrompt+"\ninstall --system-dir sys --dir GAME src" || len(ins[1].Env) != 1 || ins[1].Env[0] != install.ResultEnv+"=r.json" {
		t.Errorf("install: %q, env %q", got, ins[1].Env)
	}
	r := &install.Retry{From: "x.pure.zip", Content: "c.dosz"}
	if got := strings.Join(Extract("src", "", r, "TOOLS").Args, " "); got != "install --from x.pure.zip --content c.dosz --dir TOOLS src" {
		t.Errorf("extract: %s", got)
	}
	if got := strings.Join(Start("src", "GAME/GAME.EXE", "640x480x8").Args, " "); got != "start --force --display 640x480x8 src GAME/GAME.EXE" {
		t.Errorf("start: %s", got)
	}
	if got := strings.Join(Build("src", "out.dosz", true).Args, " "); got != "build --flatten src out.dosz" {
		t.Errorf("build: %s", got)
	}
	if got := SafeName(`Star Wars: Yoda / "Stories"?`); got != "Star Wars Yoda  Stories" {
		t.Errorf("safe name: %q", got)
	}
}
