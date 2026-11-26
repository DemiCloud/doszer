package gui

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/DemiCloud/doszer/internal/config"
	"github.com/DemiCloud/doszer/internal/env"
	"github.com/DemiCloud/doszer/internal/install"
	"github.com/DemiCloud/doszer/internal/library"
)

// systemFile is a shell or OS image in the system directory, or one that
// games boot and the directory lacks.
type systemFile struct {
	install.SystemFile
	kind, note string   // fixed VHD, dynamic VHD, raw image, shell; doszer's note on it
	missing    bool     // games boot it, the system directory hasn't it
	users      []string // names of the games that boot it
}

type sysResult struct {
	dir   string
	files []systemFile
}

// systems is the Systems page's state.
type systems struct {
	files       []systemFile // nil until scanned
	sysScanning bool
	sysCh       chan sysResult
	sysSel      int
	systemsH    int32
}

func (a *app) initSystems() {
	a.sysCh = make(chan sysResult, 1)
}

// rescanSystems lists the system directory in the background.
func (a *app) rescanSystems() {
	if a.sysScanning {
		return
	}
	dir := a.sysDir.Path
	if dir == "" {
		a.systemsScanned(sysResult{})
		return
	}
	a.sysScanning = true
	go func() {
		var out []systemFile
		for _, f := range install.SystemFiles(dir) {
			s := systemFile{SystemFile: f, kind: "shell"}
			if !f.Shell {
				s.kind, s.note = env.ImageKind(filepath.Join(dir, filepath.FromSlash(f.File)))
			}
			out = append(out, s)
		}
		a.sysCh <- sysResult{dir, out}
	}()
}

func (a *app) systemsScanned(r sysResult) {
	a.sysScanning = false
	if r.dir != a.sysDir.Path {
		a.rescanSystems() // the directory changed while scanning
		return
	}
	files := r.files
	if files == nil {
		files = []systemFile{}
	}
	// Which games boot each, and those no file matches. DOSBox Pure
	// matches boot.os and boot.shell exactly (dosbox_pure_run.h,
	// ReadAutoBoot).
	index := map[string]int{}
	for i, f := range files {
		index[key(f.Name, f.Shell)] = i
	}
	for _, g := range a.list {
		if g.Boot == "" {
			continue
		}
		shell := g.Kind == library.KindShell
		i, ok := index[key(g.Boot, shell)]
		if !ok {
			files = append(files, systemFile{SystemFile: install.SystemFile{Name: g.Boot, Shell: shell}, missing: true})
			i = len(files) - 1
			index[key(g.Boot, shell)] = i
		}
		files[i].users = append(files[i].users, g.Name)
	}
	sort.SliceStable(files, func(i, j int) bool {
		x, y := files[i], files[j]
		if x.missing != y.missing {
			return y.missing
		}
		if x.Shell != y.Shell {
			return y.Shell
		}
		return strings.ToLower(x.Name) < strings.ToLower(y.Name)
	})
	a.files = files
	a.sysSel = min(a.sysSel, len(files)-1)
	a.dirty = true
}

func key(name string, shell bool) string {
	if shell {
		return "S*" + name
	}
	return "O*" + name
}

// hasSystem reports whether the system directory has the shell or image.
func (a *app) hasSystem(name string, shell bool) bool {
	for _, f := range a.files {
		if !f.missing && f.Shell == shell && f.Name == name {
			return true
		}
	}
	return false
}

func (a *app) systemsToolbar(x, y int32) {
	if a.sysDir.Path != "" && canOpen() {
		x += a.button("Open folder", x, y, btnNormal, func() { a.open(a.sysDir.Path) }) + a.px(4)
	}
	a.button("Refresh", x, y, btnNormal, func() { a.rescanSystems() })
}

func (a *app) systemsStatus() string {
	if a.sysDir.Path == "" {
		return "No system folder"
	}
	var shells, images, missing int
	for _, f := range a.files {
		switch {
		case f.missing:
			missing++
		case f.Shell:
			shells++
		default:
			images++
		}
	}
	s := plural(images, "OS image") + ", " + plural(shells, "shell")
	if missing > 0 {
		s += ", " + plural(missing, "missing system")
	}
	return s + "  |  " + a.sysDir.Path + " (" + a.sysDir.From + ")"
}

func (a *app) systemsKey(sc int32) {
	switch sc {
	case scUp:
		a.sysSel = max(0, a.sysSel-1)
	case scDown:
		a.sysSel = min(len(a.files)-1, a.sysSel+1)
	}
}

