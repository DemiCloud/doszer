package gui

import (
	"errors"
	"syscall"
	"unsafe"
)

// Windows' Explorer-style file dialogs (IFileOpenDialog, IFileSaveDialog:
// shobjidl_core.h), driven through COM without cgo: an object is a
// pointer to its vtable, each method called with the object first.

type guid struct {
	data1        uint32
	data2, data3 uint16
	data4        [8]byte
}

var (
	clsidFileOpenDialog = guid{0xDC1C5A9C, 0xE88A, 0x4DDE, [8]byte{0xA5, 0xA1, 0x60, 0xF8, 0x2A, 0x20, 0xAE, 0xF7}}
	iidIFileOpenDialog  = guid{0xD57C7288, 0xD4AD, 0x4768, [8]byte{0xBE, 0x02, 0x9D, 0x96, 0x95, 0x32, 0xD9, 0x60}}
	clsidFileSaveDialog = guid{0xC0B4E2F3, 0xBA21, 0x4773, [8]byte{0x8D, 0xBA, 0x33, 0x5E, 0xC9, 0x46, 0xEB, 0x8B}}
	iidIFileSaveDialog  = guid{0x84BCCD23, 0x5FDE, 0x4CDB, [8]byte{0xAE, 0xA4, 0xAF, 0x64, 0xB8, 0x3D, 0x78, 0xAB}}
	iidIShellItem       = guid{0x43826D1E, 0xE718, 0x42EE, [8]byte{0xBC, 0x55, 0xA1, 0xE2, 0x61, 0xC3, 0x7B, 0xFE}}

	coCreateInstance            = ole32.NewProc("CoCreateInstance")
	shCreateItemFromParsingName = shell32.NewProc("SHCreateItemFromParsingName")
	getActiveWindow             = syscall.NewLazyDLL("user32.dll").NewProc("GetActiveWindow")
)

// comObj is a COM object: its first word points to its vtable.
type comObj struct{ vtbl *[64]uintptr }

func (o *comObj) call(method int, args ...uintptr) uintptr {
	r, _, _ := syscall.SyscallN(o.vtbl[method], append([]uintptr{uintptr(unsafe.Pointer(o))}, args...)...)
	return r
}

func (o *comObj) release() { o.call(2) }

// vtable slots: IUnknown, IModalWindow, IFileDialog, IFileOpenDialog,
// IShellItem, IShellItemArray.
const (
	mShow             = 3
	mSetFileTypes     = 4
	mSetOptions       = 9
	mGetOptions       = 10
	mSetFolder        = 12
	mSetFileName      = 15
	mSetTitle         = 17
	mGetResult        = 20
	mSetDefaultExt    = 22
	mGetResults       = 27 // IFileOpenDialog
	mItemDisplayName  = 5  // IShellItem::GetDisplayName
	mArrayGetCount    = 7  // IShellItemArray
	mArrayGetItemAt   = 8
	fosOverwrite      = 0x2
	fosNoChangeDir    = 0x8
	fosPickFolders    = 0x20
	fosFileSystem     = 0x40
	fosMultiSelect    = 0x200
	fosPathMustExist  = 0x800
	fosFileMustExist  = 0x1000
	sigdnFileSysPath  = 0x80058000
	hresultCanceled   = 0x800704C7
	coinitApartment   = 2
	clsctxInprocSever = 1
)

