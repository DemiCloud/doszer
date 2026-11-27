package gui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/DemiCloud/doszer/internal/config"
	"github.com/DemiCloud/doszer/internal/library"
	"github.com/DemiCloud/doszer/internal/runlog"
)

// The Logs page: the runs doszer recorded (runlog), as one item per
// session - the commands one window flow ran (Import a disc: dump, init,
// install, start, build) - or per run outside one, newest first. An item
// opens into its runs. Beside the list, the selected session's summary
// or run's report - what it ran, chose, warned about and wrote - with
// its history: the runs about the same game.

// logs is the Logs page's state.
type logs struct {
	runs        []*runlog.Report // newest first; nil until read
	groups      []*runGroup      // sessions and lone runs, newest first
	logsCh      chan []*runlog.Report
	logsReading bool
	logGroup    string          // the selected item's key
	logRun      string          // the selected run's ID, "" for the item itself
	logOpen     map[string]bool // items opened into their runs, by key
	logWant     string          // a run to select once the logs are read
	logScroll   int32           // the report's scroll, pixels
	logReportH  int32           // the report's height, as last drawn
	logSplit    int32           // where the report starts, as last drawn
}

// runGroup is a session's runs, or one run outside a session.
type runGroup struct {
	key  string           // the session, or the run's ID
	runs []*runlog.Report // oldest first
}

// session reports whether g is a session: more than its one run.
func (g *runGroup) session() bool { return len(g.runs) > 1 }

func (g *runGroup) first() *runlog.Report { return g.runs[0] }
func (g *runGroup) last() *runlog.Report  { return g.runs[len(g.runs)-1] }

// subject is what the item is about: the name its newest named run
// gives, else any run's subject.
func (g *runGroup) subject() string {
	for i := len(g.runs) - 1; i >= 0; i-- {
		if n := g.runs[i].Name; n != "" {
			return n
		}
	}
	for _, r := range g.runs {
		if s := r.Subject(); s != "" {
			return s
		}
	}
	return g.first().Command
}

// result is the item's last run's: a flow stops at its first failure.
func (g *runGroup) result() *runlog.Report { return g.last() }

func (g *runGroup) warnings() int {
	n := 0
	for _, r := range g.runs {
		n += len(r.Warnings)
	}
	return n
}

// running is the time its commands took, not the time between them
// (the user installing, swapping discs).
func (g *runGroup) running() time.Duration {
	var d time.Duration
	for _, r := range g.runs {
		d += r.Duration()
	}
	return d
}

// commands are its runs' commands, in order.
func (g *runGroup) commands() string {
	var cs []string
	for _, r := range g.runs {
		cs = append(cs, r.Command)
	}
	return strings.Join(cs, ", ")
}

// groupRuns gathers runs (newest first) into items, newest first.
func groupRuns(runs []*runlog.Report) []*runGroup {
	var out []*runGroup
	byKey := map[string]*runGroup{}
	for i := len(runs) - 1; i >= 0; i-- { // oldest first
		r := runs[i]
		key := r.Session
		if key == "" {
			key = r.ID
		}
		g := byKey[key]
		if g == nil {
			g = &runGroup{key: key}
			byKey[key] = g
			out = append(out, g)
		}
		g.runs = append(g.runs, r)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].last().Started.After(out[j].last().Started) })
	return out
}

func (a *app) initLogs() {
	a.logsCh = make(chan []*runlog.Report, 1)
	a.logOpen = map[string]bool{}
}

// logsDir is where the runs are read from: the tests' folder, else
// doszer's (none for a screenshot without one).
func (a *app) logsDir() string {
	if a.o.LogsDir != "" || a.o.Screenshot != "" {
		return a.o.LogsDir
	}
	dir, err := config.LogsDir()
	if err != nil {
		return ""
	}
	return dir
}

// readLogs reads the runs again, in the background.
func (a *app) readLogs() {
	if a.logsReading {
		return
	}
	dir := a.logsDir()
	if dir == "" {
		a.runs, a.groups = []*runlog.Report{}, nil
		return
	}
	a.logsReading = true
	go func() {
		runs, _ := runlog.List(dir)
		if runs == nil {
			runs = []*runlog.Report{}
		}
		a.logsCh <- runs
	}()
}

