package main

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"
)

var (
	advapi32        = syscall.NewLazyDLL("advapi32.dll")
	regCreateKeyEx  = advapi32.NewProc("RegCreateKeyExW")
	regSetValueEx   = advapi32.NewProc("RegSetValueExW")
	regDeleteTree   = advapi32.NewProc("RegDeleteTreeW")
	regCloseKey     = advapi32.NewProc("RegCloseKey")
	shChangeNotify  = syscall.NewLazyDLL("shell32.dll").NewProc("SHChangeNotify")
	errFileNotFound = syscall.Errno(2) // ERROR_FILE_NOT_FOUND
)

const (
	hkeyCurrentUser = 0x80000001
	keyAllAccess    = 0xF003F
	regSZ           = 1
	progID          = "doszer.archive"
)

// associate registers (or removes) doszer as the program that opens
// .dosz files, for the current user (HKCU\Software\Classes, no
// administrator rights), with doszer's own icon (its resource 0, from
// cmd/doszer's .syso). A program the user picked in Explorer's "Open
// with" still wins.
func associate(exe string, remove, dryRun bool) ([]string, error) {
	classes := `Software\Classes\`
	if remove {
		var changes []string
		for _, k := range []string{".dosz", progID} {
			changes = append(changes, `delete HKCU\`+classes+k)
			if dryRun {
				continue
			}
			p, _ := syscall.UTF16PtrFromString(classes + k)
			if r, _, _ := regDeleteTree.Call(hkeyCurrentUser, uintptr(unsafe.Pointer(p))); r != 0 && syscall.Errno(r) != errFileNotFound {
				return changes, fmt.Errorf("deleting %s: %w", k, syscall.Errno(r))
			}
		}
		if !dryRun {
			notifyAssoc()
		}
		return changes, nil
	}
	values := [][2]string{
		{".dosz", progID},
		{progID, "DOSBox Pure archive"},
		{progID + `\DefaultIcon`, `"` + exe + `",0`},
		{progID + `\shell\open\command`, `"` + exe + `" "%1"`},
	}
	var changes []string
	for _, v := range values {
		changes = append(changes, fmt.Sprintf(`set HKCU\%s%s = %s`, classes, v[0], v[1]))
		if dryRun {
			continue
		}
		if err := setDefault(classes+v[0], v[1]); err != nil {
			return changes, err
		}
	}
	if !dryRun {
		notifyAssoc()
	}
	return changes, nil
}

// setDefault sets key's default value, creating the key.
func setDefault(key, value string) error {
	k, _ := syscall.UTF16PtrFromString(key)
	var h uintptr
	if r, _, _ := regCreateKeyEx.Call(hkeyCurrentUser, uintptr(unsafe.Pointer(k)), 0, 0, 0, keyAllAccess, 0, uintptr(unsafe.Pointer(&h)), 0); r != 0 {
		return fmt.Errorf("creating %s: %w", key, syscall.Errno(r))
	}
	defer regCloseKey.Call(h)
	data, _ := syscall.UTF16FromString(value)
	if len(data) == 0 {
		return errors.New("empty value")
	}
	if r, _, _ := regSetValueEx.Call(h, 0, 0, regSZ, uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)*2)); r != 0 {
		return fmt.Errorf("setting %s: %w", key, syscall.Errno(r))
	}
	return nil
}

// notifyAssoc tells Explorer file associations changed, so icons update.
func notifyAssoc() {
	const shcneAssocChanged, shcnfIDList = 0x08000000, 0
	shChangeNotify.Call(shcneAssocChanged, shcnfIDList, 0, 0)
}
