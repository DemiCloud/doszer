package manifest

import (
	"strings"
	"testing"
)

func TestSetExec(t *testing.T) {
	const exec = `C:\GAME\GO.BAT`
	tests := []struct {
		name, in, want string
	}{
		{
			"replaces the template's commented-out line",
			"format = 1\n[boot]\n# Set at most one.\n# exec = 'C:\\GAME\\GAME.EXE'\n# shell = \"x\"\n\n[layout]\n# exec = 'not this'\n",
			"format = 1\n[boot]\n# Set at most one.\nexec = 'C:\\GAME\\GO.BAT'\n# shell = \"x\"\n\n[layout]\n# exec = 'not this'\n",
		},
		{
			"replaces a set value, keeping CRLF",
			"format = 1\r\n[boot]  # startup\r\nexec = 'C:\\OLD.EXE'\r\n",
			"format = 1\r\n[boot]  # startup\r\nexec = 'C:\\GAME\\GO.BAT'\r\n",
		},
		{
			"adds the key after [boot]",
			"format = 1\n[boot]\ndisc = 'Game'\n",
			"format = 1\n[boot]\nexec = 'C:\\GAME\\GO.BAT'\ndisc = 'Game'\n",
		},
		{
			"appends a [boot] table",
			"format = 1\n[layout]\nflatten = true",
			"format = 1\n[layout]\nflatten = true\n\n[boot]\nexec = 'C:\\GAME\\GO.BAT'\n",
		},
		{
			"ignores exec lines in other tables",
			"format = 1\n[layout]\n# exec = 'x'\n[boot]\n",
			"format = 1\n[layout]\n# exec = 'x'\n[boot]\nexec = 'C:\\GAME\\GO.BAT'\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SetExec([]byte(tt.in), exec)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Errorf("got\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}

func TestSetExecOnTemplate(t *testing.T) {
	got, err := SetExec([]byte(Template(TemplateOptions{Name: "Example Game"})), `C:\GAME\GAME.BAT`)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(got), "exec = 'C:\\GAME\\GAME.BAT'\n"); n != 1 {
		t.Errorf("%d exec lines set, want 1:\n%s", n, got)
	}
}

func TestSetExecRejectsAnotherModel(t *testing.T) {
	if _, err := SetExec([]byte("format = 1\n[boot]\nos = 'dosbox_pure/image'\n"), `C:\GO.BAT`); err == nil {
		t.Error("set exec beside os")
	}
}

func TestTemplateMediaLabels(t *testing.T) {
	disc := func(id, label string) Media {
		return Media{ID: id, Label: label, Tracks: []Track{{Type: TrackData, SHA1: strings.Repeat("0", 40)}}}
	}
	tests := []struct {
		name   string
		media  []Media
		labels []string
	}{
		{"one disc takes the game's name", []Media{disc("disc1", "GAME_CD")}, []string{"label  = \"King's Quest\""}},
		{"several keep their file names", []Media{disc("disc1", "Game (Disc 1)"), disc("disc2", "Game (Disc 2)")}, []string{"label  = 'Game (Disc 1)'", "label  = 'Game (Disc 2)'"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Template(TemplateOptions{Name: "King's Quest", Media: tt.media})
			for _, l := range tt.labels {
				if !strings.Contains(got, l+"\n") {
					t.Errorf("no %s in:\n%s", l, got)
				}
			}
			if _, err := Parse([]byte(got), "x"); err != nil {
				t.Error(err)
			}
		})
	}
}
