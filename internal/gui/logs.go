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

// The Logs page: the runs doszer recorded (runlog), newest first, and
// the selected one's report - what it ran, chose, warned about and
// wrote - with its history: the runs about the same game.

// logs is the Logs page's state.
type logs struct {
	runs        []*runlog.Report // newest first; nil until read
	logsCh      chan []*runlog.Report
	logsReading bool
	logSel      int    // index into runs
	logWant     string // a run to select once the logs are read
	logScroll   int32  // the report's scroll, pixels
	logReportH  int32  // the report's height, as last drawn
	logSplit    int32  // where the report starts, as last drawn
}

func (a *app) initLogs() {
	a.logsCh = make(chan []*runlog.Report, 1)
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
		a.runs = []*runlog.Report{}
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

// logsRead takes the runs read, keeping the selected run selected.
func (a *app) logsRead(runs []*runlog.Report) {
	a.logsReading = false
	want := a.logWant
	if want == "" && a.logSel < len(a.runs) {
		want = a.runs[a.logSel].ID
	}
	a.runs, a.logSel, a.logWant = runs, 0, ""
	for i, r := range runs {
		if r.ID == want {
			a.logSel = i
		}
	}
	a.dirty = true
}

// showRun shows the run with ID id in Logs, selected.
func (a *app) showRun(id string) {
	a.show(pageLogs)
	a.logScroll = 0
	for i, r := range a.runs {
		if r.ID == id {
			a.logSel = i
			a.revealRun()
			return
		}
	}
	a.logWant = id
}

func (a *app) selectedRun() *runlog.Report {
	if a.logSel < len(a.runs) {
		return a.runs[a.logSel]
	}
	return nil
}

func (a *app) logRowH() int32 { return a.px(58) }

func (a *app) logsH() int32 {
	return a.pad() + int32(len(a.runs))*(a.logRowH()+a.rowGap()) + a.pad()
}

// revealRun scrolls the list to the selected run.
func (a *app) revealRun() {
	_, _, _, ah := a.area()
	top := a.pad() + int32(a.logSel)*(a.logRowH()+a.rowGap())
	s := &a.scroll[pageLogs]
	if top < *s {
		*s = top - a.pad()
	} else if top+a.logRowH()+a.pad() > *s+ah {
		*s = top + a.logRowH() + a.pad() - ah
	}
	*s = max(0, *s)
}

func (a *app) logsKey(sc int32) {
	switch sc {
	case scUp:
		a.logSel = max(0, a.logSel-1)
	case scDown:
		a.logSel = min(len(a.runs)-1, a.logSel+1)
	case scPageUp:
		a.logSel = max(0, a.logSel-8)
	case scPageDown:
		a.logSel = min(len(a.runs)-1, a.logSel+8)
	case scHome:
		a.logSel = 0
	case scEnd:
		a.logSel = len(a.runs) - 1
	default:
		return
	}
	a.logSel = max(0, a.logSel)
	a.logScroll = 0
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
	for _, r := range a.runs {
		if r.Result == runlog.ResultFailed {
			failed++
		}
	}
	s := plural(len(a.runs), "run")
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
	switch r.Result {
	case runlog.ResultOK:
		if len(r.Warnings) > 0 {
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
	d := shortDuration(r.Duration())
	switch r.Result {
	case runlog.ResultOK:
		if n := len(r.Warnings); n > 0 {
			return plural(n, "warning") + ", " + d
		}
		return d
	case runlog.ResultFailed:
		return "failed after " + d
	case runlog.ResultInterrupted:
		return "stopped after " + d
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

	// The runs.
	rh, gap := a.logRowH(), a.rowGap()
	for i, r := range a.runs {
		x, y, w := ax+p, ay+p+int32(i)*(rh+gap)-a.scroll[pageLogs], listW
		if y+rh < ay || y > ay+ah {
			continue
		}
		a.card(x, y, w, rh, i == a.logSel, a.hovered(x, y, w, rh))
		idx := i
		a.clipHit(x, y, w, rh, func(bool) {
			a.logSel, a.logScroll = idx, 0
		})
		d := a.px(10)
		a.round(x+a.px(14), y+(rh-d)/2, d, d, d/2, resultColour(r))
		tx := x + a.px(34)
		when := r.Started.Local().Format("Jan 2, 3:04 PM")
		ww := a.textWidth(when, 12, false)
		a.text(a.ellipsize(runSubject(r), w-(tx-x)-ww-a.px(24), 14, true), tx, y+a.px(10), 14, true, colText)
		a.textRight(when, x+w-a.px(12), y+a.px(12), 12, false, colMuted)
		line := r.Command + "  ·  " + resultLabel(r)
		a.text(a.ellipsize(line, w-(tx-x)-a.px(12), 12, false), tx, y+a.px(32), 12, false, colMuted)
	}

	// The selected run's report.
	r := a.selectedRun()
	if r == nil {
		return
	}
	rx, rw := a.logSplit, ax+aw-p-a.logSplit
	a.pushClip(rx, ay, rw+p, ah)
	defer a.popClip()
	y := ay + p - a.logScroll
	y = a.drawRunReport(r, rx, y, rw)
	a.logReportH = y + a.logScroll - ay + p
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
			row("Changes", fmt.Sprintf("%s: %d added, %d changed, %d deleted", in.Drive, added, changed, deleted), colText)
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

// exportRun packs r's history into a ZIP in a folder the user picks:
// doszer logs --export, redacted.
func (a *app) exportRun(r *runlog.Report) {
	start, _ := os.UserHomeDir()
	dir, ok := pickFolder("export", start)
	if !ok {
		return
	}
	name := safeName(runSubject(r))
	if name == "" {
		name = r.Command
	}
	out := filepath.Join(dir, "doszer-logs-"+name+"-"+r.Started.Format("20060102-150405")+".zip")
	args := []string{"logs", "--format", "plain", "--force", "--export", out}
	if r.Source != "" {
		args = append(args, r.Source)
	} else {
		args = append(args, r.ID)
	}
	a.runDoszer("Export logs", args, nil)
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
