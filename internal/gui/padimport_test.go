package gui

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"

	"github.com/DemiCloud/doszer/internal/padmap"
)

func TestImportPads(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, data []byte) string {
		p := filepath.Join(dir, name)
		os.WriteFile(p, data, 0o644)
		return p
	}
	manifestPath := write("DOSZ.TOML", []byte("format = 1\nname = 'Game'\n\n[boot]\nexec = 'C:\\GAME.EXE'\n\n[[input.pad]]\nb = 'space Fire'\nwheel = ['f1 Help']\n"))
	raw := padmap.EncodePADMAP([]padmap.Pad{{Controls: map[string]string{"b": "space", "a": "leftctrl"}}})
	padmapPath := write("PADMAP.DBP", raw)
	ymlPath := write("DOS.YML", []byte("cycles: max\r\ninput_pad_b: space Fire\r\ninput_pad_a: leftctrl Use\r\n"))
	arch := filepath.Join(dir, "Game.dosz")
	f, _ := os.Create(arch)
	zw := zip.NewWriter(f)
	for name, data := range map[string][]byte{"PADMAP.DBP": raw, "DOS.YML": []byte("input_pad_a: leftctrl Use\r\n"), "GAME.EXE": []byte("MZ")} {
		w, _ := zw.Create(name)
		w.Write(data)
	}
	zw.Close()
	f.Close()

	none := []padmap.Pad{{Controls: map[string]string{}}}
	tests := []struct {
		name string
		cur  []padmap.Pad
		path string
		want map[string]string
	}{
		{"manifest", none, manifestPath, map[string]string{"b": "space Fire"}},
		{"padmap alone", none, padmapPath, map[string]string{"b": "space", "a": "leftctrl"}},
		{"padmap after a labelled mapping", []padmap.Pad{{Controls: map[string]string{"x": "space Fire"}}}, padmapPath, map[string]string{"b": "space Fire", "a": "leftctrl"}},
		{"yml labels the bindings", []padmap.Pad{{Controls: map[string]string{"b": "space", "a": "leftctrl"}}}, ymlPath, map[string]string{"b": "space Fire", "a": "leftctrl Use"}},
		{"yml alone is port 1", none, ymlPath, map[string]string{"b": "space Fire", "a": "leftctrl Use"}},
		{"archive: padmap labelled by its yml", none, arch, map[string]string{"b": "space", "a": "leftctrl Use"}},
	}
	for _, tt := range tests {
		got, _, err := importPads(tt.cur, tt.path)
		if err != nil {
			t.Errorf("%s: %v", tt.name, err)
			continue
		}
		if len(got) == 0 || len(got[0].Controls) != len(tt.want) {
			t.Errorf("%s: %+v, want %v", tt.name, got, tt.want)
			continue
		}
		for k, v := range tt.want {
			if got[0].Controls[k] != v {
				t.Errorf("%s: %s = %q, want %q", tt.name, k, got[0].Controls[k], v)
			}
		}
	}
	if _, _, err := importPads(none, write("notes.txt", []byte("x"))); err == nil {
		t.Error("imported a text file")
	}
}
