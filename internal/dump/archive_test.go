package dump

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeZip(t *testing.T, p string, files map[string]string) {
	t.Helper()
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, data := range files {
		h := &zip.FileHeader{Name: name, Method: zip.Deflate}
		h.SetModTime(time.Date(1995, 2, 18, 17, 37, 0, 0, time.UTC)) //nolint:staticcheck // the DOS fields
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(data))
	}
	zw.Close()
	f.Close()
}

func TestArchiveKind(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"game.zip", map[string]string{"GODDESS.EXE": "MZ", "GODDESS.LBR": "x"}, ArchiveFiles},
		{"disc.zip", map[string]string{"Disc.cue": "FILE", "Disc.bin": "x"}, ArchiveDisc},
		{"two.zip", map[string]string{"a.cue": "", "b.cue": ""}, ArchiveFiles},
		{"made.dosz", map[string]string{"DOSZ.TOML": "format = 1\nname = 'x'\n\n[boot]\nexec = 'C:\\GAME.EXE'\n", "GAME.EXE": "MZ"}, ArchiveDoszer},
	}
	for _, tt := range tests {
		p := filepath.Join(dir, tt.name)
		writeZip(t, p, tt.files)
		if got, err := ArchiveKind(p); err != nil || got != tt.want {
			t.Errorf("%s: %q, %v; want %q", tt.name, got, err, tt.want)
		}
	}
}

func TestImportArchive(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "game.zip")
	writeZip(t, p, map[string]string{"GODDESS.EXE": "MZ", "DATA/LEVEL1.DAT": "level", "SUB\\DOS.TXT": "dos"})
	out := filepath.Join(dir, "src")
	n, err := ImportArchive(p, out)
	if err != nil || n != 3 {
		t.Fatalf("%d, %v", n, err)
	}
	for _, f := range []string{"GODDESS.EXE", "DATA/LEVEL1.DAT", "SUB/DOS.TXT"} {
		fi, err := os.Stat(filepath.Join(out, filepath.FromSlash(f)))
		if err != nil {
			t.Fatal(err)
		}
		if m := fi.ModTime(); m.Year() != 1995 || m.Hour() != 17 || m.Minute() != 37 {
			t.Errorf("%s dated %v, want the archive's 1995-02-18 17:37", f, m)
		}
	}
	if _, err := ImportArchive(p, out); err == nil {
		t.Error("extracted over existing files")
	}

	bad := filepath.Join(dir, "bad.zip")
	writeZip(t, bad, map[string]string{"../evil.txt": "x"})
	if _, err := ImportArchive(bad, filepath.Join(dir, "src2")); err == nil {
		t.Error("extracted outside the folder")
	}
	if _, err := os.Stat(filepath.Join(dir, "evil.txt")); err == nil {
		t.Error("wrote outside the folder")
	}
}

func TestInspectProblems(t *testing.T) {
	dir := t.TempDir()
	valid := "format = 1\nname = 'Game'\n\n[boot]\nexec = 'C:\\GAME.EXE'\n"
	tests := []struct {
		name, manifest, kind, problem string
	}{
		{"none", "", ArchiveFiles, "no manifest"},
		{"valid", valid, ArchiveDoszer, ""},
		{"newer", "format = 9\nname = 'Game'\n", ArchiveFiles, "a newer manifest (format 9; this doszer reads format 1)"},
		{"no format", "name = 'Game'\n", ArchiveFiles, "a manifest that isn't valid: "},
		{"broken", "name = [", ArchiveFiles, "a manifest that isn't valid: "},
	}
	for _, tt := range tests {
		files := map[string]string{"GAME.EXE": "MZ"}
		if tt.manifest != "" {
			files["DOSZ.TOML"] = tt.manifest
		}
		p := filepath.Join(dir, tt.name+".dosz")
		writeZip(t, p, files)
		st, err := Inspect(p)
		if err != nil {
			t.Fatal(err)
		}
		if st.Kind != tt.kind || !strings.HasPrefix(st.Problem, tt.problem) || (tt.problem == "" && st.Problem != "") {
			t.Errorf("%s: %+v, want %s %q", tt.name, st, tt.kind, tt.problem)
		}
	}
}
