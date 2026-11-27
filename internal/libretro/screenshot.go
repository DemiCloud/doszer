package libretro

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"time"

	"github.com/DemiCloud/doszer/internal/runlog"
	"github.com/DemiCloud/doszer/internal/status"
)

// saveScreenshot writes an XRGB8888 frame as a PNG at the core's own
// resolution, in the background, and says where on stderr.
func (h *host) saveScreenshot(pix []byte, width, height, pitch int) {
	img := frameImage(pix, width, height, pitch)
	dir := h.shotDir
	name := fmt.Sprintf("%s %s.png", h.name, time.Now().Format("2006-01-02 15-04-05.000"))
	go func() {
		if dir == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				status.Printf(runlog.Stderr, "[WARN] screenshot: %v\n", err)
				return
			}
			dir = filepath.Join(home, "Pictures", "doszer")
		}
		p := filepath.Join(dir, name)
		if err := writePNG(p, img); err != nil {
			status.Printf(runlog.Stderr, "[WARN] screenshot: %v\n", err)
			return
		}
		status.Printf(runlog.Stderr, "Screenshot: %s\n", p)
	}()
}

// frameImage copies an XRGB8888 frame (bytes B, G, R, X) into an image.
func frameImage(pix []byte, width, height, pitch int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		src, dst := pix[y*pitch:], img.Pix[y*img.Stride:]
		for x := 0; x < width; x++ {
			dst[x*4], dst[x*4+1], dst[x*4+2], dst[x*4+3] = src[x*4+2], src[x*4+1], src[x*4], 255
		}
	}
	return img
}

func writePNG(p string, img image.Image) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := os.Create(p)
	if err != nil {
		return err
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		os.Remove(p)
		return err
	}
	return f.Close()
}
