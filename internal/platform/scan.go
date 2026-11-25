package platform

import (
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/DemiCloud/doszer/internal/cue"
	"github.com/DemiCloud/doszer/internal/iso9660"
)

// Program is one executable and what it runs on.
type Program struct {
	Path   string // slash-separated; inside a disc image: "<image>:<path>"
	Target Target
}

// helperName matches programs that set a game up or come with it rather
// than run it: installers, uninstallers, AutoRun menus, README viewers,
// registration cards, DirectX setup, Acrobat Reader, DOS extenders a game
// loads itself (DOS/4GW, PMODE/W), sound setup, archivers. A DOS game often
// ships a Windows setup program or the reverse, and they say nothing
// about what the game itself needs.
var helperName = regexp.MustCompile(`(?i)^(setup|install|instal|inst|_inst|_isdel|uninst|unins\d*|uninstal|isuninst|autorun|setupw|setup32|winsetup|dxsetup\w*|directx|dsetup|ddhelp|readme|e?regcard|regist\w*|register|ar(16|32)e\d+|acrord\w*|checknt|winarj|dos4gw|dos32a|pmodew|cwsdpmi|rtm|rtmres|dpmi16bi|sndsetup|setsound|setsnd|soundset|setblast|mssetup|pkunzip|pkzip|lha|arj|unzip|vesatest|univbe|uvconfig|vbetest)\.(exe|com)$`)

// helperDir matches directories that hold only such helpers.
var helperDir = regexp.MustCompile(`(?i)^(directx|dx\d*|acrobat|acroread|redist)$`)

// IsHelper reports whether p names an installer or another helper that
// comes with a game (helperName), or is in a helper directory.
func IsHelper(p string) bool {
	if _, in, ok := strings.Cut(p, ":"); ok {
		p = in
	}
	if helperName.MatchString(path.Base(p)) {
		return true
	}
	for _, d := range strings.Split(path.Dir(p), "/") {
		if helperDir.MatchString(d) {
			return true
		}
	}
	return false
}

// Need is what a game's programs need.
type Need struct {
	Game  Target // the oldest system that runs every program but helpers
	DOS   bool   // Game is Windows, but there are DOS programs too: a DOS version
	Setup Target // what helpers need, if more than Game
}

func (n Need) String() string {
	if n.DOS {
		return "DOS or " + n.Game.String()
	}
	return n.Game.String()
}

// Needs works out what a game's programs need. Programs installed in the
// source (paths without a disc image prefix) decide when there are any:
// a disc carries more than the game (a Windows setup branch, DirectX,
// readers). Helpers count only if they're all there is.
func Needs(progs []Program) Need {
	onDisc := func(p Program) bool { return strings.Contains(p.Path, ":") }
	installed := false
	for _, p := range progs {
		if !onDisc(p) && !IsHelper(p.Path) {
			installed = true
		}
	}
	var set []Program
	for _, p := range progs {
		if !installed || !onDisc(p) {
			set = append(set, p)
		}
	}
	others := false
	for _, p := range set {
		if !IsHelper(p.Path) {
			others = true
		}
	}
	var n Need
	dos := false
	max := func(t *Target, u Target) {
		if t.Platform == NotProgram || t.Less(u) {
			*t = u
		}
	}
	for _, p := range set {
		if others && IsHelper(p.Path) {
			max(&n.Setup, p.Target)
			continue
		}
		max(&n.Game, p.Target)
		if p.Target.Platform == DOS {
			dos = true
		}
	}
	n.DOS = dos && (n.Game.Platform == Win16 || n.Game.Platform == Win32)
	if !n.Game.Less(n.Setup) {
		n.Setup = Target{}
	}
	return n
}

// Starters returns the programs a plain DOS game could start with: DOS
// programs installed in the source (not only on a disc), helpers aside.
func Starters(progs []Program) []Program {
	var out []Program
	for _, p := range progs {
		if p.Target.Platform == DOS && !strings.Contains(p.Path, ":") && !IsHelper(p.Path) {
			out = append(out, p)
		}
	}
	return out
}

