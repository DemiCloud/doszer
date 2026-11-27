package gui

import (
	"sort"
	"time"

	"github.com/DemiCloud/doszer/internal/library"
	"github.com/DemiCloud/doszer/internal/runlog"
)

// The Dashboard: the collection at a glance. Everything on it is the
// games', the system folder's and the logs' own data, and each figure
// leads to where it comes from: a page, the games filtered to it, or a
// run's log.

// Problem filters the Dashboard counts, besides filterMissing and
// filterBroken.
const (
	filterUnmet      = "Windows too old"
	filterNoIcon     = "No icon"
	filterUnreadable = "Unreadable"
)

// problems are the problem filters, in the Dashboard's order.
var problems = []string{filterUnreadable, filterBroken, filterMissing, filterUnmet, filterNoIcon}

// problem reports whether g has the problem f.
func (a *app) problem(g *library.Game, f string) bool {
	switch f {
	case filterUnreadable:
		return g.Err != ""
	case filterBroken:
		return g.Broken != ""
	case filterMissing:
		return g.Boot != "" && a.files != nil && !a.hasSystem(g.Boot, g.Kind == library.KindShell)
	case filterUnmet:
		_, bad := a.needBadge(g)
		return bad
	case filterNoIcon:
		return g.Err == "" && g.Icon == nil
	}
	return false
}

// showGame shows g in Games, selected, with no filter or search.
func (a *app) showGame(path string) {
	a.show(pageGames)
	a.filter = ""
	a.search.set("")
	a.refilter(path)
	a.reveal()
}

// showFilter shows Games filtered by f.
func (a *app) showFilter(f string) {
	a.show(pageGames)
	a.search.set("")
	a.Filter(f)
}

func (a *app) dashboardStatus() string {
	if a.scanning {
		return ""
	}
	n := 0
	for _, f := range problems {
		for i := range a.list {
			if a.problem(&a.list[i], f) {
				n++
			}
		}
	}
	n += len(a.failedBuilds())
	if n == 0 {
		return "No problems"
	}
	return plural(n, "problem")
}

