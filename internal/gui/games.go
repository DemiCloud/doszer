package gui

import (
	"bytes"
	"errors"
	"image"
	"image/draw"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/DemiCloud/doszer/internal/config"
	"github.com/DemiCloud/doszer/internal/icon"
	"github.com/DemiCloud/doszer/internal/install"
	"github.com/DemiCloud/doszer/internal/library"
	"github.com/DemiCloud/doszer/internal/platform"
)

type sortMode int

const (
	sortName sortMode = iota
	sortLastPlayed
	sortPlayTime
	sortSize
	sortModes
)

func (s sortMode) String() string {
	return [...]string{"Name", "Last played", "Play time", "Size"}[s]
}

type scanResult struct {
	list []library.Game
	err  error
}

type runResult struct {
	path  string
	start time.Time
	took  time.Duration
	err   error
}

// games is the Games page's state.
type games struct {
	list     []library.Game
	stats    *library.Stats
	scanning bool
	scanErr  error
	scanCh   chan scanResult

	search field
	filter string // only games whose systemLabel is this, "" for all
	sort   sortMode
	grid   bool
	view   []int // indices into list: filtered and sorted
	sel    int   // index into view, -1 for none
}

func (a *app) initGames(st guiState) {
	a.scanCh = make(chan scanResult, 1)
	a.sel = -1
	a.grid = st.Grid
	for m := sortName; m < sortModes; m++ {
		if m.String() == st.Sort {
			a.sort = m
		}
	}
	a.search = field{placeholder: "Type to search…", nav: true, onChange: func() { a.refilter(a.keepPath()) }}
	a.focus = &a.search
}

// Select selects the n-th game shown (Options.Setup).
func (a *app) Select(n int) { a.sel = min(n, len(a.view)-1); a.reveal() }

// Search types q into the search field (Options.Setup).
func (a *app) Search(q string) { a.search.set(q); a.refilter(a.keepPath()) }

// Grid switches to the grid view (Options.Setup).
func (a *app) Grid(on bool) { a.grid = on; a.reveal() }

// Filter shows only the games booting system label (Options.Setup).
func (a *app) Filter(label string) { a.filter = label; a.refilter(a.keepPath()) }

// Show switches to a page: "games", "systems" or "settings" (Options.Setup).
func (a *app) Show(name string) {
	if name == "settings" {
		a.openSettings()
		return
	}
	a.show(map[string]page{"games": pageGames, "systems": pageSystems}[name])
}

// rescan lists the ROM directory in the background.
func (a *app) rescan() {
	if a.scanning {
		return
	}
	if len(a.romDirs) == 0 {
		a.list, a.view, a.scanErr = []library.Game{}, nil, nil
		return
	}
	a.scanning = true
	dirs, stats := config.DirPaths(a.romDirs), a.stats
	go func() {
		list, err := library.Scan(dirs, stats)
		a.scanCh <- scanResult{list, err}
	}()
}

func (a *app) scanned(r scanResult) {
	a.scanning = false
	a.scanErr = r.err
	keep := a.keepPath()
	for _, t := range a.icons {
		if t != nil {
			destroyTexture(t)
		}
	}
	a.icons = map[string]*texture{}
	a.list = r.list
	if a.list == nil {
		a.list = []library.Game{}
	}
	if a.selectAfterScan != "" {
		keep, a.selectAfterScan = a.selectAfterScan, ""
		a.search.set("")
		a.filter = ""
	}
	a.refilter(keep)
	a.rescanSystems() // which games use which system
}

// refilter recomputes the view after the games, query or sort changed,
// keeping keep (an archive path) selected if it is still shown.
func (a *app) refilter(keep string) {
	q := strings.ToLower(strings.TrimSpace(a.search.text))
	a.view = a.view[:0]
	for i, g := range a.list {
		if a.filter != "" && !a.matchesFilter(&g) {
			continue
		}
		if q == "" || matchesSearch(&g, q) {
			a.view = append(a.view, i)
		}
	}
	less := func(x, y *library.Game) bool {
		switch a.sort {
		case sortLastPlayed:
			if !x.LastPlayed.Equal(y.LastPlayed) {
				return x.LastPlayed.After(y.LastPlayed)
			}
		case sortPlayTime:
			if x.PlayTime != y.PlayTime {
				return x.PlayTime > y.PlayTime
			}
		case sortSize:
			if x.Size != y.Size {
				return x.Size > y.Size
			}
		}
		return strings.ToLower(x.Name) < strings.ToLower(y.Name)
	}
	sort.SliceStable(a.view, func(i, j int) bool { return less(&a.list[a.view[i]], &a.list[a.view[j]]) })
	a.sel = -1
	for i, gi := range a.view {
		if a.list[gi].Path == keep {
			a.sel = i
		}
	}
	if a.sel < 0 && len(a.view) > 0 {
		a.sel = 0
	}
	a.scroll[pageGames] = 0
	a.reveal()
	a.dirty = true
}