// Likely returns the one starter named after its folder or after the game
// (name, compared by letters and digits only), e.g. DESCENT2/DESCENT2.EXE,
// or nil if there isn't exactly one.
func Likely(starters []Program, name string) *Program {
	norm := func(s string) string {
		var b strings.Builder
		for _, r := range strings.ToLower(s) {
			if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
				b.WriteRune(r)
			}
		}
		return b.String()
	}
	var hit *Program
	for i, p := range starters {
		base := norm(strings.TrimSuffix(path.Base(p.Path), path.Ext(p.Path)))
		dir := path.Base(path.Dir(p.Path))
		if base != "" && (base == norm(dir) || base == norm(name)) {
			if hit != nil {
				return nil
			}
			hit = &starters[i]
		}
	}
	return hit
}

// Launcher returns the batch file in dir, the source, that starts the
// program at rel: of the batch files whose text runs it by name (a game's
// own, often passing it arguments, e.g. Sierra's "sierra -o
// resource.cfg"), the shallowest, if only one is that shallow. "" if
// there's none.
func Launcher(dir, rel string) string {
	base := strings.TrimSuffix(path.Base(rel), path.Ext(rel))
	runs := regexp.MustCompile(`(?im)^[^\r\n]*?(^|[\s\\/:@])` + regexp.QuoteMeta(base) + `(\.(exe|com))?(\s|$)`)
	best, depth, tie := "", 1<<30, false
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.EqualFold(filepath.Ext(p), ".bat") {
			return nil
		}
		r, _ := filepath.Rel(dir, p)
		r = filepath.ToSlash(r)
		if IsHelper(strings.TrimSuffix(r, path.Ext(r)) + ".exe") {
			return nil
		}
		fi, err := d.Info()
		if err != nil || fi.Size() > 64<<10 {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil || !runs.Match(data) {
			return nil
		}
		n := strings.Count(r, "/")
		switch {
		case n < depth:
			best, depth, tie = r, n, false
		case n == depth:
			tie = true
		}
		return nil
	})
	if tie {
		return ""
	}
	return best
}

