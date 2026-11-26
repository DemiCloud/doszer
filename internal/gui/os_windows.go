package gui

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

// hideConsole keeps a game's `doszer run` from opening a console window:
// doszer is a console program, and the window has none to share.
func hideConsole(cmd *exec.Cmd) {
	const createNoWindow = 0x08000000
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
}

func canOpen() bool { return true }

// open shows a folder in Explorer, or opens a file with its program.
func (a *app) open(path string) {
	if err := exec.Command("explorer.exe", path).Start(); err != nil {
		a.say(err.Error(), true)
	}
}

// showInFolder opens Explorer on a file's folder, the file selected.
func (a *app) showInFolder(path string) {
	cmd := exec.Command("explorer.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `explorer.exe /select,"` + path + `"`}
	if err := cmd.Start(); err != nil {
		a.say(err.Error(), true)
	}
}

func canPick() bool { return true }

var (
	shell32             = syscall.NewLazyDLL("shell32.dll")
	shBrowseForFolder   = shell32.NewProc("SHBrowseForFolderW")
	shGetPathFromIDList = shell32.NewProc("SHGetPathFromIDListW")
	ole32               = syscall.NewLazyDLL("ole32.dll")
	coInitializeEx      = ole32.NewProc("CoInitializeEx")
	coTaskMemFree       = ole32.NewProc("CoTaskMemFree")
	sendMessage         = syscall.NewLazyDLL("user32.dll").NewProc("SendMessageW")
	browseCallback      = syscall.NewCallback(browseProc) // made once: callbacks are never freed
)

// browseProc selects the starting folder once the dialog is up
// (BFFM_INITIALIZED, then BFFM_SETSELECTIONW with lpData, a path).
func browseProc(hwnd, msg, lParam, data uintptr) uintptr {
	const bffmInitialized, bffmSetSelectionW = 1, 0x467
	if msg == bffmInitialized && data != 0 {
		sendMessage.Call(hwnd, bffmSetSelectionW, 1, data)
	}
	return 0
}

// pickFolder asks for a folder with Windows' folder browser.
func pickFolder(title, start string) (string, bool) {
	coInitializeEx.Call(0, 2) // apartment threaded, as the dialog needs
	t, _ := syscall.UTF16PtrFromString("Choose the " + title + " folder")
	s, _ := syscall.UTF16PtrFromString(start)
	var name [260]uint16
	bi := struct {
		owner, root uintptr
		display     *uint16
		title       *uint16
		flags       uint32
		fn, data    uintptr
		image       int32
	}{display: &name[0], title: t, flags: 0x0001 | 0x0040, fn: browseCallback, data: uintptr(unsafe.Pointer(s))} // BIF_RETURNONLYFSDIRS | BIF_NEWDIALOGSTYLE
	pidl, _, _ := shBrowseForFolder.Call(uintptr(unsafe.Pointer(&bi)))
	runtime.KeepAlive(s)
	runtime.KeepAlive(t)
	if pidl == 0 {
		return "", false
	}
	defer coTaskMemFree.Call(pidl)
	var path [1024]uint16
	if r, _, _ := shGetPathFromIDList.Call(pidl, uintptr(unsafe.Pointer(&path[0]))); r == 0 {
		return "", false
	}
	return syscall.UTF16ToString(path[:]), true
}

var (
	kernel32              = syscall.NewLazyDLL("kernel32.dll")
	getLogicalDriveString = kernel32.NewProc("GetLogicalDriveStringsW")
	getDriveType          = kernel32.NewProc("GetDriveTypeW")
	getVolumeInformation  = kernel32.NewProc("GetVolumeInformationW")
	setErrorMode          = kernel32.NewProc("SetErrorMode")
	getOpenFileName       = syscall.NewLazyDLL("comdlg32.dll").NewProc("GetOpenFileNameW")
	getSaveFileName       = syscall.NewLazyDLL("comdlg32.dll").NewProc("GetSaveFileNameW")
)

// opticalDrives lists the CD drives (GetDriveType's DRIVE_CDROM), each
// with its disc's label, or no disc.
func opticalDrives() []drive {
	var buf [512]uint16
	n, _, _ := getLogicalDriveString.Call(uintptr(len(buf)), uintptr(unsafe.Pointer(&buf[0])))
	// No "insert a disk" box for an empty drive.
	old, _, _ := setErrorMode.Call(0x0001) // SEM_FAILCRITICALERRORS
	defer setErrorMode.Call(old)
	var out []drive
	// The roots are NUL-separated ("C:\\\x00D:\\\x00..."): UTF16ToString would
	// stop at the first.
	for _, root := range strings.Split(string(utf16.Decode(buf[:n])), "\x00") {
		if root == "" {
			continue
		}
		p, _ := syscall.UTF16PtrFromString(root)
		if t, _, _ := getDriveType.Call(uintptr(unsafe.Pointer(p))); t != 5 { // DRIVE_CDROM
			continue
		}
		var label [261]uint16
		d := drive{path: strings.TrimSuffix(root, `\`)}
		if r, _, _ := getVolumeInformation.Call(uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&label[0])), uintptr(len(label)), 0, 0, 0, 0, 0); r != 0 {
			d.disc, d.label = true, syscall.UTF16ToString(label[:])
		}
		out = append(out, d)
	}
	return out
}

// pickSave asks where to save a PNG, with Windows' save dialog.
func pickSave(title, name string) (string, bool) {
	paths := fileDialog(getSaveFileName, title, utf16z("PNG images (*.png)\x00*.png\x00"), name, "png",
		0x2|0x800|0x8|0x80000) // OFN_OVERWRITEPROMPT | OFN_PATHMUSTEXIST | OFN_NOCHANGEDIR | OFN_EXPLORER
	if len(paths) == 0 {
		return "", false
	}
	return paths[0], true
}

// pickFile asks for a file of a kind (fileDisc, fileArchive) with
// Windows' open dialog.
func pickFile(title string, kind int) (string, bool) {
	paths := pickFilesFlags(title, kind, 0)
	if len(paths) == 0 {
		return "", false
	}
	return paths[0], true
}

// pickFiles is pickFile choosing one or more files.
func pickFiles(title string, kind int) []string {
	return pickFilesFlags(title, kind, 0x200) // OFN_ALLOWMULTISELECT
}

func pickFilesFlags(title string, kind int, flags uint32) []string {
	filter := utf16z("Disc images (*.cue, *.zip)\x00*.cue;*.zip\x00All files\x00*.*\x00")
	if kind == fileArchive {
		filter = utf16z("DOSBox Pure archives (*.dosz)\x00*.dosz\x00All files\x00*.*\x00")
	}
	return fileDialog(getOpenFileName, title, filter, "", "", flags|0x1000|0x800|0x8|0x80000) // OFN_FILEMUSTEXIST | OFN_PATHMUSTEXIST | OFN_NOCHANGEDIR | OFN_EXPLORER
}

// fileDialog runs GetOpenFileName or GetSaveFileName and returns the
// files chosen: with OFN_ALLOWMULTISELECT, several come back as the
// folder, then each name, NUL-separated.
func fileDialog(proc *syscall.LazyProc, title string, filter []uint16, name, defExt string, flags uint32) []string {
	file := make([]uint16, 1<<16)
	copy(file[:len(file)-1], utf16z(name))
	t, _ := syscall.UTF16PtrFromString(title)
	ext := utf16z(defExt)
	ofn := struct {
		size                 uint32
		owner, instance      uintptr
		filter, customFilter *uint16
		maxCustFilter, index uint32
		file                 *uint16
		maxFile              uint32
		fileTitle            *uint16
		maxFileTitle         uint32
		initialDir, title    *uint16
		flags                uint32
		fileOffset, fileExt  uint16
		defExt               *uint16
		custData, hook       uintptr
		template             *uint16
		reserved             uintptr
		reserved2, flagsEx   uint32
	}{filter: &filter[0], index: 1, file: &file[0], maxFile: uint32(len(file)), title: t, flags: flags}
	if defExt != "" {
		ofn.defExt = &ext[0]
	}
	ofn.size = uint32(unsafe.Sizeof(ofn))
	r, _, _ := proc.Call(uintptr(unsafe.Pointer(&ofn)))
	runtime.KeepAlive(filter)
	runtime.KeepAlive(t)
	runtime.KeepAlive(ext)
	if r == 0 {
		return nil
	}
	var parts []string
	for start, i := 0, 0; i < len(file); i++ {
		if file[i] == 0 {
			if i == start {
				break // the double NUL ending the list
			}
			parts = append(parts, string(utf16.Decode(file[start:i])))
			start = i + 1
		}
	}
	if len(parts) <= 1 {
		return parts
	}
	out := make([]string, 0, len(parts)-1)
	for _, n := range parts[1:] {
		out = append(out, filepath.Join(parts[0], n))
	}
	return out
}

func utf16z(s string) []uint16 {
	out := make([]uint16, 0, len(s)+1)
	for _, r := range s {
		out = append(out, uint16(r))
	}
	return append(out, 0)
}
