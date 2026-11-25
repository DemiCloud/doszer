// Package builtin holds the programs embedded into doszer at build time:
// the DOSBox Pure libretro core (and, on Windows, SDL2) for the built-in
// frontend (internal/libretro, `make core`), and redumper for `doszer
// dump` (`make redumper`). They're extracted to a cache directory named
// after their content on first use. Guest programs, run inside DOSBox Pure
// rather than on the host, are the same on every platform: RunExit for
// `doszer prepare shell` (`make runexit`), copied into what it writes.
package builtin

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/binary"
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

// Extract writes the embedded core and SDL to the cache directory, if not
// already there, and returns the core's path and the SDL libraries to load
// (empty: the system's).
func Extract() (core string, sdl []string, err error) {
	if !Available() {
		return "", nil, errors.New("this doszer was built without the DOSBox Pure core (make core; there is none for this platform)")
	}
	dir, err := extract("dosbox-pure-"+DOSBoxPureVersion, append([]string{coreName()}, sdlLibs()...))
	if err != nil {
		return "", nil, err
	}
	for _, n := range sdlLibs() {
		sdl = append(sdl, filepath.Join(dir, n))
	}
	return filepath.Join(dir, coreName()), sdl, nil
}

// RedumperVersion is the redumper release `make redumper` embeds.
const RedumperVersion = "b773"

// Redumper extracts the embedded redumper and returns its path: the
// Windows build when windows is set (on linux/amd64, for WSL, which can't
// reach optical drives itself), else this platform's.
func Redumper(windows bool) (string, error) {
	name := "redumper"
	if windows || runtime.GOOS == "windows" {
		name = "redumper.exe"
	}
	f, err := files()
	if err == nil {
		_, err = fs.Stat(f, name)
	}
	if err != nil {
		return "", errors.New("this doszer was built without redumper (make redumper)")
	}
	dir, err := extract("redumper-"+RedumperVersion, []string{name})
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

// RunExitVersion is the RunExit release `make runexit` embeds.
const RunExitVersion = "v2.0"

//go:embed data/guest
var guest embed.FS

// RunExit returns the embedded RUNEXIT.EXE (a Windows 3.x program) and its
// licence text, which has to travel with it.
func RunExit() (exe, license []byte, err error) {
	exe, err = guest.ReadFile("data/guest/RUNEXIT.EXE")
	if err == nil {
		license, err = guest.ReadFile("data/guest/RUNEXIT-LICENSE")
	}
	if err != nil {
		return nil, nil, errors.New("this doszer was built without RunExit (make runexit)")
	}
	return exe, license, nil
}

// extract writes the named embedded files to a cache directory named after
// prefix and their content, unless already there, and returns it.
func extract(prefix string, names []string) (string, error) {
	f, err := files()
	if err != nil {
		return "", err
	}
	names = append([]string(nil), names...)
	sort.Strings(names)
	sum := sha256.New()
	data := map[string][]byte{}
	for _, n := range names {
		b, err := fs.ReadFile(f, n)
		if err != nil {
			return "", fmt.Errorf("embedded %s: %w", n, err)
		}
		data[n] = b
		sum.Write([]byte(n))
		sum.Write(b)
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cache, "doszer", prefix+"-"+hex.EncodeToString(sum.Sum(nil))[:12])
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	for _, n := range names {
		p := filepath.Join(dir, path.Base(n))
		if fi, err := os.Stat(p); err == nil && fi.Size() == int64(len(data[n])) {
			continue
		}
		tmp, err := os.CreateTemp(dir, ".extract-*")
		if err != nil {
			return "", err
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
			return "", fmt.Errorf("extracting %s: %w", n, werr)
		}
	}
	return dir, nil
}

// Program is a program embedded into doszer, for `doszer version`.
type Program struct {
	Name, Version, URL string
	Embedded           bool // false: this build doesn't carry it
}

// Programs lists the programs doszer embeds, with their versions and
// source repositories.
func Programs() []Program {
	f, _ := files()
	has := func(names ...string) bool {
		for _, n := range names {
			if f == nil {
				return false
			}
			if _, err := fs.Stat(f, n); err == nil {
				return true
			}
		}
		return false
	}
	progs := []Program{
		{"DOSBox Pure", DOSBoxPureVersion, "https://codeberg.org/schelling/dosbox-pure", has(coreName())},
		{"redumper", RedumperVersion, "https://github.com/superg/redumper", has("redumper", "redumper.exe")},
	}
	_, _, rerr := RunExit()
	progs = append(progs, Program{"RunExit", RunExitVersion, "https://github.com/andshrew/RunExit", rerr == nil})
	if runtime.GOOS == "windows" {
		for _, s := range []struct{ name, file, url string }{
			{"SDL", "SDL3.dll", "https://github.com/libsdl-org/SDL"},
			{"sdl2-compat", "SDL2.dll", "https://github.com/libsdl-org/sdl2-compat"},
		} {
			p := Program{Name: s.name, URL: s.url, Embedded: has(s.file)}
			if p.Embedded {
				data, _ := fs.ReadFile(f, s.file)
				p.Version = peVersion(data)
			}
			progs = append(progs, p)
		}
	}
	return progs
}

// peVersion returns the file version in a Windows executable's
// VS_FIXEDFILEINFO (signature 0xFEEF04BD, then the struct version, then
// the version as two DWORDs of 16-bit parts), or "" if it has none.
func peVersion(data []byte) string {
	i := bytes.Index(data, []byte{0xBD, 0x04, 0xEF, 0xFE})
	if i < 0 || i+16 > len(data) {
		return ""
	}
	ms := binary.LittleEndian.Uint32(data[i+8:])
	ls := binary.LittleEndian.Uint32(data[i+12:])
	return fmt.Sprintf("%d.%d.%d", ms>>16, ms&0xffff, ls>>16)
}
