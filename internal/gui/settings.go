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
	fROM, fSys, fSave, fDat field
	defOS, defShell         string
	ogg                     int
	saved                   map[string]string // the file's values when loaded
	effective               map[string]config.Setting
	cfgPath                 string
	settingsH               int32
}

func (a *app) initSettings() {
	for _, f := range []*field{&a.fROM, &a.fSys, &a.fSave, &a.fDat} {
		f.onEnter = a.saveSettings
	}
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
	a.saved = map[string]string{
		"rom_dir":       str("rom_dir", file.ROMDir),
		"system_dir":    str("system_dir", file.SystemDir),
		"save_dir":      str("save_dir", file.SaveDir),
		"redump_dat":    str("redump_dat", file.RedumpDAT),
		"default_os":    str("default_os", file.DefaultOS),
		"default_shell": str("default_shell", file.DefaultShell),
		"ogg_quality":   strconv.Itoa(file.OggQuality),
	}
	a.fROM.set(a.saved["rom_dir"])
	a.fSys.set(a.saved["system_dir"])
	a.fSave.set(a.saved["save_dir"])
	a.fDat.set(a.saved["redump_dat"])
	a.defOS, a.defShell = a.saved["default_os"], a.saved["default_shell"]
	a.ogg = file.OggQuality

	a.effective = map[string]config.Setting{}
	if list, err := config.Settings(""); err == nil {
		for _, s := range list {
			a.effective[s.Key] = s
		}
	}
	a.fROM.placeholder = a.defaultHint("rom_dir")
	a.fSys.placeholder = a.defaultHint("system_dir")
	a.fSave.placeholder = a.defaultHint("save_dir")
	a.fDat.placeholder = "Redump's own, embedded and updated weekly"
}

// form is the page's values, keyed like the config file.
func (a *app) form() map[string]string {
	return map[string]string{
		"rom_dir":       a.fROM.text,
		"system_dir":    a.fSys.text,
		"save_dir":      a.fSave.text,
		"redump_dat":    a.fDat.text,
		"default_os":    a.defOS,
		"default_shell": a.defShell,
		"ogg_quality":   strconv.Itoa(a.ogg),
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
	switch k {
	case "save_dir":
		if !config.Configured() {
			return "None: a temporary folder, deleted after each game"
		}
	}
	var d config.Dir
	switch k {
	case "rom_dir":
		d = config.FindROMDir()
	case "system_dir":
		d = config.FindSystemDir()
	case "save_dir":
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
				err = config.Set(p, k, v)
			}
			if err != nil {
				a.say(err.Error(), true)
				return
			}
		}
		a.focus = nil
		a.say("Saved to "+p, false)
		a.reload()
	})
}

func (a *app) settingsToolbar(x, y int32) {
	changed := a.settingsChanged()
	var save, revert func()
	if changed {
		save, revert = a.saveSettings, a.loadSettings
	}
	x += a.button("Save", x, y, btnAccent, save) + a.px(4)
	a.button("Revert", x, y, btnNormal, revert)
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

func (a *app) drawSettings() {
	ax, ay, aw, _ := a.area()
	p := a.pad()
	x := ax + p*2
	w := min(aw-4*p, a.px(820))
	y := ay + p*2 - a.scroll[pageSettings]
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
		if flag := map[string]string{"rom_dir": a.o.ROMDir, "system_dir": a.o.SystemDir, "save_dir": a.o.SaveDir}[k]; flag != "" {
			note("This window was started with --"+strings.ReplaceAll(k, "_", "-")+" "+flag+", which takes precedence", colShell)
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
	folder("Games (ROMs)", "rom_dir", &a.fROM)
	note("The .dosz archives the Games page lists, at any depth.", colFaint)
	folder("System", "system_dir", &a.fSys)
	note("OS images and Windows 3.x shells, as DOSBox Pure's system directory.", colFaint)
	folder("Saves", "save_dir", &a.fSave)
	note("DOSBox Pure's save files, named after each archive.", colFaint)

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

	a.settingsH = y + a.scroll[pageSettings] - ay + p
}
