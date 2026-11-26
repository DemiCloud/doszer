package gui

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/DemiCloud/doszer/internal/config"
)

// addGame is the Add a game flow: a disc in a CD drive, or disc image
// files, made into an archive in a ROM folder by the commands a person
// would type - doszer dump (or dump --from), init and build - run one
// after the other in a dialog.
type addGame struct {
	page     int // 0: source, 1: options
	drives   []drive
	useFiles bool
	drive    int
	discs    int
	files    []*field
	name     field
	boot     int // index into bootChoices
	audio    int // 0 Ogg Vorbis, 1 WAV
	dest     int // index into dests
	dests    []string
	keep     bool
	named    bool // the name was typed, not suggested
}

var bootChoices = []struct{ label, flag string }{
	{"Automatic (from the game's programs)", ""},
	{"DOS", "dos"},
	{"Windows 3.x shell", "shell"},
	{"Windows 9x OS image", "os"},
}

// romSubfolders are the folders a ROM folder's games usually sort into
// (RetroDECK's and ES-DE's systems for DOSBox Pure).
var romSubfolders = map[string]bool{"dos": true, "pc": true, "windows": true, "windows3x": true, "windows9x": true}

// openAddGame starts the flow.
func (a *app) openAddGame() {
	if len(a.romDirs) == 0 {
		a.alert("No ROM folder", "Choose where games go first: Settings, Games (ROMs).")
		return
	}
	g := &addGame{drives: opticalDrives(), discs: 1, keep: true}
	g.useFiles = len(g.drives) == 0
	for i, d := range g.drives {
		if d.disc {
			g.drive = i
			break
		}
	}
	g.files = []*field{{placeholder: "A .cue file, or a .zip holding one"}}
	g.name.placeholder = "The game's name"
	g.name.onChange = func() { g.named = true }
	// Where it can go: each ROM folder, and its system subfolders or any
	// that already hold archives.
	for _, d := range a.romDirs {
		g.dests = append(g.dests, d.Path)
		entries, _ := os.ReadDir(d.Path)
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			sub := filepath.Join(d.Path, e.Name())
			if romSubfolders[strings.ToLower(e.Name())] || holdsArchives(sub) {
				g.dests = append(g.dests, sub)
			}
		}
	}
	a.add, a.popup = g, nil
	a.focus = nil
}

func holdsArchives(dir string) bool {
	m, _ := filepath.Glob(filepath.Join(dir, "*.dosz"))
	return len(m) > 0
}

// suggestName is a name from a disc image's file name or a disc's label,
// without Redump's region and version tags: "Betrayal in Antara (USA)
// (Disc 1).cue" is Betrayal in Antara.
var tags = regexp.MustCompile(`\s*[\(\[][^\)\]]*[\)\]]`)

func suggestName(s string) string {
	s = strings.TrimSuffix(filepath.Base(s), filepath.Ext(s))
	s = strings.TrimSpace(tags.ReplaceAllString(s, ""))
	if s == strings.ToUpper(s) { // a disc label: ANTARA_1
		s = strings.TrimRight(strings.ReplaceAll(s, "_", " "), " 0123456789")
		if s != "" {
			s = strings.ToUpper(s[:1]) + strings.ToLower(s[1:])
		}
	}
	return s
}

// safeName makes name a file name on every system.
func safeName(name string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if strings.ContainsRune(`\/:*?"<>|`, r) || r < 32 {
			return -1
		}
		return r
	}, name))
}

func (a *app) addGameKey(sc int32) {
	switch sc {
	case scEscape:
		a.add = nil
	}
}

// addGameNext moves from the source page to the options.
func (a *app) addGameNext() {
	g := a.add
	if g.useFiles {
		var files []*field
		for _, f := range g.files {
			if strings.TrimSpace(f.text) != "" {
				files = append(files, f)
			}
		}
		if len(files) == 0 {
			a.say("Choose a disc image first", true)
			return
		}
		for _, f := range files {
			if _, err := os.Stat(strings.TrimSpace(f.text)); err != nil {
				a.say(err.Error(), true)
				return
			}
		}
		g.files = files
		if !g.named {
			g.name.set(suggestName(strings.TrimSpace(files[0].text)))
		}
	} else {
		if len(g.drives) == 0 {
			a.say("No CD drive found", true)
			return
		}
		if !g.named && g.drives[g.drive].label != "" && runtime.GOOS == "windows" {
			g.name.set(suggestName(g.drives[g.drive].label))
		}
	}
	g.named = false
	g.page = 1
	a.focus = &g.name
}