func (a *app) drawDashboard() {
	ax, ay, aw, _ := a.area()
	p := a.pad()
	x, y, w := ax+p, ay+p-a.scroll[pageDashboard], aw-2*p
	gap := a.px(12)

	// Tiles: the totals, each opening its page.
	var total int64
	var played time.Duration
	for _, g := range a.list {
		total += g.Size
		played += g.PlayTime
	}
	systems := 0
	for _, f := range a.files {
		if !f.missing {
			systems++
		}
	}
	tiles := []struct {
		label, value string
		fn           func()
	}{
		{"Games", itoa(len(a.list)), func() { a.showFilter("") }},
		{"Parents", itoa(len(a.parentList)), func() { a.show(pageParents) }},
		{"Systems", itoa(systems), func() { a.show(pageSystems) }},
		{"Size", size(total), func() { a.show(pageGames); a.sort = sortSize; a.refilter(a.keepPath()) }},
		{"Played", duration(played), func() { a.show(pageGames); a.sort = sortPlayTime; a.refilter(a.keepPath()) }},
	}
	if played == 0 {
		tiles[4].value = "-"
	}
	tw := (w - gap*int32(len(tiles)-1)) / int32(len(tiles))
	th := a.px(84)
	for i, t := range tiles {
		tx := x + int32(i)*(tw+gap)
		a.card(tx, y, tw, th, false, a.hovered(tx, y, tw, th))
		a.text(t.label, tx+a.px(16), y+a.px(14), 13, false, colMuted)
		a.text(a.ellipsize(t.value, tw-a.px(32), 22, true), tx+a.px(16), y+a.px(38), 22, true, colText)
		fn := t.fn
		a.clipHit(tx, y, tw, th, func(bool) { fn() })
	}
	y += th + gap

	// Below: problems on the left, recent games on the right.
	cw := (w - gap) / 2
	lh := a.px(36)
	rows := len(problems) + 1 // and failed builds
	ch := a.px(48) + int32(rows)*lh
	left, right := x, x+cw+gap

	// Problems: each kind with its count, opening the games it applies to.
	a.card(left, y, cw, ch, false, false)
	a.text("Problems", left+a.px(16), y+a.px(14), 15, true, colText)
	ry := y + a.px(48)
	for _, f := range problems {
		n := 0
		for i := range a.list {
			if a.problem(&a.list[i], f) {
				n++
			}
		}
		c := colFaint
		if n > 0 {
			c = colText
			if a.hovered(left+a.px(8), ry, cw-a.px(16), lh) {
				a.round(left+a.px(8), ry, cw-a.px(16), lh, a.px(6), colCardHover)
			}
			f := f
			a.clipHit(left+a.px(8), ry, cw-a.px(16), lh, func(bool) { a.showFilter(f) })
		}
		a.text(f, left+a.px(16), ry+(lh-a.lineHeight(13, false))/2, 13, false, c)
		count := itoa(n)
		cc := colFaint
		if n > 0 {
			cc = colError
			if f == filterNoIcon {
				cc = colShell
			}
		}
		a.textRight(count, left+cw-a.px(16), ry+(lh-a.lineHeight(13, true))/2, 13, true, cc)
		ry += lh
	}
	// Builds whose last run failed, opening the newest one's log.
	failed := a.failedBuilds()
	fc, cc := colFaint, colFaint
	if len(failed) > 0 {
		fc, cc = colText, colError
		if a.hovered(left+a.px(8), ry, cw-a.px(16), lh) {
			a.round(left+a.px(8), ry, cw-a.px(16), lh, a.px(6), colCardHover)
		}
		id := failed[0].ID
		a.clipHit(left+a.px(8), ry, cw-a.px(16), lh, func(bool) { a.showRun(id) })
	}
	a.text("Failed builds", left+a.px(16), ry+(lh-a.lineHeight(13, false))/2, 13, false, fc)
	a.textRight(itoa(len(failed)), left+cw-a.px(16), ry+(lh-a.lineHeight(13, true))/2, 13, true, cc)

	// Recent games: played, else built.
	recent := func(title string, x int32, less func(g, h *library.Game) bool, when func(g *library.Game) time.Time) {
		a.card(x, y, cw, ch, false, false)
		a.text(title, x+a.px(16), y+a.px(14), 15, true, colText)
		var gs []*library.Game
		for i := range a.list {
			if g := &a.list[i]; !when(g).IsZero() {
				gs = append(gs, g)
			}
		}
		sort.SliceStable(gs, func(i, j int) bool { return less(gs[i], gs[j]) })
		ry := y + a.px(48)
		if len(gs) == 0 {
			a.text("None", x+a.px(16), ry+(lh-a.lineHeight(13, false))/2, 13, false, colFaint)
		}
		for _, g := range gs[:min(rows, len(gs))] {
			rx, rw := x+a.px(8), cw-a.px(16)
			if a.hovered(rx, ry, rw, lh) {
				a.round(rx, ry, rw, lh, a.px(6), colCardHover)
			}
			is := a.px(28)
			a.drawIcon(g, rx+a.px(8), ry+(lh-is)/2, is)
			ts := when(g).Local().Format("Jan 2, 3:04 PM")
			tw := a.textWidth(ts, 12, false)
			a.text(a.ellipsize(g.Name, rw-is-tw-a.px(40), 13, false), rx+is+a.px(18), ry+(lh-a.lineHeight(13, false))/2, 13, false, colText)
			a.textRight(ts, rx+rw-a.px(8), ry+(lh-a.lineHeight(12, false))/2, 12, false, colMuted)
			path := g.Path
			a.clipHit(rx, ry, rw, lh, func(bool) { a.showGame(path) })
			ry += lh
		}
	}
	recent("Recently played", right, func(g, h *library.Game) bool { return g.LastPlayed.After(h.LastPlayed) },
		func(g *library.Game) time.Time { return g.LastPlayed })
	y += ch + gap
	if built := a.builtRuns(); len(built) > 0 {
		a.recentBuilds(built, left, y, cw, ch, lh, rows)
	} else {
		// Nothing recorded (doszer not set up, or no build since): the
		// archives' own dates.
		recent("Recently built", left, func(g, h *library.Game) bool { return g.Modified.After(h.Modified) },
			func(g *library.Game) time.Time { return g.Modified })
	}

	// What the games run on, each opening those games.
	a.card(right, y, cw, ch, false, false)
	a.text("Runs on", right+a.px(16), y+a.px(14), 15, true, colText)
	counts, colours := map[string]int{}, map[string]rgba{}
	for i := range a.list {
		l, c := kindLabel(&a.list[i])
		counts[l]++
		colours[l] = c
	}
	labels := make([]string, 0, len(counts))
	for l := range counts {
		labels = append(labels, l)
	}
	sort.Slice(labels, func(i, j int) bool {
		if counts[labels[i]] != counts[labels[j]] {
			return counts[labels[i]] > counts[labels[j]]
		}
		return labels[i] < labels[j]
	})
	ry = y + a.px(48)
	if len(labels) == 0 {
		a.text("None", right+a.px(16), ry+(lh-a.lineHeight(13, false))/2, 13, false, colFaint)
	}
	for _, l := range labels[:min(rows, len(labels))] {
		rx, rw := right+a.px(8), cw-a.px(16)
		if a.hovered(rx, ry, rw, lh) {
			a.round(rx, ry, rw, lh, a.px(6), colCardHover)
		}
		a.badge(l, colours[l], rx+a.px(8), ry+(lh-a.lineHeight(12, false)-a.px(6))/2)
		a.textRight(itoa(counts[l]), rx+rw-a.px(8), ry+(lh-a.lineHeight(13, true))/2, 13, true, colText)
		l := l
		a.clipHit(rx, ry, rw, lh, func(bool) { a.showFilter(l) })
		ry += lh
	}
	a.dashboardH = y + ch + p + a.scroll[pageDashboard] - ay
}

// recentBuilds draws the newest builds recorded, each opening its log.
func (a *app) recentBuilds(runs []*runlog.Report, x, y, cw, ch, lh int32, rows int) {
	a.card(x, y, cw, ch, false, false)
	a.text("Recently built", x+a.px(16), y+a.px(14), 15, true, colText)
	ry := y + a.px(48)
	for _, r := range runs[:min(rows, len(runs))] {
		rx, rw := x+a.px(8), cw-a.px(16)
		if a.hovered(rx, ry, rw, lh) {
			a.round(rx, ry, rw, lh, a.px(6), colCardHover)
		}
		is := a.px(28)
		a.drawIcon(a.runGame(r), rx+a.px(8), ry+(lh-is)/2, is)
		ts, tc := r.Started.Local().Format("Jan 2, 3:04 PM"), colMuted
		if r.Result == runlog.ResultFailed {
			ts, tc = "Failed, "+ts, colError
		}
		tw := a.textWidth(ts, 12, false)
		a.text(a.ellipsize(runSubject(r), rw-is-tw-a.px(40), 13, false), rx+is+a.px(18), ry+(lh-a.lineHeight(13, false))/2, 13, false, colText)
		a.textRight(ts, rx+rw-a.px(8), ry+(lh-a.lineHeight(12, false))/2, 12, false, tc)
		id := r.ID
		a.clipHit(rx, ry, rw, lh, func(bool) { a.showRun(id) })
		ry += lh
	}
}
