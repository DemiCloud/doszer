package gui

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/DemiCloud/doszer/internal/config"
)

// settings is the Settings page's state: the values of the user config
// file (the one the page writes), not the effective ones, which show as
// placeholders.
type settings struct {
	fROMs             []*field // paths.roms: one field per folder
	fSys, fSave, fDat field
	fSources          field
	defOS, defShell   string
	ogg               int
	saved             map[string]string // the file's values when loaded; paths.roms joined by NUL
	effective         map[string]config.Setting
	cfgPath           string
	settingsH         int32
	settingsOpen      bool
	settingsScroll    int32
}

func (a *app) initSettings() {
	for _, f := range []*field{&a.fSys, &a.fSave, &a.fDat, &a.fSources} {
		f.onEnter = a.saveSettings
	}
}

// romField is a new ROM folder field.
func (a *app) romField(v string) *field {
	f := &field{onEnter: a.saveSettings}
	f.set(v)
	return f
}

// loadSettings reads the user config file's values into the form.
func (a *app) loadSettings() {
	a.cfgPath, _ = config.UserConfigPath()
	file, keys := config.Default(), map[string]bool{}
	if _, err := os.Stat(a.cfgPath); err == nil {
		var err error
		if file, keys, err = config.LoadFile(a.cfgPath); err != nil {
			a.say(err.Error(), true)
		}
	}
	str := func(k, v string) string {
		if keys[k] {
			return v
		}
		return ""
	}
	var roms []string
	if keys["paths.roms"] {
		roms = file.Paths.ROMs
	}
	a.saved = map[string]string{
		"paths.roms":        strings.Join(roms, "\x00"),
		"paths.system":      str("paths.system", file.Paths.System),
		"paths.saves":       str("paths.saves", file.Paths.Saves),
		"paths.sources":     str("paths.sources", file.Paths.Sources),
		"build.redump_dat":  str("build.redump_dat", file.Build.RedumpDAT),
		"defaults.os":       str("defaults.os", file.Defaults.OS),
		"defaults.shell":    str("defaults.shell", file.Defaults.Shell),
		"build.ogg_quality": strconv.Itoa(file.Build.OggQuality),
	}
	a.fROMs = nil
	for _, r := range roms {
		a.fROMs = append(a.fROMs, a.romField(r))
	}
	if len(a.fROMs) == 0 {
		a.fROMs = []*field{a.romField("")}
	}
	a.fSys.set(a.saved["paths.system"])
	a.fSave.set(a.saved["paths.saves"])
	a.fSources.set(a.saved["paths.sources"])
	a.fDat.set(a.saved["build.redump_dat"])
	a.defOS, a.defShell = a.saved["defaults.os"], a.saved["defaults.shell"]
	a.ogg = file.Build.OggQuality

	a.effective = map[string]config.Setting{}
	if list, err := config.Settings(""); err == nil {
		for _, s := range list {
			if _, dup := a.effective[s.Key]; !dup {
				a.effective[s.Key] = s
			}
		}
	}
	a.fROMs[0].placeholder = a.defaultHint("paths.roms")
	a.fSys.placeholder = a.defaultHint("paths.system")
	a.fSave.placeholder = a.defaultHint("paths.saves")
	a.fSources.placeholder = a.defaultHint("paths.sources")
	a.fDat.placeholder = "Redump's own, embedded and updated weekly"
}

// form is the page's values, keyed like the config file.
func (a *app) form() map[string]string {
	var roms []string
	for _, f := range a.fROMs {
		if t := strings.TrimSpace(f.text); t != "" {
			roms = append(roms, t)
		}
	}
	return map[string]string{
		"paths.roms":        strings.Join(roms, "\x00"),
		"paths.system":      a.fSys.text,
		"paths.saves":       a.fSave.text,
		"paths.sources":     a.fSources.text,
		"build.redump_dat":  a.fDat.text,
		"defaults.os":       a.defOS,
		"defaults.shell":    a.defShell,
		"build.ogg_quality": strconv.Itoa(a.ogg),
	}
}

