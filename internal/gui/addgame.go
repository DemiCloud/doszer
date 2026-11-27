package gui

import (
	"github.com/DemiCloud/doszer/internal/repack"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/DemiCloud/doszer/internal/config"
	"github.com/DemiCloud/doszer/internal/install"
	"github.com/DemiCloud/doszer/internal/manifest"
	"github.com/DemiCloud/doszer/internal/platform"
	"github.com/DemiCloud/doszer/internal/start"
)

// addGame is Build > Import a disc and Build a folder: a disc in a CD drive, disc image
// files, made into an archive in a ROM folder by the commands a person
// would type - doszer dump (or dump --from), init and build - run one
// after the other in a dialog.
type addGame struct {
	page     int // 0: source, 1: options, 2: start program, 3: folder
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
	install  bool // run its setup in DOSBox Pure, then choose the program it starts
	flatten  bool // collapse single-folder chains (C:\VENDOR\GAME to C:\GAME): build --flatten

	// Build a folder: game files already on disk, built where they are.
	useFolder   bool
	folder      field
	hasManifest bool // it has a DOSZ.TOML: init isn't run
	kind        int  // Build a folder: 0 a game (.dosz), 1 a parent (.dosp), files games share

	// Once installed: choosing the program the game starts with.
	plan       *addPlan
	candidates []start.Candidate
	choice     int
	scroll     int            // the report's lines scrolled up from its end
	windows9x  bool           // the game boots an OS image: start --display applies
	retry      *install.Retry // page 3: install's changes span these folders
	colors256  bool           // start --display 640x480x8
}

// addPlan is where a game being added goes.
type addPlan struct {
	name, src, out string
	temp, keep     bool
	install        []step   // the install step, to run again
	report         []string // install's output
	folder         bool     // Build a folder, not Import a disc
	result         string   // the install step's install.ResultEnv file
	flatten        bool     // build --flatten
}

var bootChoices = []struct{ label, flag string }{
	{"Auto", ""},
	{"DOS", "dos"},
	{"Windows 3.x", "shell"},
	{"Windows 9x", "os"},
}

// romSubfolders are the folders a ROM folder's games usually sort into
// (RetroDECK's and ES-DE's systems for DOSBox Pure).
var romSubfolders = map[string]bool{"dos": true, "pc": true, "windows": true, "windows3x": true, "windows9x": true}

