package lint

import (
	"reflect"
	"testing"

	"github.com/DemiCloud/doszer/internal/chain"
	"github.com/DemiCloud/doszer/internal/info"
	"github.com/DemiCloud/doszer/internal/manifest"
	"github.com/DemiCloud/doszer/internal/platform"
)

func TestCheck(t *testing.T) {
	win95 := platform.Need{Game: platform.Target{Platform: platform.Win32, Major: 4}}
	win31 := platform.Need{Game: platform.Target{Platform: platform.Win16, Major: 3, Minor: 10}}
	media := &manifest.Media{}
	good := func() *info.Report {
		return &info.Report{Path: "/roms/Game.dosz", Manifest: &manifest.Manifest{}, Icon: "32x32", Programs: 1,
			Need: win95, Discs: []info.Disc{{Path: "Game.cue", Data: 1, Audio: 2, Ogg: 2, Media: media}}}
	}
	osChain := func(img string) *chain.Chain {
		return &chain.Chain{Links: []chain.Link{{Found: true}}, BootFrom: 0, Kind: chain.KindOS, Boot: img, System: "/sys/" + img + ".vhd"}
	}
	tests := []struct {
		name   string
		report func(r *info.Report)
		chain  *chain.Chain
		users  int
		want   []string // checks found
	}{
		{name: "good", chain: osChain("dosbox_pure/Windows 98")},
		{name: "no manifest, no icon", report: func(r *info.Report) { r.Manifest, r.Icon = nil, "" }, chain: osChain("Win98"), want: []string{"manifest", "icon"}},
		{name: "unidentified WAV disc", report: func(r *info.Report) { r.Discs[0].Media, r.Discs[0].WAV, r.Discs[0].Ogg = nil, 2, 0 }, chain: osChain("Win98"), want: []string{"media", "audio"}},
		{name: "WAV asked for", report: func(r *info.Report) { r.Manifest.Pack.Audio, r.Discs[0].WAV, r.Discs[0].Ogg = "wav", 2, 0 }, chain: osChain("Win98")},
		{name: "image too old", chain: osChain("Windows 3.11"), want: []string{"os"}},
		{name: "unknown image", chain: osChain("Games")},
		{name: "shell runs Win16", report: func(r *info.Report) { r.Need = win31 }, chain: &chain.Chain{Links: []chain.Link{{Found: true}}, BootFrom: 0, Kind: chain.KindShell, Boot: "Win31", System: "x"}},
		{name: "Windows game boots DOS", chain: &chain.Chain{Links: []chain.Link{{Found: true}}, BootFrom: -1, Kind: chain.KindDOS}, want: []string{"os", "start"}},
		{name: "broken chain", chain: &chain.Chain{Links: []chain.Link{{Found: true}, {Name: "Base.dosp"}}, BootFrom: 0, Kind: chain.KindOS, Boot: "Win98"}, want: []string{"chain"}},
		{name: "files-only parent as .dosz", report: func(r *info.Report) { r.Programs, r.Need = 0, platform.Need{} }, chain: osChain("Win98"), users: 3, want: []string{"type"}},
		{name: "unused .dosp", report: func(r *info.Report) { r.Path, r.Icon = "/roms/Base.dosp", "" }, chain: osChain("Win98"), want: []string{"type"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := good()
			if tt.report != nil {
				tt.report(r)
			}
			var got []string
			for _, f := range check(r, tt.chain, tt.users, true) {
				got = append(got, f.Check)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("checks = %v, want %v", got, tt.want)
			}
		})
	}
}