func (a *app) settingsChanged() bool {
	for k, v := range a.form() {
		if a.saved[k] != v {
			return true
		}
	}
	return false
}

// defaultHint is a folder field's placeholder: what doszer uses unset.
func (a *app) defaultHint(k string) string {
	if k == "paths.saves" && !config.Configured() {
		return "None: a temporary folder, deleted after each game"
	}
	if k == "paths.sources" {
		d := config.SourcesDirOf(config.Default())
		if d.Path == "" {
			return "None: a temporary folder, deleted after each build"
		}
		return d.Path + "  (" + d.From + ")"
	}
	var d config.Dir
	switch k {
	case "paths.roms":
		d = config.FindROMDir()
	case "paths.system":
		d = config.FindSystemDir()
	case "paths.saves":
		if d = config.FindSaveDir(); d.Path == "" {
			if c, err := config.CacheSaveDir(); err == nil {
				d = config.Dir{Path: c, From: config.FromCache}
			}
		}
	}
	if d.Path == "" {
		return "None found"
	}
	return d.Path + "  (" + d.From + ")"
}

// saveSettings writes the changed values to the user config file. An
// unconfigured doszer asks where first (the first-run dialog).
func (a *app) saveSettings() {
	if !a.settingsChanged() {
		return
	}
	a.whenSetUp(func() {
		p, err := config.UserConfigPath()
		if err != nil {
			a.say(err.Error(), true)
			return
		}
		form := a.form()
		for _, k := range config.Keys {
			v, ok := form[k]
			if !ok || v == a.saved[k] {
				continue
			}
			if v == "" {
				err = config.Unset(p, k)
			} else {
				err = config.Set(p, k, strings.Split(v, "\x00")...)
			}
			if err != nil {
				a.say(err.Error(), true)
				return
			}
		}
		a.focus, a.settingsOpen = nil, false
		a.say("Saved to "+p, false)
		a.reload()
	})
}

// openSettings opens the Settings dialog on the configuration's values.
func (a *app) openSettings() {
	a.loadSettings()
	a.settingsOpen, a.settingsScroll, a.popup, a.focus = true, 0, nil, nil
}

// closeSettings closes the dialog, asking first if there are unsaved
// changes.
func (a *app) closeSettings() {
	if a.settingsChanged() {
		a.confirm("Discard your changes?", "Discard", func() {
			a.settingsOpen, a.focus = false, nil
			a.loadSettings()
		}, "The settings you changed aren't saved.")
		return
	}
	a.settingsOpen, a.focus = false, nil
}

// drawSettingsDialog draws Settings as a modal dialog: the form scrolls,
// Save and Close stay below it.
func (a *app) drawSettingsDialog() {
	if !a.settingsOpen {
		return
	}
	a.layer = layerModal
	defer func() { a.layer = layerPage }()
	a.fill(0, 0, a.w, a.h, rgba{0, 0, 0, 170})
	a.onClick(0, 0, a.w, a.h, func(bool) {})
	w := min(a.w-a.px(40), a.px(900))
	h := min(a.h-a.px(40), a.px(680))
	x, y := (a.w-w)/2, (a.h-h)/2
	a.round(x-a.px(1), y-a.px(1), w+a.px(2), h+a.px(2), a.px(14), colLine)
	a.round(x, y, w, h, a.px(14), colPopup)
	p := a.px(24)
	a.text("Settings", x+p, y+p, 19, true, colText)
	a.text(a.ellipsize(a.settingsStatus(), w/2, 12, false), x+w-p-min(a.textWidth(a.settingsStatus(), 12, false), w/2), y+p+a.px(6), 12, false, colFaint)
	top := y + p + a.lineHeight(19, true) + a.px(10)
	bottom := y + h - p - a.px(44)
	a.fill(x, top-a.px(1), w, a.px(1), colLine)
	a.fill(x, bottom+a.px(4), w, a.px(1), colLine)
	a.pushClip(x, top, w, bottom-top)
	a.drawSettings(x, top, w, bottom-top)
	a.popClip()
	by := y + h - p - a.px(32)
	bx := x + w - p
	var save func()
	if a.settingsChanged() {
		save = func() { a.saveSettings() }
	}
	bx -= a.buttonW("Save")
	a.button("Save", bx, by, btnAccent, save)
	bx -= a.buttonW("Close") + a.px(8)
	a.button("Close", bx, by, btnNormal, a.closeSettings)
}

