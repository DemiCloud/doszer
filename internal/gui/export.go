package gui

import (
	"strings"

	"github.com/DemiCloud/doszer/internal/config"
)

// export copies archives into RetroDECK's folders: `doszer export -n`
// shows where each goes (the folders it found, what's there already),
// then Export copies, Overwrite also replaces archives that differ.
// Without a RetroDECK folder found, Choose folder asks for one.
// files nil: every game in the ROM folders.
func (a *app) export(title string, files []string) {
	args := []string{"export", "--format", "plain"}
	if a.exportDir != "" {
		args = append(args, "--dir", a.exportDir)
	}
	if files == nil {
		if len(a.romDirs) == 0 {
			a.alert("No ROM folder", "Choose where games go first: Tools, Settings.")
			return
		}
		args = append(append(args, "--all"), config.DirPaths(a.romDirs)...)
	} else {
		args = append(args, files...)
	}
	a.runDoszer(title, append(append([]string{}, args[:3]...), append([]string{"-n"}, args[3:]...)...), func(err error) {
		d := a.dialog
		if d == nil || d.job == nil {
			return
		}
		d.job.mu.Lock()
		out := string(d.job.out)
		d.job.mu.Unlock()
		buttons := []dialogButton{{label: "Cancel"}}
		switch {
		case strings.Contains(out, "no RetroDECK folder found") || strings.Contains(out, "not a RetroDECK folder"):
			buttons = append(buttons, dialogButton{label: "Choose folder…", style: btnAccent, keep: true, fn: func() {
				if p, ok := pickFolder("RetroDECK's folder (holding roms and bios)", a.exportDir); ok {
					a.exportDir = p
					a.export(title, files)
				}
			}})
		case strings.Contains(out, "Would copy") || strings.Contains(out, "Would replace") || strings.Contains(out, "Differs, kept"):
			if strings.Contains(out, "Differs, kept") {
				buttons = append(buttons, dialogButton{label: "Overwrite", fn: func() {
					a.runDoszer(title, append(append([]string{}, args...), "--overwrite"), nil)
				}})
			}
			if strings.Contains(out, "Would copy") {
				buttons = append(buttons, dialogButton{label: "Export", style: btnAccent, fn: func() { a.runDoszer(title, args, nil) }})
			}
		default:
			buttons = []dialogButton{{label: "Close", style: btnAccent}}
		}
		d.buttons = append([]dialogButton{a.copyAll(d)}, buttons...)
	})
}