func (a *app) drawSystems() {
	if a.sysDir.Path == "" {
		a.emptyMessage("No system folder", "DOSBox Pure finds OS images (.img, .vhd) and shells (.dosz) there.", "Choose one in Settings, or create a system folder beside doszer.")
		a.centerButton("Open Settings", func() { a.show(pageSettings) })
		a.systemsH = 0
		return
	}
	if a.files == nil {
		a.emptyMessage("Scanning", a.sysDir.Path)
		return
	}
	ax, ay, aw, _ := a.area()
	p := a.pad()
	y := ay + p - a.scroll[pageSystems]
	section := ""
	rowH := a.px(76)
	if len(a.files) == 0 {
		y = a.heading("Nothing here yet", ax+p, y)
		a.text("Put OS images (.img, .vhd of at least 7 MB) and Windows 3.x shells (.dosz) in "+a.sysDir.Path+".", ax+p, y, 13, false, colMuted)
		y += a.px(30)
	}
	for i := range a.files {
		f := &a.files[i]
		s := "OS images"
		switch {
		case f.missing:
			s = "Missing: games boot these, the system folder hasn't them"
		case f.Shell:
			s = "Shells"
		}
		if s != section {
			if section != "" {
				y += a.px(10)
			}
			section = s
			y = a.heading(s, ax+p, y)
		}
		a.drawSystemRow(f, i, ax+p, y, aw-2*p, rowH)
		y += rowH + a.rowGap()
	}
	a.systemsH = y + a.scroll[pageSystems] - ay + p
}

func (a *app) drawSystemRow(f *systemFile, i int, x, y, w, h int32) {
	sel := i == a.sysSel
	a.card(x, y, w, h, sel, a.hovered(x, y, w, h))
	a.clipHit(x, y, w, h, func(bool) { a.sysSel = i })

	c := colOS
	if f.Shell {
		c = colShell
	}
	if f.missing {
		c = colError
	}
	is := a.px(48)
	a.initialTile(filepath.Base(f.Name), c, x+a.px(14), y+(h-is)/2, is)

	tx := x + a.px(14) + is + a.px(14)
	right := a.px(260)
	tw := w - (tx - x) - right
	ty := y + a.px(12)
	a.text(a.ellipsize(f.Name, tw, 15, true), tx, ty, 15, true, colText)
	ty += a.lineHeight(15, true) + a.px(6)

	bx := tx
	switch {
	case f.missing:
		bx += a.badge("not found", colError, bx, ty) + a.px(6)
	default:
		bx += a.badge(f.kind+", "+size(f.Size), c, bx, ty) + a.px(6)
	}
	if !f.Shell && f.Name == a.cfg.DefaultOS {
		bx += a.badge("default OS", colAccent, bx, ty) + a.px(6)
	}
	if f.Shell && f.Name == a.cfg.DefaultShell {
		bx += a.badge("default shell", colAccent, bx, ty) + a.px(6)
	}
	for k, v := range a.cfg.InstallOS {
		if !f.Shell && v == f.Name {
			bx += a.badge("install image for "+filepath.Base(k), colShell, bx, ty) + a.px(6)
		}
	}
	if f.note != "" {
		a.text(a.ellipsize(f.note, tx+tw-bx, 12, false), bx, ty+a.px(3), 12, false, colMuted)
	}

	// Right: who uses it, and on the selected row, its action.
	rx := x + w - a.px(16)
	used := "Used by no games"
	switch n := len(f.users); {
	case n == 1:
		used = "Used by " + f.users[0]
	case n > 1:
		used = "Used by " + plural(n, "game")
	}
	a.textRight(a.ellipsize(used, right-a.px(20), 13, false), rx, y+a.px(14), 13, false, colMuted)
	if len(f.users) > 1 {
		a.textRight(a.ellipsize(strings.Join(f.users, ", "), right-a.px(20), 12, false), rx, y+a.px(34), 12, false, colFaint)
	}
	if !sel || f.missing {
		return
	}
	k, cur, label := "default_os", a.cfg.DefaultOS, "Set as default OS"
	if f.Shell {
		k, cur, label = "default_shell", a.cfg.DefaultShell, "Set as default shell"
	}
	if cur == f.Name {
		return
	}
	bw := a.buttonW(label)
	name := f.Name
	a.button(label, rx-bw, y+h-a.px(40), btnAccent, func() {
		a.whenSetUp(func() { a.saveKey(k, name) })
	})
}

// saveKey sets one setting in the user config file and reloads.
func (a *app) saveKey(k, v string) {
	p, err := config.UserConfigPath()
	if err == nil {
		if v == "" {
			err = config.Unset(p, k)
		} else {
			err = config.Set(p, k, v)
		}
	}
	if err != nil {
		a.say(err.Error(), true)
		return
	}
	a.say("Saved "+k+" to "+p, false)
	a.reload()
}