func (a *app) settingsStatus() string {
	switch {
	case config.Portable():
		return "Portable: " + a.cfgPath
	case config.Configured():
		return "Configuration: " + a.cfgPath
	}
	return "Not set up: nothing is saved"
}

// drawSettings draws the form in the rectangle, scrolled.
func (a *app) drawSettings(ax, ay, aw, ah int32) {
	p := a.pad()
	x := ax + p*2
	w := aw - 4*p
	y := ay + p - a.settingsScroll
	labelW := a.px(170)
	fx := x + labelW
	fw := w - labelW

	note := func(s string, c rgba) {
		a.text(a.ellipsize(s, fw, 12, false), fx, y, 12, false, c)
		y += a.lineHeight(12, false) + a.px(14)
	}
	label := func(s string) {
		a.text(s, x, y+a.px(7), 13, false, colText)
	}
	// overridden notes a layer above the user config file setting k.
	overridden := func(k string) {
		if flag := map[string]string{"paths.roms": a.o.ROMDir, "paths.system": a.o.SystemDir, "paths.saves": a.o.SaveDir}[k]; flag != "" {
			name := map[string]string{"paths.roms": "--rom-dir", "paths.system": "--system-dir", "paths.saves": "--save-dir"}[k]
			note("This window was started with "+name+" "+flag+", which takes precedence", colShell)
			return
		}
		if s, ok := a.effective[k]; ok && s.From != "" && s.From != a.cfgPath && filepath.IsAbs(s.From) {
			note("Set to "+s.Value+" in "+s.From+", which takes precedence", colShell)
		}
	}

	// Setup.
	y = a.heading("Setup", x, y)
	switch {
	case config.Portable():
		label("Mode")
		a.text("Portable", fx, y+a.px(7), 13, true, colText)
		y += a.px(32)
		note("Settings, saves and play time are kept beside doszer: "+filepath.Dir(a.cfgPath), colMuted)
	case config.Configured():
		label("Mode")
		a.text("User profile", fx, y+a.px(7), 13, true, colText)
		y += a.px(32)
		note("Settings: "+a.cfgPath, colMuted)
	default:
		label("Mode")
		a.text("Not set up", fx, y+a.px(7), 13, true, colShell)
		a.button("Set up…", fx+a.px(110), y, btnAccent, func() { a.openSetup() })
		y += a.px(32)
		note("Nothing is saved: no settings, play time, or game saves after a game closes.", colMuted)
	}
	if config.Configured() && canOpen() {
		a.button("Open settings file", fx, y-a.px(8), btnNormal, func() { a.open(a.cfgPath) })
		y += a.px(40)
	}

	// Folders.
	y += a.px(10)
	y = a.heading("Folders", x, y)
	folder := func(name, k string, f *field) {
		label(name)
		bw := int32(0)
		if canPick() {
			bw = a.buttonW("Browse…") + a.px(8)
		}
		a.drawField(f, fx, y, fw-bw)
		if bw > 0 {
			a.button("Browse…", fx+fw-bw+a.px(8), y, btnNormal, func() {
				start := f.text
				if start == "" {
					start = a.effective[k].Value
				}
				if dir, ok := pickFolder(name, start); ok {
					f.set(dir)
					a.focus = nil
				}
			})
		}
		y += a.px(38)
		overridden(k)
	}
	// The ROM folders: one or several.
	for i, f := range a.fROMs {
		name := "Games (ROMs)"
		if i > 0 {
			name = ""
		}
		label(name)
		bw := a.buttonW("×") + a.px(8)
		if canPick() {
			bw += a.buttonW("Browse…") + a.px(8)
		}
		a.drawField(f, fx, y, fw-bw)
		bx := fx + fw - bw + a.px(8)
		if canPick() {
			bx += a.button("Browse…", bx, y, btnNormal, func() {
				start := f.text
				if start == "" {
					start = a.effective["paths.roms"].Value
				}
				if dir, ok := pickFolder("games", start); ok {
					f.set(dir)
					a.focus = nil
				}
			}) + a.px(8)
		}
		idx := i
		a.button("×", bx, y, btnGhost, func() {
			if len(a.fROMs) == 1 {
				a.fROMs[0].set("")
				return
			}
			a.fROMs = append(a.fROMs[:idx], a.fROMs[idx+1:]...)
			a.fROMs[0].placeholder = a.defaultHint("paths.roms")
		})
		y += a.px(38)
	}
	a.button("Add a folder", fx, y-a.px(2), btnGhost, func() {
		a.fROMs = append(a.fROMs, a.romField(""))
		a.focus = a.fROMs[len(a.fROMs)-1]
	})
	y += a.px(38)
	overridden("paths.roms")
	note("The .dosz archives the Games page lists, each folder at any depth.", colFaint)
	folder("System", "paths.system", &a.fSys)
	note("OS images and Windows 3.x shells, as DOSBox Pure's system directory.", colFaint)
	folder("Saves", "paths.saves", &a.fSave)
	note("DOSBox Pure's save files, named after each archive.", colFaint)
	folder("Sources", "paths.sources", &a.fSources)
	note("Where Build > Import a disc keeps each disc's dump and DOSZ.TOML.", colFaint)

	// Systems.
	y += a.px(10)
	y = a.heading("Systems", x, y)
	chooser := func(name string, shell bool, cur *string, hint string) {
		label(name)
		items, current := []string{"None"}, 0
		for _, f := range a.files {
			if f.Shell == shell && !f.missing {
				items = append(items, f.Name)
				if f.Name == *cur {
					current = len(items) - 1
				}
			}
		}
		if *cur != "" && current == 0 {
			items = append(items, *cur+" (not found)")
			current = len(items) - 1
		}
		text := "None ▼"
		if *cur != "" {
			text = items[current] + " ▼"
		}
		bx := fx
		a.button(text, bx, y, btnNormal, func() {
			a.openPopup(bx, y, a.px(260), items, current, func(i int) {
				switch {
				case i == 0:
					*cur = ""
				case !strings.HasSuffix(items[i], " (not found)"):
					*cur = items[i]
				}
			})
		})
		y += a.px(38)
		note(hint, colFaint)
	}
	chooser("Default OS image", false, &a.defOS, "init gives games that need Windows 95 or 98 this image.")
	chooser("Default shell", true, &a.defShell, "init gives games that need Windows 3.x this shell.")

	// Discs.
	y += a.px(10)
	y = a.heading("Building", x, y)
	label("Ogg Vorbis quality")
	sx := fx
	sx += a.button("−", sx, y, btnNormal, func() { a.ogg = max(-1, a.ogg-1) }) + a.px(8)
	q := strconv.Itoa(a.ogg)
	a.text(q, sx+a.px(8), y+a.px(7), 13, true, colText)
	sx += a.px(36)
	a.button("+", sx, y, btnNormal, func() { a.ogg = min(10, a.ogg+1) })
	y += a.px(38)
	note("CD audio tracks' encoding quality, -1 to 10; 6 is the default.", colFaint)
	label("Redump datfile")
	a.drawField(&a.fDat, fx, y, fw)
	y += a.px(38)
	note("A datfile dump and verify check discs against instead of Redump's own.", colFaint)

	a.settingsH = y + a.settingsScroll - ay + p
	a.settingsScroll = max(0, min(a.settingsScroll, a.settingsH-ah))
}