// logsRead takes the runs read, keeping the selection; with none, the
// newest item is selected.
func (a *app) logsRead(runs []*runlog.Report) {
	a.logsReading = false
	a.runs, a.groups = runs, groupRuns(runs)
	if a.logWant != "" {
		a.selectRun(a.logWant)
		a.logWant = ""
	}
	if a.selectedGroup() == nil && len(a.groups) > 0 {
		a.logGroup, a.logRun = a.groups[0].key, ""
	}
	a.dirty = true
}

// showRun shows the run with ID id in Logs, selected, its item open.
func (a *app) showRun(id string) {
	a.show(pageLogs)
	if !a.selectRun(id) {
		a.logWant = id
	}
}

// selectRun selects the run with ID id, opening its item.
func (a *app) selectRun(id string) bool {
	for _, g := range a.groups {
		for _, r := range g.runs {
			if r.ID != id {
				continue
			}
			a.logGroup, a.logRun, a.logScroll = g.key, "", 0
			if g.session() {
				a.logRun = id
				a.logOpen[g.key] = true
			}
			a.revealRun()
			return true
		}
	}
	return false
}

func (a *app) selectedGroup() *runGroup {
	for _, g := range a.groups {
		if g.key == a.logGroup {
			return g
		}
	}
	return nil
}

// selectedRun is the run whose report shows: the one selected, or a
// lone run's item. nil for a session's summary.
func (a *app) selectedRun() *runlog.Report {
	g := a.selectedGroup()
	if g == nil {
		return nil
	}
	if !g.session() {
		return g.first()
	}
	for _, r := range g.runs {
		if r.ID == a.logRun {
			return r
		}
	}
	return nil
}

// logRow is a line of the list: an item, or one of an open item's runs.
type logRow struct {
	g *runGroup
	r *runlog.Report // nil: the item
}

// logRows are the list's lines, as shown.
func (a *app) logRows() []logRow {
	var out []logRow
	for _, g := range a.groups {
		out = append(out, logRow{g: g})
		if g.session() && a.logOpen[g.key] {
			for _, r := range g.runs {
				out = append(out, logRow{g: g, r: r})
			}
		}
	}
	return out
}

func (a *app) logRowH() int32 { return a.px(58) }
func (a *app) logSubH() int32 { return a.px(34) }

func (a *app) rowHeight(l logRow) int32 {
	if l.r != nil {
		return a.logSubH()
	}
	return a.logRowH()
}

func (a *app) logsH() int32 {
	h := a.pad()
	for _, l := range a.logRows() {
		h += a.rowHeight(l) + a.rowGap()
	}
	return h + a.pad()
}

// selectedRow is the selected line's index in rows, or -1.
func (a *app) selectedRow(rows []logRow) int {
	for i, l := range rows {
		if l.g.key != a.logGroup {
			continue
		}
		if l.r == nil && a.logRun == "" || l.r != nil && l.r.ID == a.logRun {
			return i
		}
	}
	return -1
}

// revealRun scrolls the list to the selected line.
func (a *app) revealRun() {
	_, _, _, ah := a.area()
	rows := a.logRows()
	sel := a.selectedRow(rows)
	if sel < 0 {
		return
	}
	top := a.pad()
	for _, l := range rows[:sel] {
		top += a.rowHeight(l) + a.rowGap()
	}
	h := a.rowHeight(rows[sel])
	s := &a.scroll[pageLogs]
	if top < *s {
		*s = top - a.pad()
	} else if top+h+a.pad() > *s+ah {
		*s = top + h + a.pad() - ah
	}
	*s = max(0, *s)
}

// selectRow selects line l: an item, or one of its runs.
func (a *app) selectRow(l logRow) {
	a.logGroup, a.logRun, a.logScroll = l.g.key, "", 0
	if l.r != nil {
		a.logRun = l.r.ID
	}
}

func (a *app) logsKey(sc int32) {
	rows := a.logRows()
	if len(rows) == 0 {
		return
	}
	sel := max(0, a.selectedRow(rows))
	switch sc {
	case scUp:
		sel--
	case scDown:
		sel++
	case scPageUp:
		sel -= 8
	case scPageDown:
		sel += 8
	case scHome:
		sel = 0
	case scEnd:
		sel = len(rows) - 1
	case scRight, scLeft, scReturn, scKPEnter: // open or close the item
		g := rows[sel].g
		if g.session() {
			a.logOpen[g.key] = sc == scRight || (sc != scLeft && !a.logOpen[g.key])
			if !a.logOpen[g.key] {
				a.logRun = ""
			}
		}
		a.revealRun()
		return
	default:
		return
	}
	a.selectRow(rows[max(0, min(len(rows)-1, sel))])
	a.revealRun()
}

