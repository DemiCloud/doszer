package gui

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/DemiCloud/doszer/internal/runlog"
)

// The Logs page's reports: a run's, and a session's summary. All their
// text is one selectable region: copied, a label and its value are a tab
// apart, and a wrapped line is one line again.

const reportRegion = "report"

// reporter draws a report down a column, from y.
type reporter struct {
	a       *app
	x, y, w int32
	lh      int32 // a line of 13-pixel text, with its spacing
}

func (a *app) newReporter(x, y, w int32) *reporter {
	return &reporter{a: a, x: x, y: y, w: w, lh: a.lineHeight(13, false) + a.px(6)}
}

// labelW is the width of a row's label column.
func (rp *reporter) labelW() int32 { return rp.a.px(110) }

// text draws s, wrapped to w from x, selectable: its first line after
// sep, the rest as its wrapping.
func (rp *reporter) text(sep, s string, x, w int32, size float64, bold bool, c rgba) {
	lh := rp.a.lineHeight(size, bold) + rp.a.px(6)
	for i, l := range rp.a.wrapKeep(s, w, size, bold) {
		if i > 0 {
			sep = sepWrap
		}
		rp.a.stext(reportRegion, sep, l, x, rp.y, size, bold, c)
		rp.y += lh
	}
}

// line draws a line of its own, wrapped to the column.
func (rp *reporter) line(s string, c rgba) { rp.text(sepLine, s, rp.x, rp.w, 13, false, c) }

// title draws the report's title.
func (rp *reporter) title(s string) {
	rp.text(sepLine, s, rp.x, rp.w, 18, true, colText)
	rp.y += rp.a.px(2)
}

// heading starts a section.
func (rp *reporter) heading(s string) {
	rp.y += rp.a.px(10)
	rp.a.stext(reportRegion, sepLine, s, rp.x, rp.y, 15, true, colText)
	rp.y += rp.a.lineHeight(15, true) + rp.a.px(8)
}

// row draws a label and its value beside it, wrapped.
func (rp *reporter) row(label, value string, c rgba) {
	rp.a.stext(reportRegion, sepLine, label, rp.x, rp.y, 13, false, colMuted)
	rp.text(sepColumn, value, rp.x+rp.labelW(), rp.w-rp.labelW(), 13, false, c)
}

// note draws a muted line under the last row's value: why it was chosen.
func (rp *reporter) note(s string) {
	rp.y -= rp.a.px(4)
	rp.text(sepColumn, s, rp.x+rp.labelW(), rp.w-rp.labelW(), 12, false, colMuted)
	rp.y += rp.a.px(2)
}

// pair draws s on the left and right at the column's right edge.
func (rp *reporter) pair(s, right string, c rgba) {
	a := rp.a
	rw := a.textWidth(right, 13, false)
	a.stext(reportRegion, sepLine, a.ellipsize(s, rp.w-rw-a.px(16), 13, false), rp.x, rp.y, 13, false, c)
	a.stext(reportRegion, sepColumn, right, rp.x+rp.w-rw, rp.y, 13, false, colMuted)
	rp.y += rp.lh
}

// gap adds space.
func (rp *reporter) gap(n float64) { rp.y += rp.a.px(n) }

// words are a string's words with the spaces after them, for wrapping
// that keeps every character.
var words = regexp.MustCompile(`\S+\s*|\s+`)

// wrapKeep breaks s into lines at most width pixels wide whose
// concatenation is s: at spaces, kept at the end of a line, and a word
// too long for a line (a path) anywhere.
func (a *app) wrapKeep(s string, width int32, size float64, bold bool) []string {
	var out []string
	for pi, para := range strings.Split(s, "\n") {
		if pi > 0 {
			out[len(out)-1] += "\n"
		}
		line := ""
		for _, w := range words.FindAllString(para, -1) {
			if a.textWidth(strings.TrimRight(line+w, " "), size, bold) <= width {
				line += w
				continue
			}
			if line != "" {
				out = append(out, line)
				line = ""
			}
			for a.textWidth(strings.TrimRight(w, " "), size, bold) > width {
				r := []rune(w)
				n := len(r) - 1
				for n > 1 && a.textWidth(string(r[:n]), size, bold) > width {
					n--
				}
				out = append(out, string(r[:n]))
				w = string(r[n:])
			}
			line = w
		}
		out = append(out, line)
	}
	return out
}

