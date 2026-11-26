package gui

import (
	"os/exec"
	"runtime"
	"syscall"
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
