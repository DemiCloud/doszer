package start

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeParent(t *testing.T, path string, files map[string][]byte) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for name, data := range files {
		fw, _ := w.Create(name)
		fw.Write(data)
	}
	w.Close()
	f.Close()
}

// TestChild: a child of a parent booting a shell or an OS image boots
// the same with a WINDOWS.BAT starting the program; of a DOS parent, it
// runs the program as boot.exec; each names the parent with its marker.
func TestChild(t *testing.T) {
	tests := []struct {
		name     string
		autoboot string
		program  string
		wantBoot string // AUTOBOOT.DBP's first line
		wantBat  string // a line WINDOWS.BAT holds, "" for none
		wantErr  string
	}{
		{name: "shell", autoboot: "S*Win31\n", program: "PACK/GAME.EXE", wantBoot: "S*Win31", wantBat: `WIN C:\PACK\GAME.EXE`},
		{name: "os image", autoboot: "O*Win98\n", program: "PACK/WIN.EXE", wantBoot: "O*Win98", wantBat: `START /W WIN.EXE`},
		{name: "dos", program: `C:\PACK\GAME.EXE`, wantBoot: `C:\PACK\GAME.EXE`},
		{name: "no such program", program: "PACK/NONE.EXE", wantErr: "no PACK/NONE.EXE"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			parent := filepath.Join(dir, "Pack.dosp")
			files := map[string][]byte{"PACK/GAME.EXE": dosExe(), "PACK/WIN.EXE": winExe(), "PACK/SETUP.EXE": dosExe()}
			if tt.autoboot != "" {
				files["AUTOBOOT.DBP"] = []byte(tt.autoboot)
			}
			writeParent(t, parent, files)

			if tt.wantErr == "" {
				cs, err := ParentCandidates(parent)
				if err != nil {
					t.Fatal(err)
				}
				for _, c := range cs {
					if c.Path == "PACK/SETUP.EXE" {
						t.Error("SETUP.EXE (a helper) offered")
					}
				}
			}
			res, err := Child(parent, tt.program, ChildOptions{Name: "A Game"})
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if res.Output != filepath.Join(dir, "A Game.dosz") {
				t.Errorf("output %s", res.Output)
			}
			zr, err := zip.OpenReader(res.Output)
			if err != nil {
				t.Fatal(err)
			}
			defer zr.Close()
			got := map[string]string{}
			for _, f := range zr.File {
				rc, _ := f.Open()
				b, _ := io.ReadAll(rc)
				rc.Close()
				got[f.Name] = string(b)
			}
			if _, ok := got["Pack.dosp.parent"]; !ok {
				t.Error("no parent marker")
			}
			if line, _, _ := strings.Cut(got["AUTOBOOT.DBP"], "\n"); strings.TrimSpace(line) != tt.wantBoot {
				t.Errorf("AUTOBOOT.DBP %q, want %q", line, tt.wantBoot)
			}
			bat, ok := got["WINDOWS.BAT"]
			if tt.wantBat == "" && ok || tt.wantBat != "" && !strings.Contains(bat, tt.wantBat) {
				t.Errorf("WINDOWS.BAT %q, want one with %q", bat, tt.wantBat)
			}
			if _, err := Child(parent, tt.program, ChildOptions{Name: "A Game"}); err == nil {
				t.Error("replaced an existing child without Force")
			}
		})
	}
}