func (a *app) logsToolbar(x, y int32) {
	if dir := a.logsDir(); dir != "" && canOpen() {
		x += a.button("Open folder", x, y, btnNormal, func() {
			os.MkdirAll(dir, 0o755)
			a.open(dir)
		}) + a.px(4)
	}
	a.button("Refresh", x, y, btnNormal, a.readLogs)
}

func (a *app) logsStatus() string {
	failed := 0
	for _, g := range a.groups {
		if g.result().Result == runlog.ResultFailed {
			failed++
		}
	}
	s := plural(len(a.groups), "run")
	if failed > 0 {
		s += ", " + itoa(failed) + " failed"
	}
	if dir := a.logsDir(); dir != "" {
		s += "  |  " + dir
	}
	return s
}

// resultColour is a run's result as a colour.
func resultColour(r *runlog.Report) rgba {
	return resultColourOf(r.Result, len(r.Warnings))
}

func resultColourOf(result string, warnings int) rgba {
	switch result {
	case runlog.ResultOK:
		if warnings > 0 {
			return colShell
		}
		return colDOS
	case runlog.ResultFailed:
		return colError
	}
	return colMuted
}

// resultLabel is a run's result and duration, as a word or two.
func resultLabel(r *runlog.Report) string {
	return resultLabelOf(r.Result, len(r.Warnings), r.Duration())
}

func resultLabelOf(result string, warnings int, d time.Duration) string {
	ds := shortDuration(d)
	switch result {
	case runlog.ResultOK:
		if warnings > 0 {
			return plural(warnings, "warning") + ", " + ds
		}
		return ds
	case runlog.ResultFailed:
		return "failed after " + ds
	case runlog.ResultInterrupted:
		return "stopped after " + ds
	}
	return "didn't finish"
}

func shortDuration(d time.Duration) string {
	switch {
	case d < time.Second:
		return d.Round(time.Millisecond).String()
	case d < time.Minute:
		return d.Round(100 * time.Millisecond).String()
	}
	return d.Round(time.Second).String()
}

func runSubject(r *runlog.Report) string {
	if s := r.Subject(); s != "" {
		return s
	}
	return r.Command
}

func (a *app) drawLogs() {
	switch {
	case a.runs == nil:
		a.emptyMessage("Reading the logs")
		return
	case len(a.runs) == 0 && !config.Configured() && a.o.LogsDir == "":
		a.emptyMessage("No logs", "doszer isn't set up: nothing is recorded.")
		a.centerButton("Set up doszer", a.openSetup)
		return
	case len(a.runs) == 0:
		a.emptyMessage("No runs yet", "Builds, installs and dumps are recorded here.")
		return
	}
	ax, ay, aw, ah := a.area()
	p := a.pad()
	listW := min(max(aw*2/5, a.px(320)), a.px(460))
	a.logSplit = ax + p + listW + p

	// The items, an open one's runs below it.
	rows := a.logRows()
	sel := a.selectedRow(rows)
	y := ay + p - a.scroll[pageLogs]
	for i, l := range rows {
		h := a.rowHeight(l)
		if y+h >= ay && y <= ay+ah {
			a.drawLogRow(l, ax+p, y, listW, h, i == sel)
		}
		y += h + a.rowGap()
	}

	// The selected run's report, or session's summary.
	rx, rw := a.logSplit, ax+aw-p-a.logSplit
	a.pushClip(rx, ay, rw+p, ah)
	defer a.popClip()
	y = ay + p - a.logScroll
	if r := a.selectedRun(); r != nil {
		y = a.drawRunReport(r, rx, y, rw)
	} else if g := a.selectedGroup(); g != nil {
		y = a.drawSessionReport(g, rx, y, rw)
	}
	a.logReportH = y + a.logScroll - ay + p
}