func (a *app) selected() *library.Game {
	if a.sel < 0 || a.sel >= len(a.view) {
		return nil
	}
	return &a.list[a.view[a.sel]]
}

func (a *app) keepPath() string {
	if g := a.selected(); g != nil {
		return g.Path
	}
	return ""
}

func (a *app) find(path string) *library.Game {
	for i := range a.list {
		if a.list[i].Path == path {
			return &a.list[i]
		}
	}
	return nil
}

// Layout, in pixels.
func (a *app) rowH() int32   { return a.px(88) }
func (a *app) rowGap() int32 { return a.px(8) }
func (a *app) tileW() int32  { return a.px(172) }
func (a *app) tileH() int32  { return a.px(196) }

// columns is the grid's column count.
func (a *app) columns() int {
	_, _, w, _ := a.area()
	return max(1, int((w-a.pad())/(a.tileW()+a.rowGap())))
}

// itemRect is item i's rectangle relative to the area's top, unscrolled.
func (a *app) itemRect(i int) (x, y, w, h int32) {
	_, _, aw, _ := a.area()
	p := a.pad()
	if !a.grid {
		return p, p + int32(i)*(a.rowH()+a.rowGap()), aw - 2*p, a.rowH()
	}
	cols := a.columns()
	used := int32(cols)*(a.tileW()+a.rowGap()) - a.rowGap()
	left := (aw - used) / 2
	return left + int32(i%cols)*(a.tileW()+a.rowGap()), p + int32(i/cols)*(a.tileH()+a.rowGap()), a.tileW(), a.tileH()
}

func (a *app) gamesH() int32 {
	if len(a.view) == 0 {
		return 0
	}
	_, y, _, h := a.itemRect(len(a.view) - 1)
	return y + h + a.pad()
}

// reveal scrolls the selection into view.
func (a *app) reveal() {
	if a.sel < 0 {
		return
	}
	_, y, _, h := a.itemRect(a.sel)
	_, _, _, ah := a.area()
	p := a.pad()
	s := &a.scroll[pageGames]
	if y-p < *s {
		*s = y - p
	}
	if y+h+p > *s+ah {
		*s = y + h + p - ah
	}
	*s = max(0, min(*s, a.gamesH()-ah))
}

func (a *app) gamesKey(sc int32, ctrl bool) {
	step := 1
	if a.grid {
		step = a.columns()
	}
	_, _, _, ah := a.area()
	page := max(1, int(ah/(a.rowH()+a.rowGap())))
	if a.grid {
		page = max(1, int(ah/(a.tileH()+a.rowGap()))) * a.columns()
	}
	move := func(d int) {
		if len(a.view) == 0 {
			return
		}
		a.sel = max(0, min(len(a.view)-1, a.sel+d))
		a.reveal()
	}
	switch {
	case sc == scReturn || sc == scKPEnter:
		a.play()
	case sc == scEscape:
		if a.search.text != "" {
			a.search.set("")
			a.refilter(a.keepPath())
		}
	case ctrl && sc == scG:
		a.Grid(true)
	case ctrl && sc == scL:
		a.Grid(false)
	case sc == scUp:
		move(-step)
	case sc == scDown:
		move(step)
	case sc == scLeft && a.grid:
		move(-1)
	case sc == scRight && a.grid:
		move(1)
	case sc == scPageUp:
		move(-page)
	case sc == scPageDown:
		move(page)
	case sc == scHome:
		move(-len(a.view))
	case sc == scEnd:
		move(len(a.view))
	}
}

