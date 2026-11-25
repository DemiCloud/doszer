package prepare

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DemiCloud/doszer/internal/builtin"
)

// madeAt is when writeZip's entries were made, in a time zone other than
// UTC: archive/zip writes it as local DOS fields plus a UTC extra field, as
// Windows' own ZIP tools do.
var madeAt = time.Date(1993, 11, 1, 3, 11, 0, 0, time.FixedZone("PST", -8*3600))

// writeZip writes files (a trailing "/" makes a directory entry) to a ZIP.
func writeZip(t *testing.T, p string, files map[string]string) {
	t.Helper()
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	for name, data := range files {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: madeAt})
		if err != nil {
			t.Fatal(err)
		}
		io.WriteString(w, data)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readZip(t *testing.T, p string) map[string]string {
	t.Helper()
	zr, err := zip.OpenReader(p)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		out[f.Name] = string(b)
	}
	return out
}

func TestShell(t *testing.T) {
	if _, _, err := builtin.RunExit(); err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	in := filepath.Join(dir, "Win31.dosz")
	writeZip(t, in, map[string]string{
		"Windows/WIN.COM":       "win",
		"Windows/win.ini":       "[windows]\r\nload=\r\n\r\n[Desktop]\r\nWallpaper=leaves.bmp\r\n",
		"Windows/SYSTEM.INI":    "[boot]\r\nshell=progman.exe\r\n\r\n[386Enh]\r\nPermSwapDOSDrive=C\r\nPermSwapSizeK=8192\r\n",
		"Windows/TEMP/":         "",
		"Windows/~WRS0001.TMP":  "junk",
		"386SPART.PAR":          "swap",
		"WINDOWS/SYSTEM/A.DLL":  "a",
		"Windows/PROGMAN.INI":   "[Groups]\r\n",
		"runexit/old.txt":       "old",
		"Windows/SYSTEM/EMPTY/": "",
	})
	add := filepath.Join(dir, "add")
	os.MkdirAll(filepath.Join(add, "WINDOWS"), 0o755)
	os.WriteFile(filepath.Join(add, "WINDOWS", "PROGMAN.INI"), []byte("[Groups]\r\nGroup1=C:\\WINDOWS\\TOOLS.GRP\r\n"), 0o644)
	os.WriteFile(filepath.Join(add, "EXTRA.TXT"), []byte("x"), 0o644)

	out := filepath.Join(dir, "Win31 (games).dosz")
	rep, err := Shell(ShellOptions{Input: in, Output: out, Add: []string{add}})
	if err != nil {
		t.Fatal(err)
	}
	got := readZip(t, out)

	for _, gone := range []string{"386SPART.PAR", "Windows/~WRS0001.TMP"} {
		if _, ok := got[gone]; ok {
			t.Errorf("%s not removed", gone)
		}
	}
	if s := got["Windows/SYSTEM.INI"]; strings.Contains(s, "PermSwap") || !strings.Contains(s, "shell=progman.exe\r\n") {
		t.Errorf("SYSTEM.INI = %q", s)
	}
	wantWin := "[windows]\r\nload=\r\n\r\n[Desktop]\r\nWallpaper=(None)\r\nPattern=(None)\r\n\r\n[colors]\r\nBackground=0 0 0\r\n"
	if s := got["Windows/win.ini"]; s != wantWin {
		t.Errorf("win.ini = %q, want %q", s, wantWin)
	}
	exe, license, _ := builtin.RunExit()
	if got["runexit/RUNEXIT.EXE"] != string(exe) {
		t.Error("RUNEXIT.EXE missing, or not in the existing directory's spelling")
	}
	if s := got["runexit/LICENSE.TXT"]; !strings.Contains(s, "\r\n") || len(s) < len(license) {
		t.Errorf("LICENSE.TXT = %q", s)
	}
	if got["Windows/PROGMAN.INI"] != "[Groups]\r\nGroup1=C:\\WINDOWS\\TOOLS.GRP\r\n" || got["EXTRA.TXT"] != "x" {
		t.Error("--add files not laid over the shell")
	}
	for _, d := range []string{"Windows/TEMP/", "Windows/SYSTEM/EMPTY/"} {
		if _, ok := got[d]; !ok {
			t.Errorf("empty directory %s dropped", d)
		}
	}
	if _, ok := got["WINDOWS/SYSTEM/"]; ok {
		t.Error("directory entry written for a directory with files")
	}
	zr, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		if f.Name == "Windows/WIN.COM" && (f.ModifiedDate != 13<<9|11<<5|1 || f.ModifiedTime != 3<<11|11<<5) { //nolint:staticcheck
			t.Errorf("WIN.COM's DOS date/time = %#x %#x, want the input's", f.ModifiedDate, f.ModifiedTime) //nolint:staticcheck
		}
	}
	zr.Close()
	if rep.Entries != len(got) {
		t.Errorf("Entries = %d, wrote %d", rep.Entries, len(got))
	}

	// Deterministic: a second run writes the same bytes.
	out2 := filepath.Join(dir, "again.dosz")
	if _, err := Shell(ShellOptions{Input: in, Output: out2, Add: []string{add}}); err != nil {
		t.Fatal(err)
	}
	a, _ := os.ReadFile(out)
	b, _ := os.ReadFile(out2)
	if !bytes.Equal(a, b) {
		t.Error("two runs differ")
	}

	// Refuses to overwrite without Force; a dry run writes nothing.
	if _, err := Shell(ShellOptions{Input: in, Output: out}); err == nil {
		t.Error("overwrote an existing output without Force")
	}
	dry := filepath.Join(dir, "dry.dosz")
	if _, err := Shell(ShellOptions{Input: in, Output: dry, DryRun: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dry); err == nil {
		t.Error("dry run wrote output")
	}
}

func TestShellRejects(t *testing.T) {
	if _, _, err := builtin.RunExit(); err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	tests := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"not windows", map[string]string{"GAME.EXE": "x"}, "not a Windows 3.x shell"},
		{"case clash", map[string]string{"WINDOWS/WIN.COM": "a", "windows/win.com": "b"}, "appears twice"},
	}
	for _, tt := range tests {
		in := filepath.Join(dir, tt.name+".zip")
		writeZip(t, in, tt.files)
		_, err := Shell(ShellOptions{Input: in, Output: filepath.Join(dir, "out.dosz"), DryRun: true})
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: err = %v, want %q", tt.name, err, tt.want)
		}
	}
}