// wrapMono breaks s into lines of the monospaced face at most width
// pixels wide, whose concatenation is s: after a space if one is near
// the end, else anywhere.
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

// header draws a report's title, command line (or summary line), result
// and time.
func (rp *reporter) header(title, command string, result string, colour rgba, meta string) {
	a := rp.a
	rp.title(title)
	if command != "" {
		for i, l := range a.wrapMono(command, rp.w) {
			sep := sepWrap
			if i == 0 {
				sep = sepLine
			}
			a.smono(reportRegion, sep, l, rp.x, rp.y+a.px(2), colMuted)
			rp.y += rp.lh
		}
	}
	rp.gap(6)
	bx := rp.x + a.badge(strings.ToUpper(result[:1])+result[1:], colour, rp.x, rp.y) + a.px(8)
	a.stext(reportRegion, sepLine, a.ellipsize(meta, rp.w-(bx-rp.x), 12, false), bx, rp.y+a.px(3), 12, false, colMuted)
	rp.y += a.lineHeight(12, false) + a.px(18)
}

// drawRunReport draws r's report from y in a column w wide, returning
// the y below it.
func (a *app) drawRunReport(r *runlog.Report, x, y, w int32) int32 {
	rp := a.newReporter(x, y, w)
	rp.header(runSubject(r), r.CommandLine(), r.Result, resultColour(r),
		r.Started.Local().Format("Mon Jan 2 2006, 3:04:05 PM")+"  ·  "+shortDuration(r.Duration()))

	// What can be done with it: each a doszer command.
	bx := x
	bx += a.button("Transcript", bx, rp.y, btnNormal, func() {
		a.runDoszer("Transcript", []string{"logs", "--transcript", r.ID}, nil)
	}) + a.px(4)
	bx += a.button("Export…", bx, rp.y, btnNormal, func() { a.exportRun(r) }) + a.px(4)
	if canOpen() {
		bx += a.button("Open folder", bx, rp.y, btnNormal, func() { a.open(r.Folder()) }) + a.px(4)
	}
	if a.inSources(r.Source) {
		if fi, err := os.Stat(r.Source); err == nil && fi.IsDir() {
			src := r.Source
			a.button("Source", bx, rp.y, btnNormal, func() { a.showSource(src) })
		}
	}
	rp.gap(50)

	if r.Error != "" {
		rp.line(r.Error, colError)
		rp.gap(10)
	}
	if r.Source != "" {
		rp.row("Source", r.Source, colText)
	}
	if r.Archive != "" {
		rp.row("Archive", r.Archive, colText)
	}
	rp.row("In", r.Dir, colText)
	var vs []string
	for _, k := range []string{"doszer", "dosbox-pure", "redumper"} {
		if v := r.Versions[k]; v != "" {
			vs = append(vs, k+" "+v)
		}
	}
	if len(vs) > 0 {
		rp.row("Versions", strings.Join(vs, ", "), colText)
	}

	if len(r.Steps) > 0 {
		rp.heading("Steps")
		for _, s := range r.Steps {
			c := colText
			if !s.OK {
				c = colError
			}
			rp.pair(s.Name, shortDuration(time.Duration(s.Seconds*float64(time.Second))), c)
		}
	}
	rp.decisions(r.Decisions)
	rp.warnings(r.Warnings)

	if d := r.Dump; d != nil {
		rp.heading("Dump")
		if d.Cue != "" {
			rp.row("Image", d.Cue, colText)
		}
		switch {
		case d.Verified:
			rp.row("Redump", "Verified: "+d.Redump, colDOS)
		case d.Redump != "":
			rp.row("Redump", fmt.Sprintf("Not verified: closest %s (%d matching)", d.Redump, d.Matching), colShell)
		case d.Datfile != nil:
			rp.row("Redump", "No match", colShell)
		}
		if d.Datfile != nil {
			rp.row("Datfile", d.Datfile.String(), colMuted)
		}
		for _, t := range d.Tracks {
			rp.row(filepath.Base(t.Path), t.SHA1, colMuted)
		}
		for _, e := range d.Errors {
			rp.row("Read error", e, colError)
		}
	}
	if in := r.Install; in != nil {
		rp.heading("Install")
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
			rp.row("Changes", fmt.Sprintf("%s %d added, %d changed, %d deleted", in.Drive, added, changed, deleted), colText)
		}
		if len(in.Extracted) > 0 || in.Dir != "" {
			rp.row("Extracted", fmt.Sprintf("%s from %s\\%s (%d already identical)", plural(len(in.Extracted), "file"), in.Drive, strings.ReplaceAll(in.Dir, "/", `\`), len(in.Unchanged)), colText)
		}
		for _, p := range in.Programs {
			rp.row("Program", p, colText)
		}
		if in.Registry != "" {
			rp.row("Registry", in.Registry, colText)
		}
		if len(in.Folders) > 0 {
			rp.row("Folders", strings.Join(in.Folders, ", "), colShell)
		}
	}
	var entries *runlog.Archive // drawn last: there may be thousands
	if b := r.Build; b != nil {
		for _, m := range b.Media {
			rp.heading("Disc " + m.ID)
			if m.RedumpName != "" {
				rp.row("Redump", m.RedumpName, colText)
			}
			if m.Image != "" {
				rp.row("Image", m.Image, colText)
			} else {
				rp.row("Image", "none matched", colError)
			}
			for i, t := range m.Tracks {
				rp.row(fmt.Sprintf("Track %d", i+1), t.Type+"  "+t.SHA1, colMuted)
			}
		}
		if len(b.Input) > 0 || len(b.Trimmed)+len(b.Moved)+len(b.Rewrites)+len(b.Converted) > 0 {
			rp.heading("Source")
			var n int64
			for _, f := range b.Input {
				n += f.Size
			}
			rp.row("Collected", plural(len(b.Input), "file")+", "+size(n), colText)
			if len(b.Trimmed) > 0 {
				rp.row("Trimmed", plural(len(b.Trimmed), "file"), colText)
			}
			if len(b.Moved) > 0 {
				rp.row("Flattened", plural(len(b.Moved), "path"), colText)
			}
			for _, rw := range b.Rewrites {
				rp.row("Rewrote", fmt.Sprintf("%s → %s in %s", rw.From, rw.To, plural(len(rw.Files), "file")), colText)
			}
			for _, c := range b.Converted {
				rp.row("Converted", fmt.Sprintf("%s to %s", c.Cue, plural(len(c.Files), "file")), colText)
			}
		}
		if ar := b.Archive; ar != nil {
			rp.archive(ar, false)
			entries = ar
		}
	}

	// A run of its own has a history; a session's run leaves it to the
	// session.
	if r.Session == "" {
		var others []*runGroup
		for _, h := range runlog.History(a.runs, r) {
			if h != r {
				others = append(others, a.groupOf(h))
			}
		}
		rp.history(others)
	}
	if len(r.Files) > 0 {
		rp.heading("Files")
		for _, f := range r.Files {
			fw := a.textWidth(f, 13, false)
			c := colText
			if canOpen() {
				if a.hovered(x, rp.y, fw, rp.lh) {
					c = colAccent
				}
				p := filepath.Join(r.Folder(), f)
				a.clipHit(x, rp.y, fw, rp.lh, func(bool) { a.open(p) })
			}
			a.text(f, x, rp.y, 13, false, c) // a link: clicked, not selected
			rp.y += rp.lh
		}
	}
	if entries != nil {
		rp.heading("Entries")
		rp.entries(entries)
	}
	return rp.y
}

// drawSessionReport draws a session's summary: its commands, each
// opening its report, what was chosen, warnings, the archive it ended
// with, and the game's other runs.
func (a *app) drawSessionReport(g *runGroup, x, y, w int32) int32 {
	rp := a.newReporter(x, y, w)
	last := g.result()
	end := last.Started.Add(last.Duration())
	rp.header(g.subject(), "", last.Result, resultColourOf(last.Result, g.warnings()),
		g.first().Started.Local().Format("Mon Jan 2 2006, 3:04:05 PM")+" to "+end.Local().Format("3:04:05 PM")+
			"  ·  "+shortDuration(g.running())+" running")

	key := g.key
	bx := x
	bx += a.button("Transcript", bx, rp.y, btnNormal, func() {
		a.runDoszer("Transcript", []string{"logs", "--transcript", key}, nil)
	}) + a.px(4)
	a.button("Export…", bx, rp.y, btnNormal, func() { a.exportLogs(g.subject(), g.first().Started, key) })
	rp.gap(50)

	if last.Error != "" {
		rp.line(last.Error, colError)
	}
	rp.heading("Commands")
	for _, r := range g.runs {
		// Each a link to its report (clicked, not selected: its report
		// has the command line to select), in the region's text for
		// Copy all.
		lh := rp.lh
		hx, hw := x-a.px(6), w+a.px(12)
		if a.hovered(hx, rp.y-a.px(3), hw, lh) {
			a.round(hx, rp.y-a.px(3), hw, lh, a.px(5), colCardHover)
		}
		d := a.px(8)
		a.round(x, rp.y+(lh-d)/2-a.px(3), d, d, d/2, resultColour(r))
		rl := resultLabel(r)
		rlw := a.textWidth(rl, 12, false)
		cmd := r.CommandLine()
		shown := a.ellipsize(cmd, w-rlw-a.px(36), 13, false)
		a.selAdd(reportRegion, cmd, sepLine)
		a.text(shown, x+a.px(16), rp.y, 13, false, colText)
		a.textRight(rl, x+w, rp.y+a.px(1), 12, false, colMuted)
		id := r.ID
		a.clipHit(hx, rp.y-a.px(3), hw, lh, func(bool) {
			a.logRun, a.logScroll = id, 0
			a.logOpen[key] = true
		})
		rp.y += lh
	}
	var chosen []runlog.Decision
	var warnings []string
	for _, r := range g.runs {
		chosen = append(chosen, r.Decisions...)
		warnings = append(warnings, r.Warnings...)
	}
	rp.decisions(chosen)
	rp.warnings(warnings)
	for i := len(g.runs) - 1; i >= 0; i-- {
		if b := g.runs[i].Build; b != nil && b.Archive != nil {
			rp.archive(b.Archive, true)
			break
		}
	}

	// The game's runs outside this session: earlier attempts, a later
	// repack.
	in := map[*runlog.Report]bool{}
	for _, r := range g.runs {
		in[r] = true
	}
	var others []*runGroup
	seen := map[string]bool{g.key: true}
	for _, h := range runlog.History(a.runs, g.first()) {
		if in[h] {
			continue
		}
		if og := a.groupOf(h); og != nil && !seen[og.key] {
			seen[og.key] = true
			others = append(others, og)
		}
	}
	rp.history(others)
	return rp.y
}

// groupOf is the list item r is in.
func (a *app) groupOf(r *runlog.Report) *runGroup {
	for _, g := range a.groups {
		for _, o := range g.runs {
			if o == r {
				return g
			}
		}
	}
	return nil
}

func (rp *reporter) decisions(ds []runlog.Decision) {
	if len(ds) == 0 {
		return
	}
	rp.heading("Chosen")
	for _, d := range ds {
		rp.row(d.What, d.Choice, colText)
		if d.Why != "" {
			rp.note(d.Why)
		}
	}
}

func (rp *reporter) warnings(ws []string) {
	if len(ws) == 0 {
		return
	}
	rp.heading("Warnings")
	for _, w := range ws {
		rp.line(w, colShell)
	}
}

func (rp *reporter) archive(ar *runlog.Archive, path bool) {
	rp.heading("Archive")
	if path {
		rp.row("Path", ar.Path, colText)
	}
	rp.row("Size", fmt.Sprintf("%s, %s of content (%.0f%%)", size(ar.Size), size(ar.Content), ar.Ratio()*100), colText)
	rp.row("Entries", itoa(len(ar.Entries)), colText)
	rp.row("SHA-1", ar.SHA1, colText)
	rp.row("CRC-32", ar.CRC32, colText)
}

// history lists the game's other items, oldest first, each a link to
// it.
func (rp *reporter) history(gs []*runGroup) {
	if len(gs) == 0 {
		return
	}
	a := rp.a
	sort.SliceStable(gs, func(i, j int) bool { return gs[i].first().Started.Before(gs[j].first().Started) })
	rp.heading("History")
	for _, g := range gs {
		lh := rp.lh
		hx, hw := rp.x-a.px(6), rp.w+a.px(12)
		if a.hovered(hx, rp.y-a.px(3), hw, lh) {
			a.round(hx, rp.y-a.px(3), hw, lh, a.px(5), colCardHover)
		}
		last := g.result()
		d := a.px(8)
		a.round(rp.x, rp.y+(lh-d)/2-a.px(3), d, d, d/2, resultColourOf(last.Result, g.warnings()))
		a.text(g.first().Started.Local().Format("Jan 2, 3:04 PM"), rp.x+a.px(16), rp.y, 13, false, colMuted)
		what := last.CommandLine()
		if g.session() {
			what = g.commands()
		}
		a.text(a.ellipsize(what, rp.w-a.px(150), 13, false), rp.x+a.px(140), rp.y, 13, false, colText)
		key := g.key
		a.clipHit(hx, rp.y-a.px(3), hw, lh, func(bool) {
			a.logGroup, a.logRun, a.logScroll = key, "", 0
			a.revealRun()
		})
		rp.y += lh
	}
}

// entries draws an archive's entries, the largest first: size,
// compressed size, ratio and method, each row selectable.
func (rp *reporter) entries(ar *runlog.Archive) {
	a := rp.a
	es := append([]runlog.Entry(nil), ar.Entries...)
	sort.SliceStable(es, func(i, j int) bool { return es[i].Size > es[j].Size })
	lh := a.lineHeight(12, false) + a.px(5)
	cols := []int32{a.px(84), a.px(84), a.px(52), a.px(60)} // size, compressed, ratio, method
	var colsW int32
	for _, c := range cols {
		colsW += c
	}
	x, w := rp.x, rp.w
	cell := func(sep, s string, right int32, bold bool, c rgba) {
		a.stext(reportRegion, sep, s, right-a.textWidth(s, 12, bold), rp.y, 12, bold, c)
	}
	a.stext(reportRegion, sepLine, "Path", x, rp.y, 12, true, colMuted)
	cx := x + w - colsW
	for i, l := range []string{"Size", "Packed", "Ratio", "Method"} {
		cx += cols[i]
		cell(sepColumn, l, cx, true, colMuted)
	}
	rp.y += lh
	_, ay, _, ah := a.area()
	for _, e := range es {
		ratio := "-"
		if e.Size > 0 {
			ratio = fmt.Sprintf("%.0f%%", float64(e.Compressed)*100/float64(e.Size))
		}
		vals := []string{size(e.Size), size(e.Compressed), ratio, e.Method}
		if rp.y+lh < ay || rp.y > ay+ah {
			// Off screen: in the region's text, not drawn.
			a.selAdd(reportRegion, e.Path, sepLine)
			for _, v := range vals {
				a.selAdd(reportRegion, v, sepColumn)
			}
			rp.y += lh
			continue
		}
		a.stext(reportRegion, sepLine, a.ellipsize(e.Path, w-colsW-a.px(8), 12, false), x, rp.y, 12, false, colText)
		a.selText[reportRegion][len(a.selText[reportRegion])-1] = e.Path // copied whole
		cx := x + w - colsW
		for i, v := range vals {
			cx += cols[i]
			cell(sepColumn, v, cx, false, colMuted)
		}
		rp.y += lh
	}
}