func (a *app) gamesToolbar(x, y int32) {
	sx := x
	label := "Sort: " + a.sort.String() + " ▼"
	x += a.button(label, x, y, btnNormal, func() {
		var items []string
		for m := sortName; m < sortModes; m++ {
			items = append(items, m.String())
		}
		a.openPopup(sx, y, a.buttonW(label), items, int(a.sort), func(i int) {
			a.sort = sortMode(i)
			a.refilter(a.keepPath())
		})
	}) + a.px(4)
	fx := x
	flabel := "System: All ▼"
	if a.filter != "" {
		flabel = "System: " + filterName(a.filter) + " ▼"
	}
	x += a.button(flabel, x, y, btnNormal, func() {
		values := a.filters()
		items, cur := make([]string, len(values)), 0
		for i, v := range values {
			items[i] = filterName(v)
			if v == a.filter {
				cur = i
			}
		}
		items[0] = "All"
		a.openPopup(fx, y, a.buttonW(flabel), items, cur, func(i int) {
			a.filter = values[i]
			a.refilter(a.keepPath())
		})
	}) + a.px(4)

	fw := min(a.px(320), a.w/4)
	a.drawField(&a.search, a.w-a.pad()-fw, y, fw)
}

func (a *app) gamesStatus() string {
	var total time.Duration
	for _, g := range a.list {
		total += g.PlayTime
	}
	s := plural(len(a.list), "game")
	if len(a.view) != len(a.list) {
		s = plural(len(a.view), "match") + " of " + s
		s = strings.Replace(s, "matchs", "matches", 1)
	}
	if total > 0 {
		s += "  |  Total play time: " + duration(total)
	}
	switch len(a.romDirs) {
	case 0:
	case 1:
		s += "  |  " + a.romDirs[0].Path
	default:
		s += "  |  " + plural(len(a.romDirs), "ROM folder")
	}
	return s
}

func (a *app) drawGames() {
	switch {
	case len(a.romDirs) == 0:
		a.emptyMessage("No ROM folder", "Choose one in Settings, or create a roms folder beside doszer.")
		a.centerButton("Open Settings", a.openSettings)
		return
	case a.scanErr != nil:
		a.emptyMessage("Can't read the ROM folder", a.scanErr.Error())
		return
	case a.scanning && len(a.list) == 0:
		a.emptyMessage("Scanning", strings.Join(config.DirPaths(a.romDirs), ", "))
		return
	case len(a.list) == 0:
		a.emptyMessage("No games yet", "No .dosz archives in "+strings.Join(config.DirPaths(a.romDirs), ", "))
		if canOpen() {
			a.centerButton("Open the ROM folder", func() { a.open(a.romDirs[0].Path) })
		}
		return
	case len(a.view) == 0:
		a.emptyMessage("No matches", "No game matches “"+a.search.text+"” (Esc clears the search)")
		return
	}
	_, ay, _, ah := a.area()
	scroll := a.scroll[pageGames]
	for i := range a.view {
		x, y, w, h := a.itemRect(i)
		y += ay - scroll
		if y+h < ay || y > ay+ah {
			continue
		}
		hover := a.hovered(x, y, w, h)
		g := &a.list[a.view[i]]
		if a.grid {
			a.drawTile(g, x, y, w, h, i == a.sel, hover)
		} else {
			a.drawRow(g, x, y, w, h, i == a.sel, hover)
		}
		a.clipHit(x, y, w, h, func(double bool) {
			a.sel = i
			a.menuAt = func() { a.gameMenu(g) }
			if double {
				a.play()
			}
		})
	}
}

// centerButton draws a button below an emptyMessage.
func (a *app) centerButton(label string, fn func()) {
	_, ay, aw, ah := a.area()
	w := a.buttonW(label)
	a.button(label, (aw-w)/2, ay+ah/2+a.px(56), btnAccent, fn)
}

// card draws an item's background.
func (a *app) card(x, y, w, h int32, sel, hover bool) {
	r := a.px(10)
	switch {
	case sel:
		a.outline(x, y, w, h, r, a.px(2), colAccent, colCardSel)
	case hover:
		a.round(x, y, w, h, r, colCardHover)
	default:
		a.round(x, y, w, h, r, colCard)
	}
}