// addGameRun runs the commands.
func (a *app) addGameRun() {
	g := a.add
	name := safeName(g.name.text)
	if name == "" {
		a.say("The game needs a name", true)
		return
	}
	srcs := config.SourcesDirOf(a.cfg)
	temp := srcs.Path == ""
	src := filepath.Join(srcs.Path, name)
	if temp {
		dir, err := os.MkdirTemp("", "doszer-source-")
		if err != nil {
			a.say(err.Error(), true)
			return
		}
		src = filepath.Join(dir, name)
	}
	out := filepath.Join(g.dests[g.dest], name+".dosz")

	var steps []step
	if g.useFiles {
		for _, f := range g.files {
			steps = append(steps, step{args: []string{"dump", "--format", "plain", "--from", strings.TrimSpace(f.text), src}})
		}
	} else {
		d := g.drives[g.drive].path
		for i := 1; i <= g.discs; i++ {
			if i > 1 {
				steps = append(steps, step{prompt: "Put disc " + itoa(i) + " of " + itoa(g.discs) + " in " + d + ", then Continue."})
			}
			steps = append(steps, step{args: []string{"dump", "--format", "plain", "--drive", d, src}})
		}
	}
	initArgs := []string{"init", "--format", "plain", "--force", "--name", g.name.text}
	if b := bootChoices[g.boot].flag; b != "" {
		initArgs = append(initArgs, "--boot", b)
	}
	if g.audio == 1 {
		initArgs = append(initArgs, "--audio", "wav")
	}
	if a.o.SystemDir != "" {
		initArgs = append(initArgs, "--system-dir", a.o.SystemDir)
	}
	steps = append(steps,
		step{args: append(initArgs, src)},
		step{args: []string{"build", "--format", "plain", src, out}},
	)
	keep := g.keep && !temp
	a.add = nil
	a.runSteps("Add "+g.name.text, steps, func(err error) {
		if !keep && err == nil {
			if temp {
				os.RemoveAll(filepath.Dir(src))
			} else {
				os.RemoveAll(src)
			}
		}
		if err == nil {
			a.selectAfterScan = out
			a.rescan()
			a.say("Added "+filepath.Base(out), false)
		}
	})
}

func itoa(n int) string { return strconv.Itoa(n) }

func (a *app) drawAddGame() {
	g := a.add
	if g == nil {
		return
	}
	a.layer = layerModal
	defer func() { a.layer = layerPage }()
	a.fill(0, 0, a.w, a.h, rgba{0, 0, 0, 170})
	a.onClick(0, 0, a.w, a.h, func(bool) {})

	w := min(a.w-a.px(40), a.px(760))
	h := min(a.h-a.px(40), a.px(560))
	x, y := (a.w-w)/2, (a.h-h)/2
	a.round(x-a.px(1), y-a.px(1), w+a.px(2), h+a.px(2), a.px(14), colLine)
	a.round(x, y, w, h, a.px(14), colPopup)
	p := a.px(24)
	ty := y + p
	title := "Add a game: where is it?"
	if g.page == 1 {
		title = "Add a game: how should it be built?"
	}
	a.text(title, x+p, ty, 19, true, colText)
	ty += a.lineHeight(19, true) + a.px(16)
	cx, cw := x+p, w-2*p

	if g.page == 0 {
		ty = a.addGameSource(g, cx, ty, cw)
	} else {
		ty = a.addGameOptions(g, cx, ty, cw)
	}

	// Buttons.
	by := y + h - p - a.px(32)
	bx := x + w - p
	place := func(label string, style int, fn func()) {
		bx -= a.buttonW(label)
		a.button(label, bx, by, style, fn)
		bx -= a.px(8)
	}
	if g.page == 0 {
		place("Next", btnAccent, a.addGameNext)
	} else {
		place("Build", btnAccent, a.addGameRun)
		place("Back", btnNormal, func() { g.page = 0; a.focus = nil })
	}
	place("Cancel", btnNormal, func() { a.add = nil })
}

// option draws a selectable card and returns the y below it.
func (a *app) option(x, y, w int32, on bool, title, detail string, fn func()) int32 {
	h := a.px(54)
	bg := colCard
	if a.hovered(x, y, w, h) {
		bg = colCardHover
	}
	if on {
		a.outline(x, y, w, h, a.px(8), a.px(2), colAccent, colCardSel)
	} else {
		a.round(x, y, w, h, a.px(8), bg)
	}
	dot := "○"
	if on {
		dot = "●"
	}
	a.text(dot, x+a.px(14), y+a.px(9), 15, false, colAccent)
	a.text(a.ellipsize(title, w-a.px(60), 14, true), x+a.px(40), y+a.px(8), 14, true, colText)
	a.text(a.ellipsize(detail, w-a.px(60), 12, false), x+a.px(40), y+a.px(30), 12, false, colMuted)
	a.onClick(x, y, w, h, func(bool) { fn() })
	return y + h + a.px(8)
}