// shellDialog asks for folders or files with Explorer's dialog: open
// (folder: pick a folder; multi: several files) or save (saveName, ext).
// It returns the paths chosen (nil if canceled), or an error if the
// dialog couldn't be made, for the old dialogs to stand in.
func shellDialog(save, folder, multi bool, title, start, saveName, ext string, filters [][2]string) ([]string, error) {
	coInitializeEx.Call(0, coinitApartment)
	clsid, iid := &clsidFileOpenDialog, &iidIFileOpenDialog
	if save {
		clsid, iid = &clsidFileSaveDialog, &iidIFileSaveDialog
	}
	var d *comObj
	if r, _, _ := coCreateInstance.Call(uintptr(unsafe.Pointer(clsid)), 0, clsctxInprocSever, uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&d))); r != 0 || d == nil {
		return nil, errors.New("no Explorer file dialog")
	}
	defer d.release()

	var opts uint32
	d.call(mGetOptions, uintptr(unsafe.Pointer(&opts)))
	opts |= fosNoChangeDir | fosFileSystem | fosPathMustExist
	switch {
	case save:
		opts |= fosOverwrite
	case folder:
		opts |= fosPickFolders
	default:
		opts |= fosFileMustExist
	}
	if multi {
		opts |= fosMultiSelect
	}
	d.call(mSetOptions, uintptr(opts))

	var keep []any // UTF-16 strings the dialog points to, kept alive
	str := func(s string) uintptr {
		p, _ := syscall.UTF16PtrFromString(s)
		keep = append(keep, p)
		return uintptr(unsafe.Pointer(p))
	}
	d.call(mSetTitle, str(title))
	if len(filters) > 0 && !folder {
		spec := make([][2]uintptr, len(filters))
		for i, f := range filters {
			spec[i] = [2]uintptr{str(f[0]), str(f[1])}
		}
		keep = append(keep, spec)
		d.call(mSetFileTypes, uintptr(len(spec)), uintptr(unsafe.Pointer(&spec[0])))
	}
	if save {
		d.call(mSetFileName, str(saveName))
		if ext != "" {
			d.call(mSetDefaultExt, str(ext))
		}
	}
	if start != "" {
		var item *comObj
		if r, _, _ := shCreateItemFromParsingName.Call(str(start), 0, uintptr(unsafe.Pointer(&iidIShellItem)), uintptr(unsafe.Pointer(&item))); r == 0 && item != nil {
			d.call(mSetFolder, uintptr(unsafe.Pointer(item)))
			item.release()
		}
	}

	owner, _, _ := getActiveWindow.Call() // doszer's window: the dialog is modal over it
	hr := d.call(mShow, owner)
	keepAlive(keep)
	if uint32(hr) == hresultCanceled {
		return nil, nil
	}
	if hr != 0 {
		return nil, errors.New("file dialog failed")
	}

	path := func(item *comObj) string {
		var p *uint16
		if item.call(mItemDisplayName, sigdnFileSysPath, uintptr(unsafe.Pointer(&p))) != 0 || p == nil {
			return ""
		}
		defer coTaskMemFree.Call(uintptr(unsafe.Pointer(p)))
		return utf16At(p)
	}
	var out []string
	if multi && !save {
		var arr *comObj
		if d.call(mGetResults, uintptr(unsafe.Pointer(&arr))) != 0 || arr == nil {
			return nil, nil
		}
		defer arr.release()
		var n uint32
		arr.call(mArrayGetCount, uintptr(unsafe.Pointer(&n)))
		for i := uint32(0); i < n; i++ {
			var item *comObj
			if arr.call(mArrayGetItemAt, uintptr(i), uintptr(unsafe.Pointer(&item))) == 0 && item != nil {
				if p := path(item); p != "" {
					out = append(out, p)
				}
				item.release()
			}
		}
		return out, nil
	}
	var item *comObj
	if d.call(mGetResult, uintptr(unsafe.Pointer(&item))) != 0 || item == nil {
		return nil, nil
	}
	defer item.release()
	if p := path(item); p != "" {
		out = append(out, p)
	}
	return out, nil
}

// utf16At reads a NUL-terminated UTF-16 string.
func utf16At(p *uint16) string {
	var s []uint16
	for i := 0; i < 1<<15; i++ {
		c := *(*uint16)(unsafe.Add(unsafe.Pointer(p), 2*i))
		if c == 0 {
			break
		}
		s = append(s, c)
	}
	return syscall.UTF16ToString(s)
}

//go:noinline
func keepAlive(v []any) { _ = v }