func (a *app) drawRow(g *library.Game, x, y, w, h int32, sel, hover bool) {
	a.card(x, y, w, h, sel, hover)
	is := a.px(64)
	ix := x + a.px(12)
	iy := y + (h-is)/2
	a.drawIcon(g, ix, iy, is)

	right := a.px(360)
	tx := ix + is + a.px(16)
	tw := w - (tx - x) - right - a.px(24)
	ty := y + a.px(12)
	a.text(a.ellipsize(g.Name, tw, 16, true), tx, ty, 16, true, colText)
	ty += a.lineHeight(16, true) + a.px(2)
	a.text(a.ellipsize(g.Rel, tw, 13, false), tx, ty, 13, false, colMuted)
	ty += a.lineHeight(13, false) + a.px(6)
	a.chip(g, tx, ty)

	sx := x + w - right - a.px(12)
	a.fill(sx, y+a.px(14), a.px(1), h-a.px(28), colLine)
	rx := x + w - a.px(16)
	ry := y + a.px(14)
	lh := a.lineHeight(13, false) + a.px(4)

	// What it needs and what disc it is, as Ryujinx shows a title ID.
	mx := sx + a.px(16)
	my := ry
	if osl, bad := a.needBadge(g); osl != "" {
		c := colMuted
		if bad {
			c = colError
		}
		a.badge(strings.TrimPrefix(osl, "Needs "), c, mx, my)
	}
	my += a.lineHeight(12, false) + a.px(12)
	if g.Disc != "" {
		id := g.Disc[:8]
		iw := a.textWidth(id, 12, false) + a.px(16)
		ih := a.lineHeight(12, false) + a.px(6)
		bg := rgba{72, 72, 80, 255}
		if a.hovered(mx, my, iw, ih) {
			bg = colCardSel
		}
		a.round(mx, my, iw, ih, a.px(5), bg)
		a.monoText(id, mx+a.px(8), my+a.px(3), colText)
		full := g.Disc
		a.clipHit(mx, my, iw, ih, func(bool) { a.copyText(full) })
		if g.Redump != "" {
			a.text("Redump", mx+iw+a.px(8), my+a.px(3), 12, false, colDOS)
		}
	}
	played := "Never played"
	if !g.LastPlayed.IsZero() {
		played = g.LastPlayed.Local().Format("Jan 2, 2006 3:04 PM")
	}
	lines := []string{played}
	if g.PlayTime > 0 {
		lines = append(lines, duration(g.PlayTime)+" played")
	}
	lines = append(lines, size(g.Size))
	for i, l := range lines {
		c := colMuted
		if i == 0 {
			c = colText
		}
		a.textRight(l, rx, ry+int32(i)*lh, 13, false, c)
	}
}

func (a *app) drawTile(g *library.Game, x, y, w, h int32, sel, hover bool) {
	a.card(x, y, w, h, sel, hover)
	is := a.px(96)
	a.drawIcon(g, x+(w-is)/2, y+a.px(18), is)
	ty := y + a.px(18) + is + a.px(14)
	name := a.ellipsize(g.Name, w-a.px(20), 14, true)
	a.text(name, x+(w-a.textWidth(name, 14, true))/2, ty, 14, true, colText)
	ty += a.lineHeight(14, true) + a.px(4)
	label, c := kindLabel(g)
	label = a.ellipsize(label, w-a.px(20), 12, false)
	a.text(label, x+(w-a.textWidth(label, 12, false))/2, ty, 12, false, c)
}

// chip draws a coloured badge and returns its width.
func (a *app) badge(label string, c rgba, x, y int32) int32 {
	w := a.textWidth(label, 12, false) + a.px(16)
	h := a.lineHeight(12, false) + a.px(6)
	a.round(x, y, w, h, a.px(5), rgba{c.r / 5, c.g / 5, c.b / 5, 255})
	a.text(label, x+a.px(8), y+a.px(3), 12, false, c)
	return w
}

// chip draws how the game boots as a badge, flagging a missing system.
func (a *app) chip(g *library.Game, x, y int32) {
	label, c := kindLabel(g)
	if g.Err != "" {
		a.badge(a.ellipsize("Unreadable: "+g.Err, a.px(420), 12, false), colError, x, y)
		return
	}
	x += a.badge(label, c, x, y) + a.px(6)
	switch {
	case g.Broken != "":
		a.badge(a.ellipsize(g.Broken, a.px(360), 12, false), colError, x, y)
	case g.Boot != "" && a.files != nil && !a.hasSystem(g.Boot, g.Kind == library.KindShell):
		a.badge("not in the system folder", colError, x, y)
	case len(g.Parents) > 0:
		a.badge("over "+strings.Join(g.Parents, " > "), colMuted, x, y)
	}
}

