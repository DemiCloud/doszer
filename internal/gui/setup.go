package gui

import (
	"path/filepath"

	"github.com/DemiCloud/doszer/internal/config"
	"github.com/DemiCloud/doszer/internal/env"
)

// setupDialog is the first-run dialog: where doszer keeps its settings,
// saves and games - beside itself (portable), in the user's profile, or
// nowhere.
type setupDialog struct {
	then func() // what the user was doing that needed a configuration
}

// openSetup opens the first-run dialog.
func (a *app) openSetup() {
	a.setup, a.popup = &setupDialog{}, nil
}

// whenSetUp runs fn now if doszer is configured, else after the user
// sets it up in the first-run dialog (not if they choose not to save).
func (a *app) whenSetUp(fn func()) {
	if config.Configured() {
		fn()
		return
	}
	a.setup, a.popup = &setupDialog{then: fn}, nil
}

func (a *app) setupKey(sc int32) {
	switch sc {
	case scEscape:
		a.setupChoose(2)
	case sc1:
		a.setupChoose(0)
	case sc2:
		a.setupChoose(1)
	case sc3:
		a.setupChoose(2)
	}
}

// setupChoose applies choice 0 (portable), 1 (user profile) or 2 (don't
// save).
func (a *app) setupChoose(choice int) {
	then := a.setup.then
	switch choice {
	case 0:
		dir := config.ExeDir()
		if dir == "" || !config.Writable(dir) {
			a.say("doszer's folder can't be written to, so it can't be portable there", true)
			return
		}
		if _, err := env.Portable(env.PortableOptions{Dir: dir, Settings: config.Default()}); err != nil {
			a.say("Portable setup: "+err.Error(), true)
			return
		}
		a.say("Set up portable in "+dir, false)
	case 1:
		p, err := config.SetupUser()
		if err != nil {
			a.say("Setup: "+err.Error(), true)
			return
		}
		a.say("Set up: settings in "+p, false)
	default:
		a.setup = nil
		a.say("Not set up: nothing will be saved", false)
		return
	}
	a.setup = nil
	a.reload()
	if then != nil {
		then()
	}
}

func (a *app) drawSetup() {
	if a.setup == nil {
		return
	}
	a.layer = layerModal
	defer func() { a.layer = layerPage }()
	a.fill(0, 0, a.w, a.h, rgba{0, 0, 0, 170})
	a.onClick(0, 0, a.w, a.h, func(bool) {}) // the page below is out of reach

	w := min(a.w-a.px(40), a.px(720))
	cardH := a.px(104)
	h := a.px(120) + 3*(cardH+a.px(10))
	x, y := (a.w-w)/2, max(a.px(20), (a.h-h)/2)
	a.round(x-a.px(1), y-a.px(1), w+a.px(2), h+a.px(2), a.px(14), colLine)
	a.round(x, y, w, h, a.px(14), colPopup)

	p := a.px(24)
	ty := y + p
	a.text("Welcome to doszer", x+p, ty, 20, true, colText)
	ty += a.lineHeight(20, true) + a.px(6)
	a.text(a.ellipsize("Where should doszer keep its settings, saves and play time? You can change this later in Settings.", w-2*p, 13, false), x+p, ty, 13, false, colMuted)
	ty += a.lineHeight(13, false) + a.px(22)

	exe := config.ExeDir()
	portableOK := exe != "" && config.Writable(exe)
	user, _ := config.XDGConfigPath()
	userDirs := "Folders for games, systems and saves in "
	if data, err := config.DataDir(); err == nil {
		userDirs += data
	}
	if config.RetroDECK() != (config.RetroDECKDirs{}) {
		userDirs = "Games, systems and saves in RetroDECK's folders"
	}
	type option struct {
		title, line1, line2 string
		ok                  bool
		why                 string
		accent              bool
	}
	options := []option{
		{"Portable", "Everything beside doszer, to carry on a USB stick or keep in one folder:", filepath.Join(exe, "{config.toml, roms, system, saves}"), portableOK, "doszer's folder can't be written to", false},
		{"User profile (recommended)", "Settings in " + user, userDirs, true, "", true},
		{"Don't save", "Try doszer without writing anything: no settings, play time or game saves.", "This question comes back next time.", true, "", false},
	}
	for i, o := range options {
		cx, cw := x+p, w-2*p
		hover := a.hovered(cx, ty, cw, cardH)
		bg := colCard
		if hover && o.ok {
			bg = colCardHover
		}
		if o.accent {
			a.outline(cx, ty, cw, cardH, a.px(10), a.px(1), colAccentDark, bg)
		} else {
			a.round(cx, ty, cw, cardH, a.px(10), bg)
		}
		lx := cx + a.px(18)
		ly := ty + a.px(14)
		tc := colText
		if !o.ok {
			tc = colFaint
		}
		a.text(o.title, lx, ly, 15, true, tc)
		ly += a.lineHeight(15, true) + a.px(6)
		bw := a.buttonW("Choose") + a.px(16)
		inner := cw - a.px(36) - bw
		a.text(a.ellipsize(o.line1, inner, 13, false), lx, ly, 13, false, colMuted)
		ly += a.lineHeight(13, false) + a.px(4)
		line2, c2 := o.line2, colFaint
		if !o.ok {
			line2, c2 = o.why, colError
		}
		a.text(a.ellipsize(line2, inner, 12, false), lx, ly, 12, false, c2)

		style := btnNormal
		if o.accent {
			style = btnAccent
		}
		var fn func()
		if o.ok {
			choice := i
			fn = func() { a.setupChoose(choice) }
		}
		a.button("Choose", cx+cw-a.px(18)-a.buttonW("Choose"), ty+(cardH-a.px(32))/2, style, fn)
		ty += cardH + a.px(10)
	}
}

// OpenSetup opens the first-run dialog (Options.Setup).
func (a *app) OpenSetup() { a.openSetup() }
