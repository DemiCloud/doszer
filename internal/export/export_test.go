package export

import (
	"archive/zip"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func writeZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for name, content := range files {
		fw, _ := w.Create(name)
		fw.Write([]byte(content))
	}
	w.Close()
	f.Close()
}

// TestPlan: a game goes into its system's folder with its parent beside
// it and its image into bios; a second export finds it all the same; an
// archive that changed is kept unless overwrite, an image always.
func TestPlan(t *testing.T) {
	src, sys, dest := t.TempDir(), t.TempDir(), t.TempDir()
	writeZip(t, filepath.Join(src, "Base.dosp"), map[string]string{"AUTOBOOT.DBP": "O*dosbox_pure/Windows 98\n", "X.DLL": "x"})
	writeZip(t, filepath.Join(src, "Game.dosz"), map[string]string{"Base.dosp.parent": "", "GAME.EXE": "MZ"})
	writeZip(t, filepath.Join(src, "Old.dosz"), map[string]string{"AUTOBOOT.DBP": "S*Win31\n"})
	writeZip(t, filepath.Join(src, "Plain.dosz"), map[string]string{"AUTOBOOT.DBP": "C:\\GAME.EXE\n"})
	img := filepath.Join(sys, "dosbox_pure", "Windows 98.vhd")
	os.MkdirAll(filepath.Dir(img), 0o755)
	os.WriteFile(img, make([]byte, 7<<20), 0o644)
	tg := Target{ROMs: filepath.Join(dest, "roms"), BIOS: filepath.Join(dest, "bios")}
	archives := []string{filepath.Join(src, "Game.dosz"), filepath.Join(src, "Old.dosz"), filepath.Join(src, "Plain.dosz")}

	actions := func(steps []Step) []string {
		var out []string
		for _, s := range steps {
			rel, _ := filepath.Rel(dest, s.To)
			out = append(out, s.Action+" "+filepath.ToSlash(rel))
		}
		return out
	}
	steps, err := Plan(archives, sys, tg, false)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"copy roms/windows9x/Game.dosz",
		"copy roms/windows9x/Base.dosp",
		"copy bios/dosbox_pure/Windows 98.vhd",
		"copy roms/windows3x/Old.dosz",
		"missing bios/Win31",
		"copy roms/dos/Plain.dosz",
	}
	if got := actions(steps); !reflect.DeepEqual(got, want) {
		t.Fatalf("first export:\n got %q\nwant %q", got, want)
	}
	if err := Apply(steps, nil); err != nil {
		t.Fatal(err)
	}
	steps, _ = Plan(archives[:1], sys, tg, false)
	if got, want := actions(steps), []string{"same roms/windows9x/Game.dosz", "same roms/windows9x/Base.dosp", "same bios/dosbox_pure/Windows 98.vhd"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("second export:\n got %q\nwant %q", got, want)
	}

	// A shell not here but there already is no problem.
	os.MkdirAll(filepath.Join(tg.BIOS), 0o755)
	writeZip(t, filepath.Join(tg.BIOS, "Win31.dosz"), map[string]string{"WINDOWS/WIN.COM": "x"})
	steps, _ = Plan(archives[1:2], sys, tg, false)
	if got, want := actions(steps), []string{"same roms/windows3x/Old.dosz", "same bios/Win31.dosz"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("shell there already:\n got %q\nwant %q", got, want)
	}

	writeZip(t, filepath.Join(src, "Game.dosz"), map[string]string{"Base.dosp.parent": "", "GAME.EXE": "MZ2"})
	os.WriteFile(img, make([]byte, 8<<20), 0o644)
	for _, tt := range []struct {
		overwrite bool
		want      []string
	}{
		{false, []string{"conflict roms/windows9x/Game.dosz", "same roms/windows9x/Base.dosp", "conflict bios/dosbox_pure/Windows 98.vhd"}},
		{true, []string{"replace roms/windows9x/Game.dosz", "same roms/windows9x/Base.dosp", "conflict bios/dosbox_pure/Windows 98.vhd"}},
	} {
		steps, _ = Plan(archives[:1], sys, tg, tt.overwrite)
		if got := actions(steps); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("changed, overwrite %v:\n got %q\nwant %q", tt.overwrite, got, tt.want)
		}
	}
}

