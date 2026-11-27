package libretro

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/DemiCloud/doszer/internal/builtin"
)

// TestProbe runs real content headless and saves a screenshot every
// DOSZER_PROBE_EVERY frames (default 300, 5 seconds) to DOSZER_PROBE_OUT:
// a way to see what a game does without a window. Skipped unless
// DOSZER_PROBE_CONTENT is set; DOSZER_PROBE_SYSTEM is the system
// directory, DOSZER_PROBE_FRAMES how long to run (default 7200). Saves
// are kept in DOSZER_PROBE_OUT/saves. DOSZER_PROBE_SWAP is a frame
// after which to swap discs (Ctrl+F4), logging the disc then in the drive.
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
	saves := filepath.Join(out, "saves")
	if err := os.MkdirAll(saves, 0o755); err != nil {
		t.Fatal(err)
	}
	next := every
	ramdisk := os.Getenv("DOSZER_PROBE_RAMDISK") // dosbox_pure_bootos_ramdisk; default diff, to read C: back
	if ramdisk == "" {
		ramdisk = "diff"
	}
	last := 0
	probeSwapAt = envInt("DOSZER_PROBE_SWAP", 0)
	probeKeys = probeKeyScript(t, os.Getenv("DOSZER_PROBE_KEYS"))
	defer func() {
		probeKeys = nil
		if probeSwapAt != 0 {
			t.Logf("swapped at frame %d: %q", probeSwapAt, probeDisc)
		}
		probeSwapAt, probeDisc = 0, ""
	}()
	err = Run(Config{Core: corePath, SDL: sdlLibs, Content: content, SystemDir: os.Getenv("DOSZER_PROBE_SYSTEM"),
		SaveDir: saves, Title: "probe", Frames: frames, Paced: os.Getenv("DOSZER_PROBE_PACED") != "",
		Options: map[string]string{"dosbox_pure_menu_time": "0", "dosbox_pure_bootos_ramdisk": ramdisk},
		OnFrame: func(frame int, pix []byte, w, h, pitch int) {
			last = frame
			if frame < next {
				return
			}
			next = frame + every
			if err := writePNG(filepath.Join(out, fmt.Sprintf("%06d.png", frame)), frameImage(pix, w, h, pitch)); err != nil {
				t.Error(err)
			}
		}})
	if err != nil {
		t.Fatal(err)
	}
	if last < frames {
		t.Logf("the core closed the session at frame %d", last)
	} else {
		t.Logf("still running after %d frames", frames)
	}
}

func envInt(key string, def int) int {
	if n, err := strconv.Atoi(os.Getenv(key)); err == nil && n > 0 {
		return n
	}
	return def
}

// probeKeyScript parses DOSZER_PROBE_KEYS: space-separated FRAME:KEYS,
// KEYS comma-separated names (down, up, left, right, enter, esc, tab,
// space, bs, colon, or one character), each optionally *N; text=STRING
// types STRING. From FRAME on, one key is pressed and released per two
// frames.
func probeKeyScript(t *testing.T, script string) func(int, func(uint32, bool)) {
	if script == "" {
		return nil
	}
	named := map[string][]uint32{"down": {274}, "up": {273}, "right": {275}, "left": {276},
		"enter": {13}, "esc": {27}, "tab": {9}, "space": {32}, "bs": {8}, "colon": {304, 59}}
	type press struct {
		frame int
		keys  []uint32 // held together: modifiers first
	}
	var queue []press
	for _, item := range strings.Fields(script) {
		f, spec, ok := strings.Cut(item, ":")
		frame, err := strconv.Atoi(f)
		if !ok || err != nil {
			t.Fatalf("DOSZER_PROBE_KEYS %q: want FRAME:KEYS", item)
		}
		var keys [][]uint32
		for _, tok := range strings.Split(spec, ",") {
			if text, ok := strings.CutPrefix(tok, "text="); ok {
				for _, c := range strings.ToLower(text) {
					if c == ':' {
						keys = append(keys, named["colon"])
					} else {
						keys = append(keys, []uint32{uint32(c)})
					}
				}
				continue
			}
			n := 1
			if name, count, ok := strings.Cut(tok, "*"); ok {
				tok = name
				n, _ = strconv.Atoi(count)
			}
			k, ok := named[tok]
			if !ok && len(tok) == 1 {
				k = []uint32{uint32(strings.ToLower(tok)[0])}
			} else if !ok {
				t.Fatalf("DOSZER_PROBE_KEYS: unknown key %q", tok)
			}
			for range n {
				keys = append(keys, k)
			}
		}
		for i, k := range keys {
			queue = append(queue, press{frame + 2*i, k})
		}
	}
	return func(frame int, pressKey func(uint32, bool)) {
		for _, p := range queue {
			switch frame {
			case p.frame:
				for _, k := range p.keys {
					pressKey(k, true)
				}
			case p.frame + 1:
				for i := len(p.keys) - 1; i >= 0; i-- {
					pressKey(p.keys[i], false)
				}
			}
		}
	}
}
