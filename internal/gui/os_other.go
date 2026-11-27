//go:build !windows

package gui

import (
	"os"
	"os/exec"
	"path/filepath"
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

// showInFolder opens a file's folder.
func (a *app) showInFolder(path string) { a.open(filepath.Dir(path)) }

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

// opticalDrives lists the CD drives: the SCSI generic devices of type 5
// (CD-ROM), as redumper takes them (/dev/sgN), each named by its model.
// Whether a disc is in isn't known without opening the drive.
func opticalDrives() []drive {
	var out []drive
	matches, _ := filepath.Glob("/sys/class/scsi_generic/sg*/device/type")
	for _, m := range matches {
		t, err := os.ReadFile(m)
		if err != nil || strings.TrimSpace(string(t)) != "5" {
			continue
		}
		dev := filepath.Base(filepath.Dir(filepath.Dir(m)))
		d := drive{path: "/dev/" + dev, disc: true}
		vendor, _ := os.ReadFile(filepath.Join(filepath.Dir(m), "vendor"))
		model, _ := os.ReadFile(filepath.Join(filepath.Dir(m), "model"))
		d.label = strings.TrimSpace(strings.TrimSpace(string(vendor)) + " " + strings.TrimSpace(string(model)))
		out = append(out, d)
	}
	return out
}

// pickFile asks for a file of a kind (fileDisc, fileArchive) with zenity
// or kdialog.
func pickFile(title string, kind int) (string, bool) {
	paths := pickFilesMulti(title, kind, false)
	if len(paths) == 0 {
		return "", false
	}
	return paths[0], true
}

// pickFiles is pickFile choosing one or more files.
func pickFiles(title string, kind int) []string { return pickFilesMulti(title, kind, true) }

func pickFilesMulti(title string, kind int, multi bool) []string {
	pat, name := "*.cue *.CUE *.iso *.ISO *.zip *.ZIP *.dosz *.DOSZ *.img *.IMG *.ima *.IMA *.vfd *.VFD", "Disc images and game archives"
	switch kind {
	case fileArchive:
		pat, name = "*.dosz *.DOSZ *.dosp *.DOSP", "DOSBox Pure archives"
	case fileIcon:
		pat, name = "*.ico *.ICO *.exe *.EXE *.dll *.DLL", "Icons and programs"
	}
	var cmd *exec.Cmd
	switch picker() {
	case "zenity":
		args := []string{"--file-selection", "--title=" + title, "--file-filter=" + name + " | " + pat, "--file-filter=All files | *"}
		if multi {
			args = append(args, "--multiple", "--separator=\n")
		}
		cmd = exec.Command("zenity", args...)
	case "kdialog":
		args := []string{"--getopenfilename", ".", pat + "|" + name, "--title", title}
		if multi {
			args = append(args, "--multiple", "--separate-output")
		}
		cmd = exec.Command("kdialog", args...)
	default:
		return nil
	}
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var paths []string
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			paths = append(paths, l)
		}
	}
	return paths
}

// pickSave asks where to save a file named name, with zenity or kdialog;
// without either, it goes in the home folder.
func pickSave(title, name string) (string, bool) {
	var cmd *exec.Cmd
	switch picker() {
	case "zenity":
		cmd = exec.Command("zenity", "--file-selection", "--save", "--confirm-overwrite", "--title="+title, "--filename="+name)
	case "kdialog":
		cmd = exec.Command("kdialog", "--getsavefilename", name, "--title", title)
	default:
		home, err := os.UserHomeDir()
		if err != nil {
			return "", false
		}
		return filepath.Join(home, name), true
	}
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	p := strings.TrimSpace(string(out))
	return p, p != ""
}
