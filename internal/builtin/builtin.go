// Package builtin holds the DOSBox Pure libretro core (and, on Windows,
// SDL2) embedded into doszer at build time by `make core`, for the
// built-in frontend (internal/libretro). They're extracted to a cache
// directory named after their content on first use.
package builtin

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
)

// DOSBoxPureVersion is the DOSBox Pure release `make core` builds.
const DOSBoxPureVersion = "1.0-preview8"

// coreName is the core's file name on this platform.
func coreName() string {
	switch runtime.GOOS {
	case "windows":
		return "dosbox_pure_libretro.dll"
	case "darwin":
		return "dosbox_pure_libretro.dylib"
	}
	return "dosbox_pure_libretro.so"
}

// sdlLibs are the SDL libraries embedded beside the core, in load order:
// on Windows sdl2-compat's SDL2.dll and the SDL3.dll it loads, loaded
// first by full path so SDL2.dll finds it. Elsewhere the system's SDL2.
func sdlLibs() []string {
	if runtime.GOOS == "windows" {
		return []string{"SDL3.dll", "SDL2.dll"}
	}
	return nil
}

func files() (fs.FS, error) {
	sub, err := fs.Sub(embedded, "data/"+platform)
	if err != nil {
		return nil, err
	}
	return sub, nil
}

// Available reports whether this build embeds the core.
func Available() bool {
	f, err := files()
	if err != nil {
		return false
	}
	_, err = fs.Stat(f, coreName())
	return err == nil
}

// Extract writes the embedded files to the cache directory, if not already
// there, and returns the core's path and the SDL libraries to load (empty:
// the system's).
func Extract() (core string, sdl []string, err error) {
	if !Available() {
		return "", nil, errors.New("this doszer was built without the DOSBox Pure core (make core), use --frontend retroarch or standalone")
	}
	f, _ := files()
	names := []string{coreName()}
	names = append(names, sdlLibs()...)
	sort.Strings(names)
	sum := sha256.New()
	data := map[string][]byte{}
	for _, n := range names {
		b, err := fs.ReadFile(f, n)
		if err != nil {
			return "", nil, fmt.Errorf("embedded %s: %w", n, err)
		}
		data[n] = b
		sum.Write([]byte(n))
		sum.Write(b)
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", nil, err
	}
	dir := filepath.Join(cache, "doszer", "dosbox-pure-"+DOSBoxPureVersion+"-"+hex.EncodeToString(sum.Sum(nil))[:12])
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", nil, err
	}
	for _, n := range names {
		p := filepath.Join(dir, path.Base(n))
		if fi, err := os.Stat(p); err == nil && fi.Size() == int64(len(data[n])) {
			continue
		}
		tmp, err := os.CreateTemp(dir, ".extract-*")
		if err != nil {
			return "", nil, err
		}
		_, werr := tmp.Write(data[n])
		cerr := tmp.Close()
		if werr == nil {
			werr = cerr
		}
		if werr == nil {
			werr = os.Chmod(tmp.Name(), 0o755)
		}
		if werr == nil {
			werr = os.Rename(tmp.Name(), p)
		}
		if werr != nil {
			os.Remove(tmp.Name())
			return "", nil, fmt.Errorf("extracting %s: %w", n, werr)
		}
	}
	core = filepath.Join(dir, coreName())
	for _, n := range sdlLibs() {
		sdl = append(sdl, filepath.Join(dir, n))
	}
	return core, sdl, nil
}
