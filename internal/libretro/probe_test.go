package libretro

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/DemiCloud/doszer/internal/builtin"
)

// TestProbe runs real content headless and saves a screenshot every
// DOSZER_PROBE_EVERY frames (default 300, 5 seconds) to DOSZER_PROBE_OUT:
// a way to see what a game does without a window. Skipped unless
// DOSZER_PROBE_CONTENT is set; DOSZER_PROBE_SYSTEM is the system
// directory, DOSZER_PROBE_FRAMES how long to run (default 7200).
func TestProbe(t *testing.T) {
	content := os.Getenv("DOSZER_PROBE_CONTENT")
	if content == "" {
		t.Skip("DOSZER_PROBE_CONTENT not set")
	}
	out := os.Getenv("DOSZER_PROBE_OUT")
	every := envInt("DOSZER_PROBE_EVERY", 300)
	frames := envInt("DOSZER_PROBE_FRAMES", 7200)
	corePath, sdlLibs, err := builtin.Extract()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SDL_VIDEODRIVER", "dummy")
	t.Setenv("SDL_AUDIODRIVER", "dummy")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	next := every
	err = Run(Config{Core: corePath, SDL: sdlLibs, Content: content, SystemDir: os.Getenv("DOSZER_PROBE_SYSTEM"),
		SaveDir: t.TempDir(), Title: "probe", Frames: frames,
		Options: map[string]string{"dosbox_pure_menu_time": "0", "dosbox_pure_bootos_ramdisk": "diff"},
		OnFrame: func(frame int, pix []byte, w, h, pitch int) {
			if frame < next {
				return
			}
			next = frame + every
			img := image.NewRGBA(image.Rect(0, 0, w, h))
			for y := 0; y < h; y++ {
				for x := 0; x < w; x++ {
					p := pix[y*pitch+x*4:]
					o := img.PixOffset(x, y)
					img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = p[2], p[1], p[0], 255
				}
			}
			f, err := os.Create(filepath.Join(out, fmt.Sprintf("%06d.png", frame)))
			if err != nil {
				t.Error(err)
				return
			}
			png.Encode(f, img)
			f.Close()
		}})
	if err != nil {
		t.Fatal(err)
	}
}

func envInt(key string, def int) int {
	if n, err := strconv.Atoi(os.Getenv(key)); err == nil && n > 0 {
		return n
	}
	return def
}
