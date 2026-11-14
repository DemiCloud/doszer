package build

import (
	"testing"
	"time"

	"github.com/DemiCloud/doszer/internal/manifest"
)

func TestReplaceFold(t *testing.T) {
	got, n := replaceFold(`SET PATH=C:\OLDPATH\BIN`, `C:\OLDPATH`, `C:\GAME`)
	if n != 1 {
		t.Errorf("n = %d, want 1", n)
	}
	if want := `SET PATH=C:\GAME\BIN`; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestReplaceFoldCaseInsensitive(t *testing.T) {
	got, n := replaceFold(`path=c:\oldpath`, `C:\OLDPATH`, `C:\NEW`)
	if n != 1 || got != `path=C:\NEW` {
		t.Errorf("got %q, n=%d", got, n)
	}
}

func TestApplyRewrite(t *testing.T) {
	sourceTime := time.Date(1994, 1, 1, 0, 0, 0, 0, time.UTC)

	fs := newFileSet()
	fs.set("A.INI", dataEntry([]byte(`DIR=C:\OLD`), sourceTime))
	fs.set("B.INI", dataEntry([]byte(`DIR=C:\OTHER`), Epoch))
	fs.set("A.EXE", dataEntry([]byte(`C:\OLD binary blob`), Epoch))

	rules := []manifest.Rewrite{
		{From: `C:\OLD`, To: `C:\NEW`, Files: []string{"*.INI"}},
		{From: "NOPE", To: "x", Files: []string{"*.INI"}},
	}

	hits, warnings, err := applyRewrite(fs, rules)
	if err != nil {
		t.Fatalf("applyRewrite: %v", err)
	}

	if len(hits) != 1 || hits[0].Path != "A.INI" || hits[0].Rule != 0 {
		t.Errorf("hits = %+v, want one hit on A.INI for rule 0", hits)
	}

	a, _ := fs.get("A.INI")
	if got, _ := a.bytes(); string(got) != `DIR=C:\NEW` {
		t.Errorf("A.INI = %q", got)
	}
	if !a.modified.Equal(sourceTime) {
		t.Errorf("A.INI modified = %v, want rewrite to preserve the source time %v (§8.2)", a.modified, sourceTime)
	}

	// The binary file must not be touched even though it contains the text.
	exe, _ := fs.get("A.EXE")
	if got, _ := exe.bytes(); string(got) != `C:\OLD binary blob` {
		t.Errorf("A.EXE was rewritten: %q", got)
	}

	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want one warning for the NOPE rule", warnings)
	}
}

// TestApplyRewriteByPattern: a rule rewrites any NUL-free file its
// patterns name, whatever its extension (§6.5), never one with a NUL byte.
func TestApplyRewriteByPattern(t *testing.T) {
	tests := []struct {
		name, path, content, want string
	}{
		{"INI-style file, other extension", "GAME/RESOURCE.WIN", "etc=C:\\SIERRA\\GAME\\3DRES\r\n", "etc=D:\\GAME\\3DRES\r\n"},
		{"binary file the rule names", "GAME/GAME.EXE", "MZ\x00C:\\SIERRA\\GAME", "MZ\x00C:\\SIERRA\\GAME"},
		{"file the rule doesn't name", "GAME/OTHER.CFG", "C:\\SIERRA\\GAME", "C:\\SIERRA\\GAME"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := newFileSet()
			fs.set(tt.path, dataEntry([]byte(tt.content), Epoch))
			rules := []manifest.Rewrite{{From: `C:\SIERRA\GAME`, To: `D:\GAME`, Files: []string{"GAME/RESOURCE.WIN", "GAME/GAME.EXE"}}}
			if _, _, err := applyRewrite(fs, rules); err != nil {
				t.Fatal(err)
			}
			e, _ := fs.get(tt.path)
			if got, _ := e.bytes(); string(got) != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
