package gui

import (
	"testing"

	"github.com/DemiCloud/doszer/internal/install"
	"github.com/DemiCloud/doszer/internal/library"
	"github.com/DemiCloud/doszer/internal/platform"
)

// A Windows 95 game: DOS and the Windows 3.1 shell greyed, the
// Windows 98 image offered.
func TestBootItems(t *testing.T) {
	a := &app{}
	a.files = []systemFile{
		{SystemFile: install.SystemFile{Name: "dosbox_pure/Windows 3.1", Shell: true}},
		{SystemFile: install.SystemFile{Name: "dosbox_pure/Windows 98"}},
	}
	g := &library.Game{Name: "Game", Kind: library.KindOS, Boot: "dosbox_pure/Windows 95",
		Need: platform.Need{Game: platform.Target{Platform: platform.Win32, Major: 4, Minor: 0}}}
	got := map[string]menuItem{}
	for _, it := range a.bootItems(g) {
		got[it.label] = it
	}
	if it := got["DOS"]; !it.off {
		t.Errorf("DOS offered to a Windows game: %+v", it)
	}
	if it := got["dosbox_pure/Windows 3.1"]; !it.off || it.keys != "too old" {
		t.Errorf("Windows 3.1 offered to a Windows 95 game: %+v", it)
	}
	if it := got["dosbox_pure/Windows 98"]; it.off || it.fn == nil {
		t.Errorf("Windows 98 not offered: %+v", it)
	}
}
