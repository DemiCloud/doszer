//go:build !windows

package gui

import (
	"os/exec"
	"strings"
)

// hideConsole does nothing: only Windows opens a console for a child.
func hideConsole(*exec.Cmd) {}

func canOpen() bool { _, err := exec.LookPath("xdg-open"); return err == nil }

// open shows a folder in the file manager, or opens a file with its
// program (xdg-open).
func (a *app) open(path string) {
	if err := exec.Command("xdg-open", path).Start(); err != nil {
		a.say(err.Error(), true)
	}
}

// picker is the first folder dialog program found: zenity (GNOME) or
// kdialog (KDE).
func picker() string {
	for _, p := range []string{"zenity", "kdialog"} {
		if _, err := exec.LookPath(p); err == nil {
			return p
		}
	}
	return ""
}

func canPick() bool { return picker() != "" }

// pickFolder asks for a folder with zenity or kdialog.
func pickFolder(title, start string) (string, bool) {
	var cmd *exec.Cmd
	switch picker() {
	case "zenity":
		cmd = exec.Command("zenity", "--file-selection", "--directory", "--title=Choose the "+title+" folder", "--filename="+strings.TrimSuffix(start, "/")+"/")
	case "kdialog":
		cmd = exec.Command("kdialog", "--getexistingdirectory", start, "--title", "Choose the "+title+" folder")
	default:
		return "", false
	}
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	p := strings.TrimSpace(string(out))
	return p, p != ""
}
