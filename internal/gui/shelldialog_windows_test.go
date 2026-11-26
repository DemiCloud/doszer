package gui

import (
	"os"
	"syscall"
	"testing"
	"unsafe"
)

// TestShellDialogObjects makes Explorer's open and save dialogs without
// showing them: the class and interface IDs, the options and folder
// vtable slots, and a folder item from a path, as shellDialog uses them.
func TestShellDialogObjects(t *testing.T) {
	coInitializeEx.Call(0, coinitApartment)
	for _, c := range []struct {
		name       string
		clsid, iid *guid
	}{{"open", &clsidFileOpenDialog, &iidIFileOpenDialog}, {"save", &clsidFileSaveDialog, &iidIFileSaveDialog}} {
		var d *comObj
		if r, _, _ := coCreateInstance.Call(uintptr(unsafe.Pointer(c.clsid)), 0, clsctxInprocSever, uintptr(unsafe.Pointer(c.iid)), uintptr(unsafe.Pointer(&d))); r != 0 || d == nil {
			t.Fatalf("%s: CoCreateInstance: %#x", c.name, r)
		}
		var opts uint32
		if r := d.call(mGetOptions, uintptr(unsafe.Pointer(&opts))); r != 0 {
			t.Fatalf("%s: GetOptions: %#x", c.name, r)
		}
		want := opts | fosPickFolders | fosNoChangeDir
		if c.name == "save" {
			want = opts | fosOverwrite | fosNoChangeDir
		}
		if r := d.call(mSetOptions, uintptr(want)); r != 0 {
			t.Fatalf("%s: SetOptions: %#x", c.name, r)
		}
		var got uint32
		d.call(mGetOptions, uintptr(unsafe.Pointer(&got)))
		if got != want {
			t.Errorf("%s: options %#x, want %#x", c.name, got, want)
		}
		dir, _ := os.Getwd()
		p, _ := syscall.UTF16PtrFromString(dir)
		var item *comObj
		if r, _, _ := shCreateItemFromParsingName.Call(uintptr(unsafe.Pointer(p)), 0, uintptr(unsafe.Pointer(&iidIShellItem)), uintptr(unsafe.Pointer(&item))); r != 0 || item == nil {
			t.Fatalf("SHCreateItemFromParsingName(%s): %#x", dir, r)
		}
		var name *uint16
		if r := item.call(mItemDisplayName, sigdnFileSysPath, uintptr(unsafe.Pointer(&name))); r != 0 {
			t.Fatalf("GetDisplayName: %#x", r)
		}
		if got := utf16At(name); got != dir {
			t.Errorf("item path %q, want %q", got, dir)
		}
		coTaskMemFree.Call(uintptr(unsafe.Pointer(name)))
		if r := d.call(mSetFolder, uintptr(unsafe.Pointer(item))); r != 0 {
			t.Errorf("%s: SetFolder: %#x", c.name, r)
		}
		item.release()
		d.release()
	}
}
