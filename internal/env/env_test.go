package env

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DemiCloud/doszer/internal/config"
)

func TestRead(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir()) // no RetroDECK
	sys := t.TempDir()
	write := func(rel string, size int64) {
		p := filepath.Join(sys, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		f, err := os.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := f.Truncate(size); err != nil {
			t.Fatal(err)
		}
	}
	write("dosbox_pure/Win31.dosz", 1000)
	write("dosbox_pure/Win98.img", 8<<20)
	write("dosbox_pure/small.img", 1<<20)  // under 7 MB: DOSBox Pure skips it
	write("dosbox_pure/odd.img", 8<<20+1) // not whole sectors: skipped too

	cfg := config.Default()
	cfg.SystemDir = "/nonexistent" // --system-dir wins
	cfg.DefaultShell = "dosbox_pure/Win31"
	cfg.DefaultOS = "dosbox_pure/Win95"
	cfg.InstallOS = map[string]string{"dosbox_pure/Win98": "dosbox_pure/Win98"}

	r, err := Read(cfg, sys, "")
	if err != nil {
		t.Fatal(err)
	}
	if r.SystemDir != (Dir{sys, "--system-dir"}) {
		t.Errorf("system dir = %+v", r.SystemDir)
	}
	if len(r.Shells) != 1 || r.Shells[0].Name != "dosbox_pure/Win31" {
		t.Errorf("shells = %+v", r.Shells)
	}
	if len(r.Images) != 1 || r.Images[0].Name != "dosbox_pure/Win98" || r.Images[0].Kind != "raw image" {
		t.Errorf("images = %+v", r.Images)
	}
	want := []Setting{
		{"default_shell", "dosbox_pure/Win31", true},
		{"default_os", "dosbox_pure/Win95", false},
		{"install_os.dosbox_pure/Win98", "dosbox_pure/Win98", true},
	}
	if len(r.Settings) != len(want) {
		t.Fatalf("settings = %+v", r.Settings)
	}
	for i := range want {
		if r.Settings[i] != want[i] {
			t.Errorf("setting %d = %+v, want %+v", i, r.Settings[i], want[i])
		}
	}

	var b bytes.Buffer
	Print(&b, r)
	for _, s := range []string{"(--system-dir)", "dosbox_pure/Win98  raw image, 8.0 MB", "dosbox_pure/Win95 (not in the system directory here)"} {
		if !strings.Contains(b.String(), s) {
			t.Errorf("output lacks %q:\n%s", s, b.String())
		}
	}

	if _, err := Read(config.Default(), filepath.Join(sys, "missing"), ""); err == nil {
		t.Error("a missing --system-dir was accepted")
	}
}
