package start

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// dosExe and winExe are the smallest headers platform.Classify tells
// apart: a DOS program, and a PE program for Windows 4.0.
func dosExe() []byte { b := make([]byte, 64); copy(b, "MZ"); return b }

func winExe() []byte {
	b := make([]byte, 0x40+0x80)
	copy(b, "MZ")
	binary.LittleEndian.PutUint32(b[0x3C:], 0x40)
	copy(b[0x40:], "PE\x00\x00")
	binary.LittleEndian.PutUint16(b[0x40+24+48:], 4)
	return b
}

func source(t *testing.T, manifest string, files map[string][]byte) string {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "DOSZ.TOML"), []byte("format = 1\nname = 'Game'\n"+manifest), 0o644)
	for p, data := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		os.MkdirAll(filepath.Dir(full), 0o755)
		os.WriteFile(full, data, 0o644)
	}
	return dir
}

func TestCandidatesAndSet(t *testing.T) {
	t.Run("plain", func(t *testing.T) {
		dir := source(t, "", map[string][]byte{
			"GAME/GAME.EXE":   dosExe(),
			"GAME/SETUP.EXE":  dosExe(), // a helper
			"GAME/PLAY.BAT":   []byte("@echo off\r\ngame -x\r\n"),
			"GAME/WINVER.EXE": winExe(), // not under the plain model
		})
		cs, _, err := Candidates(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(cs) != 1 || cs[0].Path != "GAME/PLAY.BAT" || cs[0].Runs != "GAME/GAME.EXE" || !cs[0].Likely {
			t.Fatalf("candidates = %+v", cs)
		}
		res, err := Set(dir, cs[0].Path, Options{})
		if err != nil {
			t.Fatal(err)
		}
		data, _ := os.ReadFile(filepath.Join(dir, "DOSZ.TOML"))
		if res.Exec != `C:\GAME\PLAY.BAT` || !strings.Contains(string(data), `exec = 'C:\GAME\PLAY.BAT'`) {
			t.Errorf("exec %q, manifest:\n%s", res.Exec, data)
		}
		if cs, _, _ := Candidates(dir); !cs[0].Current {
			t.Error("the set program isn't current")
		}
	})

	t.Run("OS image, long names, registry", func(t *testing.T) {
		dir := source(t, "[boot]\nos = 'dosbox_pure/Win98'\n", map[string][]byte{
			"Program Files/Some Game/Game.exe":    winExe(),
			"Program Files/Some Game/INSTALL.REG": []byte("REGEDIT4\r\n"),
			"Program Files/Some Game/Uninst.exe":  winExe(), // a helper
		})
		cs, _, err := Candidates(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(cs) != 1 || cs[0].Path != "Program Files/Some Game/Game.exe" || cs[0].Target != "Windows 95" {
			t.Fatalf("candidates = %+v", cs)
		}
		res, err := Set(dir, cs[0].Path, Options{})
		if err != nil {
			t.Fatal(err)
		}
		want := "@ECHO OFF\r\nREGEDIT /S \"D:\\Program Files\\Some Game\\INSTALL.REG\"\r\nD:\r\nCD \"\\Program Files\\Some Game\"\r\nSTART /W Game.exe\r\n"
		if res.Batch != want {
			t.Errorf("WINDOWS.BAT =\n%q\nwant\n%q", res.Batch, want)
		}
		if _, err := Set(dir, cs[0].Path, Options{}); err == nil {
			t.Error("replaced WINDOWS.BAT without force")
		}
		if cs, _, _ := Candidates(dir); !cs[0].Current {
			t.Errorf("the set program isn't current: %+v", cs)
		}
	})

	t.Run("OS image, Sierra's layout", func(t *testing.T) {
		// As Betrayal in Antara installs: the game in SIERRA/ANTARA,
		// Sierra's tools beside it, install's INSTALL.REG at SIERRA.
		dir := source(t, "[boot]\nos = 'dosbox_pure/Win98'\n", map[string][]byte{
			"SIERRA/ANTARA/ANTARAR.EXE": winExe(),
			"SIERRA/SIGSPAT.EXE":        winExe(),
			"SIERRA/INSTALL.REG":        []byte("REGEDIT4\r\n"),
		})
		cs, _, err := Candidates(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(cs) != 2 || cs[0].Path != "SIERRA/ANTARA/ANTARAR.EXE" || !cs[0].Likely {
			t.Fatalf("candidates = %+v", cs)
		}
		res, err := Set(dir, cs[0].Path, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(res.Batch, "@ECHO OFF\r\nREGEDIT /S D:\\SIERRA\\INSTALL.REG\r\nD:\r\nCD \\SIERRA\\ANTARA\r\n") {
			t.Errorf("WINDOWS.BAT =\n%q", res.Batch)
		}
		// 256 colours first, as the game asks (the example manifest's line).
		res, err = Set(dir, cs[0].Path, Options{Force: true, Display: "640x480x8"})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(res.Batch, "INSTALL.REG\r\nSTART /W RUNDLL DESKCP16.DLL,QUICKRES_RUNDLLENTRY 640x480x8\r\nD:\r\n") {
			t.Errorf("WINDOWS.BAT =\n%q", res.Batch)
		}
		if _, err := Set(dir, cs[0].Path, Options{Force: true, Display: "256"}); err == nil {
			t.Error("a bad display mode was accepted")
		}
	})

	t.Run("shell", func(t *testing.T) {
		dir := source(t, "[boot]\nshell = 'dosbox_pure/Win31'\n", map[string][]byte{"SKI/SKI.EXE": winExe()})
		res, err := Set(dir, "SKI/SKI.EXE", Options{})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(res.Batch, "WIN C:\\RUNEXIT\\RUNEXIT.EXE /show=3 C:\\SKI\\SKI.EXE\r\n") {
			t.Errorf("WINDOWS.BAT =\n%s", res.Batch)
		}
		if _, err := Set(dir, "SKI/SKI.EXE", Options{Force: true, Display: "640x480x8"}); err == nil {
			t.Error("--display accepted under the shell model")
		}
		if _, err := Set(dir, "Long Name/x.exe", Options{Force: true}); err == nil {
			t.Error("a missing program was accepted")
		}
	})
}
