package gui

import (
	"fmt"
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

// drawSessionReport draws a session's summary: its commands, each
// opening its report, what was chosen, warnings, and the archive it
// ended with.
func (a *app) drawSessionReport(g *runGroup, x, y, w int32) int32 {
	lh := a.lineHeight(13, false) + a.px(6)
	a.text(a.ellipsize(g.subject(), w, 18, true), x, y, 18, true, colText)
	y += a.lineHeight(18, true) + a.px(8)
	last := g.result()
	end := last.Started.Add(last.Duration())
	bx := x + a.badge(strings.ToUpper(last.Result[:1])+last.Result[1:], resultColourOf(last.Result, g.warnings()), x, y) + a.px(8)
	meta := g.first().Started.Local().Format("Mon Jan 2 2006, 3:04:05 PM") + " to " + end.Local().Format("3:04:05 PM") +
		"  ·  " + shortDuration(g.running()) + " running"
	a.text(a.ellipsize(meta, w-(bx-x), 12, false), bx, y+a.px(3), 12, false, colMuted)
	y += a.lineHeight(12, false) + a.px(18)

	key := g.key
	bxx := x
	bxx += a.button("Transcript", bxx, y, btnNormal, func() {
		a.runDoszer("Transcript", []string{"logs", "--format", "plain", "--transcript", key}, nil)
	}) + a.px(4)
	a.button("Export…", bxx, y, btnNormal, func() { a.exportLogs(g.subject(), g.first().Started, key) })
	y += a.px(32) + a.px(18)

	section := func(title string) {
		y += a.px(10)
		y = a.heading(title, x, y)
	}
	if last.Error != "" {
		for _, l := range a.wrapAny(last.Error, w, 13) {
			a.text(l, x, y, 13, false, colError)
			y += lh
		}
	}

	section("Commands")
	for _, r := range g.runs {
		hx, hw := x-a.px(6), w+a.px(12)
		if a.hovered(hx, y-a.px(3), hw, lh) {
			a.round(hx, y-a.px(3), hw, lh, a.px(5), colCardHover)
		}
		d := a.px(8)
		a.round(x, y+(lh-d)/2-a.px(3), d, d, d/2, resultColour(r))
		rl := resultLabel(r)
		rlw := a.textWidth(rl, 12, false)
		a.text(a.ellipsize(r.CommandLine(), w-rlw-a.px(36), 13, false), x+a.px(16), y, 13, false, colText)
		a.textRight(rl, x+w, y+a.px(1), 12, false, colMuted)
		id := r.ID
		a.clipHit(hx, y-a.px(3), hw, lh, func(bool) {
			a.logRun, a.logScroll = id, 0
			a.logOpen[key] = true
		})
		y += lh
	}

	var chosen []runlog.Decision
	var warnings []string
	for _, r := range g.runs {
		chosen = append(chosen, r.Decisions...)
		warnings = append(warnings, r.Warnings...)
	}
	if len(chosen) > 0 {
		section("Chosen")
		for _, d := range chosen {
			lw := a.px(110)
			a.text(d.What, x, y, 13, false, colMuted)
			for i, l := range a.wrapAny(d.Choice, w-lw, 13) {
				if i > 0 {
					y += lh
				}
				a.text(l, x+lw, y, 13, false, colText)
			}
			y += lh
			if d.Why != "" {
				y -= a.px(4)
				for _, l := range a.wrap(d.Why, w-lw, 12) {
					a.text(l, x+lw, y, 12, false, colMuted)
					y += a.lineHeight(12, false) + a.px(4)
				}
				y += a.px(4)
			}
		}
	}
	if len(warnings) > 0 {
		section("Warnings")
		for _, wn := range warnings {
			for _, l := range a.wrapAny(wn, w, 13) {
				a.text(l, x, y, 13, false, colShell)
				y += lh
			}
		}
	}
	for i := len(g.runs) - 1; i >= 0; i-- {
		if b := g.runs[i].Build; b != nil && b.Archive != nil {
			ar := b.Archive
			section("Archive")
			for _, kv := range [][2]string{
				{"Path", ar.Path},
				{"Size", fmt.Sprintf("%s, %s of content (%.0f%%)", size(ar.Size), size(ar.Content), ar.Ratio()*100)},
				{"Entries", itoa(len(ar.Entries))},
				{"SHA-1", ar.SHA1},
			} {
				lw := a.px(110)
				a.text(kv[0], x, y, 13, false, colMuted)
				for j, l := range a.wrapAny(kv[1], w-lw, 13) {
					if j > 0 {
						y += lh
					}
					a.text(l, x+lw, y, 13, false, colText)
				}
				y += lh
			}
			break
		}
	}
	return y
}

// scrollLog scrolls the report, within its height.
func (a *app) scrollLog(d int32) {
	_, _, _, h := a.area()
	a.logScroll = max(0, min(a.logScroll+d, a.logReportH-h))
}

// drawRunReport draws r's report from y in a column w wide, returning
// the y below it.
func (a *app) drawRunReport(r *runlog.Report, x, y, w int32) int32 {
	lh := a.lineHeight(13, false) + a.px(6)
	a.text(a.ellipsize(runSubject(r), w, 18, true), x, y, 18, true, colText)
	y += a.lineHeight(18, true) + a.px(8)

	// The command line, selectable.
	cmd := r.CommandLine()
	k := faceKey{px: int(a.px(12)), mono: true}
	lines := a.wrapMono(cmd, w)
	a.selRegion("run", lines)
	for i, l := range lines {
		a.selTextLine("run", i, x, y, w, lh, l, x, k)
		a.monoText(l, x, y+a.px(2), colMuted)
		y += lh
	}
	y += a.px(6)

	bx := x + a.badge(strings.ToUpper(r.Result[:1])+r.Result[1:], resultColour(r), x, y) + a.px(8)
	meta := r.Started.Local().Format("Mon Jan 2 2006, 3:04:05 PM") + "  ·  " + shortDuration(r.Duration())
	a.text(a.ellipsize(meta, w-(bx-x), 12, false), bx, y+a.px(3), 12, false, colMuted)
	y += a.lineHeight(12, false) + a.px(18)

	// What can be done with it: each a doszer command.
	bxx := x
	bxx += a.button("Transcript", bxx, y, btnNormal, func() {
		a.runDoszer("Transcript", []string{"logs", "--format", "plain", "--transcript", r.ID}, nil)
	}) + a.px(4)
	bxx += a.button("Export…", bxx, y, btnNormal, func() { a.exportRun(r) }) + a.px(4)
	if canOpen() {
		a.button("Open folder", bxx, y, btnNormal, func() { a.open(r.Folder()) })
	}
	y += a.px(32) + a.px(18)

	if r.Error != "" {
		for _, l := range a.wrapAny(r.Error, w, 13) {
			a.text(l, x, y, 13, false, colError)
			y += lh
		}
		y += a.px(10)
	}

	row := func(label, value string, c rgba) {
		lw := a.px(110)
		a.text(label, x, y, 13, false, colMuted)
		for i, l := range a.wrapAny(value, w-lw, 13) {
			if i > 0 {
				y += lh
			}
			a.text(l, x+lw, y, 13, false, c)
		}
		y += lh
	}
	section := func(title string) {
		y += a.px(10)
		y = a.heading(title, x, y)
	}

	if r.Source != "" {
		row("Source", r.Source, colText)
	}
	if r.Archive != "" {
		row("Archive", r.Archive, colText)
	}
	row("In", r.Dir, colText)
	var vs []string
	for _, k := range []string{"doszer", "dosbox-pure", "redumper"} {
		if v := r.Versions[k]; v != "" {
			vs = append(vs, k+" "+v)
		}
	}
	if len(vs) > 0 {
		row("Versions", strings.Join(vs, ", "), colText)
	}

	if len(r.Steps) > 0 {
		section("Steps")
		for _, s := range r.Steps {
			c := colText
			if !s.OK {
				c = colError
			}
			d := shortDuration(time.Duration(s.Seconds * float64(time.Second)))
			dw := a.textWidth(d, 13, false)
			a.text(a.ellipsize(s.Name, w-dw-a.px(16), 13, false), x, y, 13, false, c)
			a.textRight(d, x+w, y, 13, false, colMuted)
			y += lh
		}
	}
	if len(r.Decisions) > 0 {
		section("Chosen")
		for _, d := range r.Decisions {
			row(d.What, d.Choice, colText)
			if d.Why != "" {
				y -= a.px(4)
				for _, l := range a.wrap(d.Why, w-a.px(110), 12) {
					a.text(l, x+a.px(110), y, 12, false, colMuted)
					y += a.lineHeight(12, false) + a.px(4)
				}
				y += a.px(4)
			}
		}
	}
	if len(r.Warnings) > 0 {
		section("Warnings")
		for _, wn := range r.Warnings {
			for _, l := range a.wrapAny(wn, w, 13) {
				a.text(l, x, y, 13, false, colShell)
				y += lh
			}
		}
	}
	if d := r.Dump; d != nil {
		section("Dump")
		if d.Cue != "" {
			row("Image", d.Cue, colText)
		}
		switch {
		case d.Verified:
			row("Redump", "Verified: "+d.Redump, colDOS)
		case d.Redump != "":
			row("Redump", fmt.Sprintf("Not verified: closest %s (%d matching)", d.Redump, d.Matching), colShell)
		case d.Datfile != "":
			row("Redump", "No match", colShell)
		}
		if d.Datfile != "" {
			row("Datfile", d.Datfile, colMuted)
		}
		for _, t := range d.Tracks {
			row(filepath.Base(t.Path), t.SHA1, colMuted)
		}
		for _, e := range d.Errors {
			row("Read error", e, colError)
		}
	}
	if in := r.Install; in != nil {
		section("Install")
		var added, changed, deleted int
		for _, c := range in.Changes {
			switch c.Kind {
			case "added":
				added++
			case "modified":
				changed++
			case "deleted":
				deleted++
			}
		}
		if in.Drive != "" {
			row("Changes", fmt.Sprintf("%s %d added, %d changed, %d deleted", in.Drive, added, changed, deleted), colText)
		}
		if len(in.Extracted) > 0 || in.Dir != "" {
			row("Extracted", fmt.Sprintf("%s from %s\\%s (%d already identical)", plural(len(in.Extracted), "file"), in.Drive, strings.ReplaceAll(in.Dir, "/", `\`), len(in.Unchanged)), colText)
		}
		for _, p := range in.Programs {
			row("Program", p, colText)
		}
		if in.Registry != "" {
			row("Registry", in.Registry, colText)
		}
		if len(in.Folders) > 0 {
			row("Folders", strings.Join(in.Folders, ", "), colShell)
		}
	}
	var entries *runlog.Archive // drawn last: there may be thousands
	if b := r.Build; b != nil {
		for _, m := range b.Media {
			section("Disc " + m.ID)
			if m.RedumpName != "" {
				row("Redump", m.RedumpName, colText)
			}
			if m.Image != "" {
				row("Image", m.Image, colText)
			} else {
				row("Image", "none matched", colError)
			}
			for i, t := range m.Tracks {
				row(fmt.Sprintf("Track %d", i+1), t.Type+"  "+t.SHA1, colMuted)
			}
		}
		if len(b.Input) > 0 || len(b.Trimmed)+len(b.Moved)+len(b.Rewrites)+len(b.Converted) > 0 {
			section("Source")
			var n int64
			for _, f := range b.Input {
				n += f.Size
			}
			row("Collected", plural(len(b.Input), "file")+", "+size(n), colText)
			if len(b.Trimmed) > 0 {
				row("Trimmed", plural(len(b.Trimmed), "file"), colText)
			}
			if len(b.Moved) > 0 {
				row("Flattened", plural(len(b.Moved), "path"), colText)
			}
			for _, rw := range b.Rewrites {
				row("Rewrote", fmt.Sprintf("%s → %s in %s", rw.From, rw.To, plural(len(rw.Files), "file")), colText)
			}
			for _, c := range b.Converted {
				row("Converted", fmt.Sprintf("%s to %s", c.Cue, plural(len(c.Files), "file")), colText)
			}
		}
		if ar := b.Archive; ar != nil {
			section("Archive")
			row("Size", fmt.Sprintf("%s, %s of content (%.0f%%)", size(ar.Size), size(ar.Content), ar.Ratio()*100), colText)
			row("Entries", itoa(len(ar.Entries)), colText)
			row("SHA-1", ar.SHA1, colText)
			row("CRC-32", ar.CRC32, colText)
			entries = ar
		}
	}

	if all := runlog.History(a.runs, r); len(all) > 1 {
		section("History")
		for _, h := range all {
			hx, hw := x-a.px(6), w+a.px(12)
			if h == r {
				a.round(hx, y-a.px(3), hw, lh, a.px(5), colCardSel)
			} else if a.hovered(hx, y-a.px(3), hw, lh) {
				a.round(hx, y-a.px(3), hw, lh, a.px(5), colCardHover)
			}
			d := a.px(8)
			a.round(x, y+(lh-d)/2-a.px(3), d, d, d/2, resultColour(h))
			when := h.Started.Local().Format("Jan 2, 3:04 PM")
			a.text(when, x+a.px(16), y, 13, false, colMuted)
			a.text(a.ellipsize(h.CommandLine(), w-a.px(150), 13, false), x+a.px(140), y, 13, false, colText)
			id := h.ID
			a.clipHit(hx, y-a.px(3), hw, lh, func(bool) { a.showRun(id) })
			y += lh
		}
	}
	if len(r.Files) > 0 {
		section("Files")
		for _, f := range r.Files {
			fw := a.textWidth(f, 13, false)
			c := colText
			if canOpen() {
				if a.hovered(x, y, fw, lh) {
					c = colAccent
				}
				p := filepath.Join(r.Folder(), f)
				a.clipHit(x, y, fw, lh, func(bool) { a.open(p) })
			}
			a.text(f, x, y, 13, false, c)
			y += lh
		}
	}
	if entries != nil {
		section("Entries")
		y = a.drawEntries(entries, x, y, w)
	}
	return y
}

// wrapMono breaks s into lines of the monospaced face at most width
// pixels wide: at a space if one is near the end, else anywhere.
func (a *app) wrapMono(s string, width int32) []string {
	n := max(8, int(width/a.monoWidth()))
	var out []string
	r := []rune(s)
	for len(r) > n {
		cut := n
		for i := n; i > n*2/3; i-- {
			if r[i] == ' ' {
				cut = i + 1
				break
			}
		}
		out = append(out, string(r[:cut]))
		r = r[cut:]
	}
	return append(out, string(r))
}

// drawEntries draws an archive's entries, the largest first: size,
// compressed size, ratio and method.
func (a *app) drawEntries(ar *runlog.Archive, x, y, w int32) int32 {
	es := append([]runlog.Entry(nil), ar.Entries...)
	sort.SliceStable(es, func(i, j int) bool { return es[i].Size > es[j].Size })
	lh := a.lineHeight(12, false) + a.px(5)
	cols := []int32{a.px(84), a.px(84), a.px(52), a.px(60)} // size, compressed, ratio, method
	var colsW int32
	for _, c := range cols {
		colsW += c
	}
	head := func(label string, cx int32) { a.textRight(label, cx, y, 12, true, colMuted) }
	a.text("Path", x, y, 12, true, colMuted)
	cx := x + w - colsW
	for i, l := range []string{"Size", "Packed", "Ratio", "Method"} {
		cx += cols[i]
		head(l, cx)
	}
	y += lh
	_, ay, _, ah := a.area()
	for _, e := range es {
		if y+lh >= ay && y <= ay+ah {
			a.text(a.ellipsize(e.Path, w-colsW-a.px(8), 12, false), x, y, 12, false, colText)
			ratio := "-"
			if e.Size > 0 {
				ratio = fmt.Sprintf("%.0f%%", float64(e.Compressed)*100/float64(e.Size))
			}
			cx := x + w - colsW
			for i, v := range []string{size(e.Size), size(e.Compressed), ratio, e.Method} {
				cx += cols[i]
				a.textRight(v, cx, y, 12, false, colMuted)
			}
		}
		y += lh
	}
	return y
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