// openAddGame starts the flow.
func (a *app) openAddGame() {
	if len(a.romDirs) == 0 {
		a.alert("No ROM folder", "Choose where games go first: Tools, Settings, Games (ROMs).")
		return
	}
	g := &addGame{drives: opticalDrives(), discs: 1, keep: true, install: true, flatten: true}
	if !haveFFmpeg() {
		g.audio = 1 // WAV: Ogg Vorbis is encoded by ffmpeg
	}
	g.useFiles = len(g.drives) == 0
	for i, d := range g.drives {
		if d.disc {
			g.drive = i
			break
		}
	}
	g.files = []*field{{placeholder: "A .cue, a .zip holding one, or a disk image"}}
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

// openBuildFolder starts the flow on a folder of the game's files.
func (a *app) openBuildFolder() {
	a.openAddGame()
	if g := a.add; g != nil {
		g.useFolder, g.useFiles = true, false
		g.folder.placeholder = "The folder with the game's files (and its discs)"
		g.install = false
		a.focus = &g.folder
	}
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
	if g.useFolder {
		dir := strings.TrimSpace(g.folder.text)
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			a.say("Choose the game's folder first", true)
			return
		}
		g.hasManifest = false
		name := filepath.Base(dir)
		if m, err := manifest.Load(dir); err == nil {
			g.hasManifest, name = true, m.Name
		}
		if !g.named {
			g.name.set(name)
		}
		// A folder of only discs needs installing; an installed game not.
		g.install = !hasInstalledPrograms(dir)
		g.named, g.page = false, 1
		a.focus = &g.name
		return
	}
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
	temp := srcs.Path == "" && !g.useFolder
	src := filepath.Join(srcs.Path, name)
	if g.useFolder {
		src = strings.TrimSpace(g.folder.text)
	}
	if temp {
		dir, err := os.MkdirTemp("", "doszer-source-")
		if err != nil {
			a.say(err.Error(), true)
			return
		}
		src = filepath.Join(dir, name)
	}
	out := filepath.Join(g.dests[g.dest], name+g.ext())

	var steps []step
	switch {
	case g.useFolder:
		// Built where it is: nothing to dump.
	case g.useFiles:
		for _, f := range g.files {
			steps = append(steps, step{args: []string{"dump", "--format", "plain", "--from", strings.TrimSpace(f.text), src}})
		}
	default:
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
	if !g.hasManifest {
		steps = append(steps, step{args: append(initArgs, src)})
	}
	plan := &addPlan{name: g.name.text, src: src, out: out, temp: temp, keep: (g.keep || g.useFolder) && !temp, folder: g.useFolder, flatten: g.flatten}
	if !g.install {
		// An installed game in a folder: choose (or change) what it starts
		// with before building.
		if g.useFolder && hasInstalledPrograms(src) {
			a.add = nil
			if len(steps) == 0 {
				a.chooseStart(plan)
				return
			}
			a.runSteps("Build "+plan.name, steps, func(err error) {
				if err == nil {
					a.chooseStart(plan)
				}
			})
			return
		}
		a.add = nil
		a.runSteps("Build "+plan.name, append(steps, buildStep(plan)), a.added(plan))
		return
	}
	installArgs := []string{"install", "--format", "plain"}
	if a.o.SystemDir != "" {
		installArgs = append(installArgs, "--system-dir", a.o.SystemDir)
	}
	if f, err := os.CreateTemp("", "doszer-install-*.json"); err == nil {
		f.Close()
		plan.result = f.Name()
	}
	steps = append(steps,
		step{prompt: "Next, DOSBox Pure opens with the game's disc. Run its setup (SETUP.EXE or INSTALL.EXE: in Windows, from the CD drive), install the game, then close DOSBox Pure's window."},
		step{args: append(installArgs, src), env: []string{install.ResultEnv + "=" + plan.result}},
	)
	plan.install = steps[len(steps)-2:]
	a.add = nil
	a.runSteps("Build "+plan.name, steps, a.installed(plan))
}

// installed ends an install run: its output stays up, read before Next
// moves on to choosing the program. Changes spanning folders none of
// which stands out go to choosing the folder.
func (a *app) installed(plan *addPlan) func(error) {
	return func(err error) {
		if err != nil {
			if r := install.ReadRetry(plan.result); r != nil {
				a.add = &addGame{page: 3, plan: plan, retry: r, dests: []string{filepath.Dir(plan.out)}, useFolder: plan.folder}
				a.dialog = nil
				return
			}
			a.say("Kept in "+plan.src, true)
			return
		}
		os.Remove(plan.result)
		if d := a.dialog; d != nil && d.job != nil {
			plan.report = d.job.lines()
			d.buttons = []dialogButton{a.copyAll(d), {label: "Next", style: btnAccent, fn: func() { a.chooseStart(plan) }}}
		} else {
			a.chooseStart(plan)
		}
	}
}

// addGameExtract extracts the chosen folder from the install session's
// kept save (install --from), then goes on as after the session.
func (a *app) addGameExtract() {
	g := a.add
	p, r := g.plan, g.retry
	args := []string{"install", "--format", "plain", "--from", r.From}
	if r.Content != "" {
		args = append(args, "--content", r.Content)
	}
	if a.o.SystemDir != "" {
		args = append(args, "--system-dir", a.o.SystemDir)
	}
	args = append(args, "--dir", r.Folders[g.choice].Name, p.src)
	os.Remove(p.result) // a failure now is the extraction's, not the folder's
	a.add = nil
	next := a.installed(p)
	a.runSteps("Build "+p.name, []step{{args: args}}, func(err error) {
		if err == nil {
			os.Remove(r.From)
			if r.Content != "" {
				os.RemoveAll(filepath.Dir(r.Content))
			}
		}
		next(err)
	})
}

// buildStep builds plan's archive.
func buildStep(p *addPlan) step {
	args := []string{"build", "--format", "plain"}
	if p.flatten {
		args = append(args, "--flatten")
	}
	return step{args: append(args, p.src, p.out)}
}

// added is what happens once a game is built: its dump removed unless
// kept, the list read again with the new archive selected.
func (a *app) added(p *addPlan) func(error) {
	return func(err error) {
		if err != nil {
			return
		}
		if !p.keep {
			if p.temp {
				os.RemoveAll(filepath.Dir(p.src))
			} else {
				os.RemoveAll(p.src)
			}
		}
		if strings.EqualFold(filepath.Ext(p.out), repack.ExtParent) {
			a.show(pageParents)
		} else {
			a.selectAfterScan = p.out
		}
		a.rescan()
		a.say("Built "+filepath.Base(p.out), false)
	}
}

// chooseStart asks which installed program starts the game (the list
// `doszer start SOURCE` prints), then sets it and builds.
func (a *app) chooseStart(p *addPlan) {
	cs, m, err := start.Candidates(p.src)
	if err != nil {
		a.alert("Can't read the installed game", err.Error())
		return
	}
	g := &addGame{page: 2, plan: p, candidates: cs, dests: []string{filepath.Dir(p.out)}, useFolder: p.folder,
		windows9x: m.Model() == manifest.ModelOS}
	for i, c := range cs {
		if c.Current {
			g.choice = i
			break
		}
		if c.Likely {
			g.choice = i
		}
	}
	a.add, a.dialog = g, nil
}

// addGameFinish sets the chosen program (doszer start) and builds.
func (a *app) addGameFinish(set bool) {
	g := a.add
	p := g.plan
	var steps []step
	if set && len(g.candidates) > 0 {
		args := []string{"start", "--format", "plain", "--force"}
		if g.windows9x && g.colors256 {
			args = append(args, "--display", "640x480x8")
		}
		steps = append(steps, step{args: append(args, p.src, g.candidates[g.choice].Path)})
	}
	steps = append(steps, buildStep(p))
	a.add = nil
	a.runSteps("Build "+p.name, steps, a.added(p))
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
	what := "Import a disc"
	if g.useFolder {
		what = "Build a folder"
	}
	title := what + ": where is it?"
	switch g.page {
	case 1:
		title = what + ": how should it be built?"
	case 2:
		title = what + ": which program starts it?"
	case 3:
		title = what + ": which folder is the game?"
	}
	a.text(title, x+p, ty, 19, true, colText)
	ty += a.lineHeight(19, true) + a.px(16)
	cx, cw := x+p, w-2*p

	switch g.page {
	case 0:
		ty = a.addGameSource(g, cx, ty, cw)
	case 1:
		ty = a.addGameOptions(g, cx, ty, cw)
	case 2:
		ty = a.addGameStart(g, cx, ty, cw, y+h-p-a.px(48))
	case 3:
		ty = a.addGameFolder(g, cx, ty, cw, y+h-p-a.px(48))
	}

	// Buttons.
	by := y + h - p - a.px(32)
	bx := x + w - p
	place := func(label string, style int, fn func()) {
		bx -= a.buttonW(label)
		a.button(label, bx, by, style, fn)
		bx -= a.px(8)
	}
	switch g.page {
	case 0:
		place("Next", btnAccent, a.addGameNext)
	case 1:
		place("Build", btnAccent, a.addGameRun)
		place("Back", btnNormal, func() { g.page = 0; a.focus = nil })
	case 2:
		if g.windows9x && len(g.candidates) > 0 {
			a.checkbox(x+p, by, g.colors256, "256 colours", func() { g.colors256 = !g.colors256 })
		}
		if len(g.candidates) > 0 {
			place("Build", btnAccent, func() { a.addGameFinish(true) })
			place("Build without", btnNormal, func() { a.addGameFinish(false) })
		} else {
			p := g.plan
			place("Install again", btnAccent, func() {
				a.add = nil
				a.runSteps("Build "+p.name, p.install, a.installed(p))
			})
			place("Build anyway", btnNormal, func() { a.addGameFinish(false) })
		}
	}
	if g.page == 3 {
		place("Extract", btnAccent, a.addGameExtract)
	}
	place("Cancel", btnNormal, func() {
		if g.retry != nil {
			a.say("Save kept: "+g.retry.From, false)
		} else if g.plan != nil {
			a.say("The installed game is kept in "+g.plan.src, false)
		}
		a.add = nil
	})
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
	if g.useFolder {
		y = a.heading("The game's folder", x, y)
		bw := int32(0)
		if canPick() {
			bw = a.buttonW("Browse…") + a.px(8)
		}
		a.drawField(&g.folder, x, y, w-bw)
		if bw > 0 {
			a.button("Browse…", x+w-bw+a.px(8), y, btnNormal, func() {
				// From the sources folder, where imported discs are.
				start := strings.TrimSpace(g.folder.text)
				if start == "" {
					start = config.SourcesDirOf(a.cfg).Path
				}
				if p, ok := pickFolder("game's", start); ok {
					g.folder.set(p)
				}
			})
		}
		y += a.px(40)
		for _, l := range a.wrap("Built in place; doszer init adds a DOSZ.TOML if it has none.", w, 12) {
			a.text(l, x, y, 12, false, colFaint)
			y += a.lineHeight(12, false) + a.px(4)
		}
		return y
	}
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
	if len(g.drives) > 0 {
		// Always there, so choosing files doesn't move what's below;
		// greyed while files are chosen.
		on := !g.useFiles
		tc := colText
		var less, more func()
		if on {
			less, more = func() { g.discs = max(1, g.discs-1) }, func() { g.discs = min(9, g.discs+1) }
		} else {
			tc = colFaint
		}
		a.text("Discs", x+a.px(14), y+a.px(7), 13, false, tc)
		sx := x + a.px(70)
		sx += a.button("−", sx, y, btnNormal, less) + a.px(8)
		a.text(itoa(g.discs), sx+a.px(6), y+a.px(7), 13, true, tc)
		sx += a.px(28)
		a.button("+", sx, y, btnNormal, more)
		y += a.px(40)
	}
	y += a.px(8)
	y = a.heading("From disc image files", x, y)
	y = a.option(x, y, w, g.useFiles, "Image files", "A disc's .cue (with its .bin files) or a .zip holding one, or a floppy or disk image; one per disc", func() {
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
			idx := i
			a.button("Browse…", x+w-bw+a.px(8), y, btnNormal, func() {
				// One or several: a multi-disc game's discs at once.
				if paths := pickFiles("Choose the discs", fileDisc); len(paths) > 0 {
					g.setFiles(idx, paths)
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

// setFiles puts paths in the disc list: the first in field i, the rest
// added, then the list sorted by name (Disc 1, Disc 2, ... Disc 10).
func (g *addGame) setFiles(i int, paths []string) {
	g.files[i].set(paths[0])
	for _, p := range paths[1:] {
		f := &field{}
		f.set(p)
		g.files = append(g.files, f)
	}
	g.sortFiles()
}

// addFile adds a disc (a drop), into an empty field if there is one.
func (g *addGame) addFile(p string) {
	for _, f := range g.files {
		if strings.TrimSpace(f.text) == "" {
			f.set(p)
			g.sortFiles()
			return
		}
	}
	f := &field{}
	f.set(p)
	g.files = append(g.files, f)
	g.sortFiles()
}

// sortFiles orders the discs by file name, numbers by value, empty
// fields last, dropping duplicates.
func (g *addGame) sortFiles() {
	seen := map[string]bool{}
	var full, empty []*field
	for _, f := range g.files {
		t := strings.TrimSpace(f.text)
		switch {
		case t == "":
			empty = append(empty, f)
		case !seen[strings.ToLower(t)]:
			seen[strings.ToLower(t)] = true
			full = append(full, f)
		}
	}
	sort.SliceStable(full, func(i, j int) bool {
		return naturalLess(filepath.Base(full[i].text), filepath.Base(full[j].text))
	})
	if len(full) > 0 && len(empty) > 0 {
		empty = empty[:0]
	}
	g.files = append(full, empty...)
}

// naturalLess compares names with digit runs by value: "Disc 2" before
// "Disc 10".
func naturalLess(a, b string) bool {
	a, b = strings.ToLower(a), strings.ToLower(b)
	for a != "" && b != "" {
		da, db := digits(a), digits(b)
		if da != "" && db != "" {
			na, nb := strings.TrimLeft(da, "0"), strings.TrimLeft(db, "0")
			if len(na) != len(nb) {
				return len(na) < len(nb)
			}
			if na != nb {
				return na < nb
			}
			a, b = a[len(da):], b[len(db):]
			continue
		}
		if a[0] != b[0] {
			return a[0] < b[0]
		}
		a, b = a[1:], b[1:]
	}
	return len(a) < len(b)
}

func digits(s string) string {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return s[:i]
}

func (a *app) addGameOptions(g *addGame, x, y, w int32) int32 {
	labelW := a.px(150)
	fx, fw := x+labelW, w-labelW
	row := func(label string) {
		a.text(label, x, y+a.px(7), 13, false, colText)
	}
	note := func(s string, c rgba) { // wraps: notes are mostly paths
		for _, l := range a.wrapAny(s, fw, 12) {
			a.text(l, fx, y, 12, false, c)
			y += a.lineHeight(12, false) + a.px(2)
		}
		y += a.px(10)
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
	y += a.px(44)

	if g.hasManifest {
		row("Settings")
		a.text("From its DOSZ.TOML", fx, y+a.px(7), 13, false, colText)
		y += a.px(32)

	} else {
		row("OS")
		var boots []string
		for _, b := range bootChoices {
			boots = append(boots, b.label)
		}
		dropdown(boots, &g.boot)
		y += a.px(6)

		row("CD audio")
		ogg := "Ogg"
		if !haveFFmpeg() {
			ogg = "Ogg (needs ffmpeg)"
		}
		dropdown([]string{ogg, "WAV (lossless)"}, &g.audio)
		y += a.px(4)
	}

	row("ROM folder")
	dests := make([]string, len(g.dests))
	copy(dests, g.dests)
	dropdown(dests, &g.dest)
	if g.useFolder {
		row("Type")
		dropdown([]string{"Game (.dosz)", "Parent (.dosp)"}, &g.kind)
		y += a.px(4)
	}

	out := filepath.Join(g.dests[g.dest], safeName(g.name.text)+g.ext())
	if _, err := os.Stat(out); err == nil && safeName(g.name.text) != "" {
		note("Replaces "+filepath.Base(out), colShell)
	} else {
		note(filepath.Base(out), colFaint)
	}

	row("Install first")
	a.checkbox(fx, y, g.install, "", func() { g.install = !g.install })
	y += a.px(44)

	row("Flatten folders")
	a.checkbox(fx, y, g.flatten, "", func() { g.flatten = !g.flatten })
	y += a.px(44)

	srcs := config.SourcesDirOf(a.cfg)
	if g.useFolder {
		return y
	}
	row("Keep source files")
	if srcs.Path == "" {
		a.text("Not kept (doszer isn't set up)", fx, y+a.px(7), 13, false, colMuted)
		y += a.px(38)
	} else {
		a.checkbox(fx, y, g.keep, "", func() { g.keep = !g.keep })
		y += a.px(38)
		note(filepath.Join(srcs.Path, safeName(g.name.text)), colFaint)
	}
	return y
}

// addGameStart lists the installed programs to choose from.
func (a *app) addGameStart(g *addGame, x, y, w, bottom int32) int32 {
	if len(g.candidates) == 0 {
		a.text("No programs found", x, y, 14, true, colText)
		y += a.px(28)
		// install's report says why: what changed where, what it skipped.
		report := g.plan.report
		lh := a.lineHeight(12, false) + a.px(3)
		bh := bottom - y
		a.round(x, y, w, bh, a.px(6), colBackground)
		rows := max(1, int((bh-a.px(12))/lh))
		g.scroll = max(0, min(g.scroll, len(report)-rows))
		end := len(report) - g.scroll
		start := max(0, end-rows)
		ly := y + a.px(6)
		a.pushClip(x, y, w, bh)
		a.selRegion("report", report)
		a.scrollbar(x+w-a.px(8), y+a.px(4), bh-a.px(8), start, rows, len(report))
		for i, l := range report[start:end] {
			a.selTextLine("report", start+i, x+a.px(4), ly, w-a.px(8), lh, l, x+a.px(10), faceKey{px: int(a.px(12)), mono: true})
			a.monoText(l, x+a.px(10), ly, colText)
			ly += lh
		}
		a.popClip()
		return bottom
	}
	for _, l := range a.wrap("Installed in "+g.plan.src+":", w, 13) {
		a.text(l, x, y, 13, false, colMuted)
		y += a.lineHeight(13, false) + a.px(4)
	}
	y += a.px(8)
	a.pushClip(x, y, w, bottom-y)
	defer a.popClip()
	for i, c := range g.candidates {
		detail := c.Target
		if c.Runs != "" {
			detail += ", runs " + c.Runs
		}
		if c.Likely {
			detail += " (likeliest)"
		}
		idx := i
		y = a.option(x, y, w, g.choice == i, c.Path, detail, func() { g.choice = idx })
	}
	return y
}

// addGameFolder lists the folders install's changes span, the biggest
// first (and chosen).
func (a *app) addGameFolder(g *addGame, x, y, w, bottom int32) int32 {
	a.pushClip(x, y, w, bottom-y)
	defer a.popClip()
	for i, f := range g.retry.Folders {
		idx := i
		detail := itoa(f.Files) + " files, " + size(f.Bytes)
		y = a.option(x, y, w, g.choice == i, g.retry.Drive+`\`+f.Name, detail, func() { g.choice = idx })
	}
	return y
}

// haveFFmpeg reports whether ffmpeg, which encodes Ogg Vorbis, is found.
func haveFFmpeg() bool {
	_, err := exec.LookPath("ffmpeg")
	return err == nil
}

// hasInstalledPrograms reports whether dir holds programs outside its
// disc images: a game installed already.
func hasInstalledPrograms(dir string) bool {
	progs, _ := platform.Scan(dir)
	for _, p := range progs {
		if !strings.Contains(p.Path, ":") {
			return true
		}
	}
	return false
}

// ext is the archive's extension: a parent's .dosp or a game's .dosz.
func (g *addGame) ext() string {
	if g.kind == 1 {
		return repack.ExtParent
	}
	return repack.ExtGame
}
