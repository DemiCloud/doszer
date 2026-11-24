package libretro

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DemiCloud/doszer/internal/builtin"
)

// TestRunDOSBoxPure runs the embedded DOSBox Pure core (or the one
// DOSZER_TEST_CORE names) on content whose DOSBOX.BAT writes a file to C:,
// under SDL's dummy drivers: unloading the content must save that file to
// the save directory's <content>.pure.zip.
func TestRunDOSBoxPure(t *testing.T) {
	corePath, sdlLibs := os.Getenv("DOSZER_TEST_CORE"), []string(nil)
	if corePath == "" {
		if !builtin.Available() {
			t.Skip("no embedded core (make core) and DOSZER_TEST_CORE not set")
		}
		var err error
		if corePath, sdlLibs, err = builtin.Extract(); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("SDL_VIDEODRIVER", "dummy")
	t.Setenv("SDL_AUDIODRIVER", "dummy")
	dir := t.TempDir()
	content := filepath.Join(dir, "test.dosz")
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("DOSBOX.BAT")
	io.WriteString(w, "@echo off\r\necho written> C:\\OUT.TXT\r\n")
	zw.Close()
	os.WriteFile(content, buf.Bytes(), 0o644)
	saves, system := filepath.Join(dir, "saves"), filepath.Join(dir, "system")
	os.Mkdir(saves, 0o755)
	os.Mkdir(system, 0o755)

	err := Run(Config{Core: corePath, SDL: sdlLibs, Content: content, SystemDir: system, SaveDir: saves, Title: "test", Frames: 600,
		Options: map[string]string{"dosbox_pure_menu_time": "0"}})
	if err != nil && strings.HasPrefix(err.Error(), "SDL2 (needed") {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(filepath.Join(saves, "test.pure.zip"))
	if err != nil {
		entries, _ := os.ReadDir(saves)
		t.Fatalf("no save (%v): %v", entries, err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name == "OUT.TXT" {
			rc, _ := f.Open()
			b, _ := io.ReadAll(rc)
			rc.Close()
			if string(b) != "written\r\n" {
				t.Fatalf("OUT.TXT = %q", b)
			}
			return
		}
	}
	t.Fatal("OUT.TXT not in the save")
}