func (a *app) addGameSource(g *addGame, x, y, w int32) int32 {
	y = a.heading("From a CD drive", x, y)
	if len(g.drives) == 0 {
		a.text("No CD drive found.", x, y, 13, false, colMuted)
		y += a.px(28)
	}
	for i, d := range g.drives {
		detail := "No disc"
		switch {
		case d.disc && d.label != "":
			detail = d.label
		case d.disc:
			detail = "A disc (no label)"
		}
		idx := i
		y = a.option(x, y, w, !g.useFiles && g.drive == i, d.path, detail, func() {
			g.useFiles, g.drive = false, idx
		})
	}
	if !g.useFiles && len(g.drives) > 0 {
		a.text("Discs:", x+a.px(14), y+a.px(7), 13, false, colText)
		sx := x + a.px(70)
		sx += a.button("−", sx, y, btnNormal, func() { g.discs = max(1, g.discs-1) }) + a.px(8)
		a.text(itoa(g.discs), sx+a.px(6), y+a.px(7), 13, true, colText)
		sx += a.px(28)
		sx += a.button("+", sx, y, btnNormal, func() { g.discs = min(9, g.discs+1) }) + a.px(12)
		a.text("doszer asks for each disc in turn.", sx, y+a.px(8), 12, false, colFaint)
		y += a.px(40)
	}
	y += a.px(8)
	y = a.heading("From disc image files", x, y)
	y = a.option(x, y, w, g.useFiles, "CUE sheets", "A dumped disc's .cue (with its .bin files), or a .zip holding one; one per disc", func() {
		g.useFiles = true
	})
	if !g.useFiles {
		return y
	}
	for i, f := range g.files {
		bw := int32(0)
		if canPick() {
			bw = a.buttonW("Browse…") + a.px(8)
		}
		a.drawField(f, x, y, w-bw)
		if bw > 0 {
			ff := f
			a.button("Browse…", x+w-bw+a.px(8), y, btnNormal, func() {
				if p, ok := pickFile("Choose disc " + itoa(i+1)); ok {
					ff.set(p)
				}
			})
		}
		y += a.px(38)
	}
	a.button("Add another disc", x, y, btnGhost, func() {
		g.files = append(g.files, &field{placeholder: "Disc " + itoa(len(g.files)+1) + "'s .cue"})
		a.focus = g.files[len(g.files)-1]
	})
	return y + a.px(40)
}

func (a *app) addGameOptions(g *addGame, x, y, w int32) int32 {
	labelW := a.px(150)
	fx, fw := x+labelW, w-labelW
	row := func(label string) {
		a.text(label, x, y+a.px(7), 13, false, colText)
	}
	note := func(s string, c rgba) {
		a.text(a.ellipsize(s, fw, 12, false), fx, y, 12, false, c)
		y += a.lineHeight(12, false) + a.px(12)
	}
	dropdown := func(items []string, cur *int) {
		bx, by := fx, y
		label := items[*cur] + " ▼"
		a.button(label, bx, by, btnNormal, func() {
			a.openPopup(bx, by, a.buttonW(label), items, *cur, func(i int) { *cur = i })
		})
		y += a.px(38)
	}

	row("Name")
	a.drawField(&g.name, fx, y, fw)
	y += a.px(38)
	note("The archive's name, and its file name.", colFaint)

	row("Boots")
	var boots []string
	for _, b := range bootChoices {
		boots = append(boots, b.label)
	}
	dropdown(boots, &g.boot)
	note("init picks from the programs it finds, and the shells and images in the system folder.", colFaint)

	row("CD audio")
	dropdown([]string{"Ogg Vorbis (smaller)", "WAV (lossless, every track verifiable)"}, &g.audio)
	y += a.px(4)

	row("Put it in")
	dests := make([]string, len(g.dests))
	copy(dests, g.dests)
	dropdown(dests, &g.dest)
	out := filepath.Join(g.dests[g.dest], safeName(g.name.text)+".dosz")
	if _, err := os.Stat(out); err == nil && safeName(g.name.text) != "" {
		note("Replaces the existing "+filepath.Base(out)+".", colShell)
	} else {
		note(out, colFaint)
	}

	srcs := config.SourcesDirOf(a.cfg)
	row("The dump")
	if srcs.Path == "" {
		a.text("Not kept: doszer isn't set up, so it goes in a temporary folder.", fx, y+a.px(7), 13, false, colMuted)
		y += a.px(38)
	} else {
		box := "□  Keep it"
		if g.keep {
			box = "■  Keep it"
		}
		a.button(box, fx, y, btnGhost, func() { g.keep = !g.keep })
		y += a.px(38)
		note("In "+filepath.Join(srcs.Path, safeName(g.name.text))+": the BIN/CUE and DOSZ.TOML, to rebuild or change later.", colFaint)
	}
	return y
}