// TestZip: the same layout into a ZIP, archives stored and images
// deflated, filtered by system, never replacing a file.
func TestZip(t *testing.T) {
	src, sys, out := t.TempDir(), t.TempDir(), t.TempDir()
	writeZip(t, filepath.Join(src, "Base.dosp"), map[string]string{"AUTOBOOT.DBP": "O*dosbox_pure/Windows 98\n"})
	writeZip(t, filepath.Join(src, "Game.dosz"), map[string]string{"Base.dosp.parent": "", "GAME.EXE": "MZ"})
	writeZip(t, filepath.Join(src, "Old.dosz"), map[string]string{"AUTOBOOT.DBP": "S*Win31\n"})
	writeZip(t, filepath.Join(src, "Plain.dosz"), map[string]string{"AUTOBOOT.DBP": "C:\\GAME.EXE\n"})
	img := filepath.Join(sys, "dosbox_pure", "Windows 98.vhd")
	os.MkdirAll(filepath.Dir(img), 0o755)
	os.WriteFile(img, make([]byte, 7<<20), 0o644)
	all := []string{filepath.Join(src, "Game.dosz"), filepath.Join(src, "Old.dosz"), filepath.Join(src, "Plain.dosz")}

	for _, tt := range []struct {
		systems []string
		want    []string
	}{
		{Systems, []string{"Game.dosz", "Old.dosz", "Plain.dosz"}},
		{[]string{"dos"}, []string{"Plain.dosz"}},
		{[]string{"windows3x", "windows9x"}, []string{"Game.dosz", "Old.dosz"}},
		{nil, nil},
	} {
		var got []string
		for _, a := range Filter(all, tt.systems) {
			got = append(got, filepath.Base(a))
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("Filter(%v) = %q, want %q", tt.systems, got, tt.want)
		}
	}

	for _, tt := range []struct {
		flat bool
		want []string
	}{
		{true, []string{
			"copy roms/Game.dosz",
			"copy roms/Base.dosp",
			"copy bios/dosbox_pure/Windows 98.vhd",
			"copy roms/Old.dosz",
			"missing bios/Win31",
			"copy roms/Plain.dosz",
		}},
	} {
		steps, err := PlanZip(all, sys, tt.flat)
		if err != nil {
			t.Fatal(err)
		}
		var plan []string
		for _, s := range steps {
			plan = append(plan, s.Action+" "+s.To)
		}
		if !reflect.DeepEqual(plan, tt.want) {
			t.Errorf("flat %v:\n got %q\nwant %q", tt.flat, plan, tt.want)
		}
	}

	// Two games of one name, from different folders: one place for both.
	other := t.TempDir()
	writeZip(t, filepath.Join(other, "Plain.dosz"), map[string]string{"AUTOBOOT.DBP": "C:\\GAME.EXE\n"})
	if _, err := PlanZip(append(all, filepath.Join(other, "Plain.dosz")), sys, true); err == nil {
		t.Error("PlanZip put two files in one place")
	}

	steps, err := PlanZip(all, sys, false)
	if err != nil {
		t.Fatal(err)
	}
	var plan []string
	for _, s := range steps {
		plan = append(plan, s.Action+" "+s.To)
	}
	want := []string{
		"copy roms/windows9x/Game.dosz",
		"copy roms/windows9x/Base.dosp",
		"copy bios/dosbox_pure/Windows 98.vhd",
		"copy roms/windows3x/Old.dosz",
		"missing bios/Win31",
		"copy roms/dos/Plain.dosz",
	}
	if !reflect.DeepEqual(plan, want) {
		t.Fatalf("plan:\n got %q\nwant %q", plan, want)
	}

	day := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	path := ZipName(out, day)
	if filepath.Base(path) != "doszer 2026-11-26.zip" {
		t.Errorf("ZipName = %s", path)
	}
	if err := WriteZip(path, steps, nil); err != nil {
		t.Fatal(err)
	}
	if next := filepath.Base(ZipName(out, day)); next != "doszer 2026-11-26 (2).zip" {
		t.Errorf("ZipName with one there = %s", next)
	}
	if err := WriteZip(path, steps, nil); err == nil {
		t.Error("WriteZip replaced a file")
	}
	r, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	methods := map[string]uint16{}
	for _, f := range r.File {
		methods[f.Name] = f.Method
	}
	wantMethods := map[string]uint16{
		"roms/windows9x/Game.dosz":        zip.Store,
		"roms/windows9x/Base.dosp":        zip.Store,
		"bios/dosbox_pure/Windows 98.vhd": zip.Deflate,
		"roms/windows3x/Old.dosz":         zip.Store,
		"roms/dos/Plain.dosz":             zip.Store,
	}
	if !reflect.DeepEqual(methods, wantMethods) {
		t.Errorf("entries %v, want %v", methods, wantMethods)
	}
	if left, _ := filepath.Glob(filepath.Join(out, ".doszer-export-*")); len(left) > 0 {
		t.Errorf("temporary files left: %q", left)
	}
}