// drawLogRow draws a line of the list.
func (a *app) drawLogRow(l logRow, x, y, w, h int32, sel bool) {
	g := l.g
	if l.r != nil { // one of an open item's runs, indented
		r := l.r
		x, w = x+a.px(24), w-a.px(24)
		a.card(x, y, w, h, sel, a.hovered(x, y, w, h))
		a.clipHit(x, y, w, h, func(bool) { a.selectRow(l) })
		d := a.px(8)
		a.round(x+a.px(12), y+(h-d)/2, d, d, d/2, resultColour(r))
		a.text(r.Command, x+a.px(28), y+(h-a.lineHeight(13, true))/2, 13, true, colText)
		a.textRight(resultLabel(r), x+w-a.px(12), y+(h-a.lineHeight(12, false))/2, 12, false, colMuted)
		return
	}
	a.card(x, y, w, h, sel, a.hovered(x, y, w, h))
	a.clipHit(x, y, w, h, func(bool) {
		// A click selects the item; on the selected item, it opens or
		// closes it.
		if g.session() && a.logGroup == g.key && a.logRun == "" {
			a.logOpen[g.key] = !a.logOpen[g.key]
		}
		a.selectRow(l)
	})
	last := g.result()
	d := a.px(10)
	a.round(x+a.px(14), y+(h-d)/2, d, d, d/2, resultColourOf(last.Result, g.warnings()))
	tx := x + a.px(34)
	when := g.first().Started.Local().Format("Jan 2, 3:04 PM")
	ww := a.textWidth(when, 12, false)
	a.text(a.ellipsize(g.subject(), w-(tx-x)-ww-a.px(24), 14, true), tx, y+a.px(10), 14, true, colText)
	a.textRight(when, x+w-a.px(12), y+a.px(12), 12, false, colMuted)
	line := last.Command + "  ·  " + resultLabel(last)
	if g.session() {
		mark := "►"
		if a.logOpen[g.key] {
			mark = "▼"
		}
		line = mark + "  " + g.commands() + "  ·  " + resultLabelOf(last.Result, g.warnings(), g.running())
	}
	a.text(a.ellipsize(line, w-(tx-x)-a.px(12), 12, false), tx, y+a.px(32), 12, false, colMuted)
}

// scrollLog scrolls the report, within its height.
func (a *app) scrollLog(d int32) {
	_, _, _, h := a.area()
	a.logScroll = max(0, min(a.logScroll+d, a.logReportH-h))
}

// exportRun packs r's history into a ZIP in a folder the user picks.
func (a *app) exportRun(r *runlog.Report) {
	what := r.ID
	if r.Source != "" {
		what = r.Source
	}
	a.exportLogs(runSubject(r), r.Started, what)
}

// exportLogs packs what (a run, session or path, as doszer logs takes
// them) into a ZIP in a folder the user picks, named after subject and
// when: doszer logs --export, redacted.
func (a *app) exportLogs(subject string, when time.Time, what string) {
	start, _ := os.UserHomeDir()
	dir, ok := pickFolder("export", start)
	if !ok {
		return
	}
	name := safeName(subject)
	if name == "" {
		name = "run"
	}
	out := filepath.Join(dir, "doszer-logs-"+name+"-"+when.Format("20060102-150405")+".zip")
	a.runDoszer("Export logs", []string{"logs", "--format", "plain", "--force", "--export", out, what}, nil)
}

// builtRuns are the newest recorded run of each archive built, or
// failed to be: one per game, newest first.
func (a *app) builtRuns() []*runlog.Report {
	seen := map[string]bool{}
	var out []*runlog.Report
	for _, r := range a.runs {
		switch r.Command {
		case "build", "repack", "child", "convert", "set":
		default:
			continue
		}
		if r.Archive == "" && r.Result == runlog.ResultOK {
			continue
		}
		// One per game: a build and its repack, or a failed build and
		// the one that fixed it, are the same game's.
		key := strings.ToLower(runSubject(r))
		if !seen[key] {
			seen[key] = true
			out = append(out, r)
		}
	}
	return out
}

// failedBuilds are the builds whose newest run failed.
func (a *app) failedBuilds() []*runlog.Report {
	var out []*runlog.Report
	for _, r := range a.builtRuns() {
		if r.Result == runlog.ResultFailed {
			out = append(out, r)
		}
	}
	return out
}

// runGame is the game in the list r's archive is, or one standing in
// for it (its name, for an icon tile).
func (a *app) runGame(r *runlog.Report) *library.Game {
	if r.Archive != "" {
		if g := a.find(r.Archive); g != nil {
			return g
		}
	}
	return &library.Game{Name: runSubject(r), Path: "run:" + r.ID}
}