func kindLabel(g *library.Game) (string, rgba) {
	switch g.Kind {
	case library.KindShell:
		return g.System + " (shell)", colShell
	case library.KindOS:
		return g.System, colOS
	}
	return "DOS", colDOS
}

// drawIcon draws the game's DOSZ.ICO, or a tile with its initial.
func (a *app) drawIcon(g *library.Game, x, y, s int32) {
	t, ok := a.icons[g.Path]
	if !ok {
		t = nil
		if g.Icon != nil {
			if img, err := icon.Decode(g.Icon, int(s)); err == nil {
				b := img.Bounds()
				n := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
				draw.Draw(n, n.Bounds(), img, b.Min, draw.Src)
				t = a.upload(n, true)
			}
		}
		a.icons[g.Path] = t
	}
	if t != nil {
		a.image(t, x, y, s)
		return
	}
	_, c := kindLabel(g)
	a.initialTile(g.Name, c, x, y, s)
}

// initialTile draws a rounded square with name's first letter.
func (a *app) initialTile(name string, c rgba, x, y, s int32) {
	a.round(x, y, s, s, s/6, rgba{c.r / 3, c.g / 3, c.b / 3, 255})
	r, _ := utf8.DecodeRuneInString(strings.TrimLeft(name, " ."))
	initial := strings.ToUpper(string(r))
	size := float64(s) / a.scale * 0.5
	w := a.textWidth(initial, size, true)
	lh := a.lineHeight(size, true)
	a.text(initial, x+(s-w)/2, y+(s-lh)/2, size, true, c)
}

// play runs the selected game with `doszer run`, one at a time.
func (a *app) play() {
	g := a.selected()
	if g == nil {
		return
	}
	if g.Err != "" {
		a.say(g.Name+": "+g.Err, true)
		return
	}
	a.playFile(g.Path)
}

// playFile runs an archive with `doszer run`, one at a time: from the
// list, File > Open or a drop.
func (a *app) playFile(path string) {
	if a.running != "" {
		a.say("A game is already running", true)
		return
	}
	exe, err := a.exe()
	if err != nil {
		a.say(err.Error(), true)
		return
	}
	args := []string{"run"}
	if a.o.SystemDir != "" {
		args = append(args, "--system-dir", a.o.SystemDir)
	}
	if a.o.SaveDir != "" {
		args = append(args, "--save-dir", a.o.SaveDir)
	}
	args = append(args, "--", path)
	cmd := exec.Command(exe, args...)
	hideConsole(cmd)
	tail := &tailBuffer{}
	cmd.Stdout, cmd.Stderr = tail, tail
	start := time.Now()
	if err := cmd.Start(); err != nil {
		a.say(err.Error(), true)
		return
	}
	a.running = path
	a.message, a.msgErr = "", false
	go func() {
		err := cmd.Wait()
		if err != nil {
			if line := tail.lastError(); line != "" {
				err = errors.New(line)
			}
		}
		a.runCh <- runResult{path, start, time.Since(start), err}
	}()
}

func (a *app) finished(r runResult) {
	a.running = ""
	var name string
	for i := range a.list {
		g := &a.list[i]
		if g.Path != r.path {
			continue
		}
		name = g.Name
		g.LastPlayed = r.start
		g.PlayTime += r.took.Round(time.Second)
	}
	if a.stats != nil {
		if err := a.stats.Add(r.path, r.start, r.took); err != nil {
			a.say("play time: "+err.Error(), true)
		}
	}
	if r.err != nil {
		a.say(name+": "+r.err.Error(), true)
	}
	if a.sort != sortName {
		a.refilter(r.path)
	}
	if a.win != 0 && !a.quit {
		raiseWindow(a.win)
	}
}

// tailBuffer keeps the end of a game process's output, for its error.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > 8<<10 {
		t.buf = t.buf[len(t.buf)-8<<10:]
	}
	return len(p), nil
}

// lastError is the last "ERROR: " line's message (main's logError), else
// the last line.
func (t *tailBuffer) lastError() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	lines := strings.Split(strings.TrimSpace(string(bytes.ReplaceAll(t.buf, []byte("\r"), nil))), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if _, msg, ok := strings.Cut(lines[i], "ERROR: "); ok {
			return msg
		}
	}
	return strings.TrimSpace(lines[len(lines)-1])
}