// DOSPath returns rel, a slash-separated path in the source, as the path
// DOS sees on C:, if every part of it is a valid 8.3 name: a longer name
// would only be reachable by the short name DOSBox Pure makes up for it.
func DOSPath(rel string) (string, bool) {
	parts := strings.Split(rel, "/")
	for i, p := range parts {
		base, ext, _ := strings.Cut(p, ".")
		if base == "" || len(base) > 8 || len(ext) > 3 || strings.Contains(ext, ".") {
			return "", false
		}
		for _, r := range base + ext {
			if r >= 0x80 || r <= ' ' || strings.ContainsRune(`"*+,/:;<=>?[\]|`, r) {
				return "", false
			}
		}
		parts[i] = strings.ToUpper(p)
	}
	return `C:\` + strings.Join(parts, `\`), true
}

// Scan finds the programs in a source directory: its files, and the files
// on every ISO 9660 disc image in it (an .iso, or a CUE sheet's first data
// track), since a game not yet installed is only its disc.
func Scan(dir string) ([]Program, error) {
	var out []Program
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		switch ext := strings.ToLower(filepath.Ext(p)); {
		case IsProgramName(p):
			f, err := os.Open(p)
			if err != nil {
				return err
			}
			t := Classify(p, f)
			f.Close()
			if t.Platform != NotProgram {
				out = append(out, Program{rel, t})
			}
		case ext == ".iso":
			out = append(out, scanDisc(rel, p, 0, 2048, 0)...)
		case ext == ".cue":
			out = append(out, scanCue(rel, p)...)
		}
		return nil
	})
	return out, err
}

// scanCue scans a CUE sheet's first data track.
func scanCue(rel, p string) []Program {
	bin, base, size, off, ok := cueDataTrack(p)
	if !ok {
		return nil
	}
	return scanDisc(rel, bin, base, size, off)
}

// cueDataTrack finds a CUE sheet's first data track: the file holding it,
// where it starts, its sector size and where in a sector its 2048 user
// bytes are.
func cueDataTrack(p string) (bin string, base int64, sectorSize, dataOff int, ok bool) {
	data, err := os.ReadFile(p)
	if err != nil {
		return "", 0, 0, 0, false
	}
	sheet, err := cue.Parse(data)
	if err != nil {
		return "", 0, 0, 0, false
	}
	for _, f := range sheet.Files {
		for _, t := range f.Tracks {
			if t.Mode == "AUDIO" || len(t.Indexes) == 0 {
				continue
			}
			size, off := 2048, 0
			switch t.Mode {
			case "MODE1/2352":
				size, off = 2352, 16
			case "MODE2/2352":
				size, off = 2352, 24
			case "MODE2/2336":
				size, off = 2336, 8
			}
			start := int64(t.Indexes[len(t.Indexes)-1].Frames)
			for _, ix := range t.Indexes {
				if ix.Number == 1 {
					start = int64(ix.Frames)
				}
			}
			bin := filepath.Join(filepath.Dir(p), filepath.FromSlash(strings.ReplaceAll(f.Name, `\`, "/")))
			return bin, start * int64(size), size, off, true
		}
	}
	return "", 0, 0, 0, false
}

// openDisc opens the ISO 9660 file system in the image file p, whose data
// sectors are sectorSize bytes from base with 2048 user bytes at dataOff.
func openDisc(p string, base int64, sectorSize, dataOff int) (*iso9660.Image, *os.File, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, nil, err
	}
	var r io.ReaderAt = f
	if sectorSize != 2048 || base != 0 {
		r = &sectors{f: f, base: base, size: int64(sectorSize), off: int64(dataOff)}
	}
	img, err := iso9660.Open(r)
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	return img, f, nil
}

// Disc is a disc image in a source directory, its first data track's
// file system open.
type Disc struct {
	Path  string // slash-separated, relative to the source: the .iso or .cue
	Image *iso9660.Image
	f     *os.File
}

// Close closes the image file.
func (d *Disc) Close() error { return d.f.Close() }

// Discs opens every ISO 9660 disc image in dir: an .iso, or a CUE sheet's
// first data track. The caller closes them.
func Discs(dir string) []*Disc {
	var out []*Disc
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		var img *iso9660.Image
		var f *os.File
		switch strings.ToLower(filepath.Ext(p)) {
		case ".iso":
			img, f, err = openDisc(p, 0, 2048, 0)
		case ".cue":
			bin, base, size, off, ok := cueDataTrack(p)
			if !ok {
				return nil
			}
			img, f, err = openDisc(bin, base, size, off)
		default:
			return nil
		}
		if err == nil {
			out = append(out, &Disc{Path: filepath.ToSlash(rel), Image: img, f: f})
		}
		return nil
	})
	return out
}

// scanDisc walks the ISO 9660 file system in the image file p, whose data
// sectors are sectorSize bytes from base with 2048 user bytes at dataOff.
func scanDisc(rel, p string, base int64, sectorSize, dataOff int) []Program {
	img, f, err := openDisc(p, base, sectorSize, dataOff)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []Program
	var walk func(dir iso9660.Entry, prefix string, depth int)
	walk = func(dir iso9660.Entry, prefix string, depth int) {
		if depth > 16 {
			return
		}
		entries, err := img.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			name := prefix + e.Name
			if e.IsDir {
				walk(e, name+"/", depth+1)
				continue
			}
			if !IsProgramName(e.Name) {
				continue
			}
			rd, err := img.OpenFile(e)
			if err != nil {
				continue
			}
			if t := Classify(e.Name, rd); t.Platform != NotProgram {
				out = append(out, Program{rel + ":" + name, t})
			}
		}
	}
	walk(img.Root(), "", 0)
	return out
}

// sectors reads a raw CD track's 2048-byte user data as one stream.
type sectors struct {
	f               io.ReaderAt
	base, size, off int64
}

func (s *sectors) ReadAt(p []byte, off int64) (int, error) {
	n := 0
	for n < len(p) {
		sec, in := (off+int64(n))/2048, (off+int64(n))%2048
		chunk := min(int64(len(p)-n), 2048-in)
		m, err := s.f.ReadAt(p[n:n+int(chunk)], s.base+sec*s.size+s.off+in)
		n += m
		if err != nil {
			return n, err
		}
	}
	return n, nil
}