// Filters beside a system's label (kindLabel) and a need (needLabel).
const (
	filterMissing = "Missing its system"
	filterBroken  = "Broken parent chain"
	filterIn      = "In " // + a ROM folder
)

// filters lists the System filter's choices: "" (all), every system the
// games boot, and the problems.
func (a *app) filters() []string {
	boots, needs := map[string]bool{}, map[string]bool{}
	missing, broken := false, false
	for i := range a.list {
		g := &a.list[i]
		l, _ := kindLabel(g)
		boots[l] = true
		if n := needLabel(g); n != "" {
			needs[n] = true
		}
		missing = missing || g.Boot != "" && a.files != nil && !a.hasSystem(g.Boot, g.Kind == library.KindShell)
		broken = broken || g.Broken != ""
	}
	sorted := func(m map[string]bool) []string {
		var out []string
		for k := range m {
			out = append(out, k)
		}
		sort.Strings(out)
		return out
	}
	// What they boot, then what their programs need, then folders, then
	// problems, each group apart.
	out := append([]string{""}, sorted(boots)...)
	group := func(items []string) {
		if len(items) > 0 {
			out = append(append(out, "-"), items...)
		}
	}
	group(sorted(needs))
	if len(a.romDirs) > 1 {
		var dirs []string
		for _, d := range a.romDirs {
			dirs = append(dirs, filterIn+d.Path)
		}
		group(dirs)
	}
	var problems []string
	if missing {
		problems = append(problems, filterMissing)
	}
	if broken {
		problems = append(problems, filterBroken)
	}
	group(problems)
	return out
}

// filterName is a filter's name in the menu: a need without its
// "Needs" (it's in its own group).
func filterName(v string) string {
	if n, ok := strings.CutPrefix(v, "Needs "); ok {
		return n
	}
	return v
}

func (a *app) matchesFilter(g *library.Game) bool {
	switch a.filter {
	case filterMissing:
		return g.Boot != "" && a.files != nil && !a.hasSystem(g.Boot, g.Kind == library.KindShell)
	case filterBroken:
		return g.Broken != ""
	}
	if root, ok := strings.CutPrefix(a.filter, filterIn); ok {
		return g.Root == root
	}
	if strings.HasPrefix(a.filter, "Needs ") {
		return needLabel(g) == a.filter
	}
	l, _ := kindLabel(g)
	return l == a.filter
}

// needLabel is what a game's programs need at least, "" for DOS or none.
func needLabel(g *library.Game) string {
	switch g.Need.Game.Platform {
	case platform.Win16, platform.Win32:
		if g.Need.DOS {
			return "Needs DOS or " + g.Need.Game.String()
		}
		return "Needs " + g.Need.Game.String()
	}
	return ""
}

// needBadge is a game's need, and whether it can't be met here: no shell
// or image in the system directory new enough (judged by their names,
// install.GuessOS; an image whose name doesn't tell may be one), or the
// one it boots is too old.
func (a *app) needBadge(g *library.Game) (string, bool) {
	label := needLabel(g)
	if label == "" || g.Need.DOS || a.files == nil {
		return label, false
	}
	need := g.Need.Game
	if g.Boot != "" {
		for _, f := range a.files {
			if !f.missing && f.Name == g.Boot && f.Shell == (g.Kind == library.KindShell) {
				if have := install.GuessOS(f.SystemFile); have.Platform != platform.NotProgram && !install.Satisfies(have, need) {
					return label + ", boots " + have.String(), true
				}
			}
		}
	}
	for _, f := range a.files {
		if f.missing {
			continue
		}
		have := install.GuessOS(f.SystemFile)
		if have.Platform == platform.NotProgram || install.Satisfies(have, need) {
			return label, false
		}
	}
	return label + ": none in the system folder", true
}

// matchesSearch reports whether a game's name, file, system, what its
// programs need, disc hash or Redump name holds q (lower case).
func matchesSearch(g *library.Game, q string) bool {
	kind, _ := kindLabel(g)
	for _, s := range []string{g.Name, g.Rel, g.System, g.Boot, kind, needLabel(g), g.Disc, g.Redump} {
		if strings.Contains(strings.ToLower(s), q) {
			return true
		}
	}
	return false
}
