package install

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/DemiCloud/doszer/internal/manifest"
	"github.com/DemiCloud/doszer/internal/vdisk"
	"github.com/DemiCloud/doszer/internal/vhd"
)

var t0 = time.Date(1996, 12, 24, 23, 32, 0, 0, time.UTC)

func TestDiff(t *testing.T) {
	before := fstest.MapFS{
		"GAME/A.TXT": {Data: []byte("a"), ModTime: t0},
		"GAME/B.TXT": {Data: []byte("b"), ModTime: t0},
		"GAME/C.TXT": {Data: []byte("c"), ModTime: t0},
		"OLD.TXT":    {Data: []byte("old"), ModTime: t0},
	}
	after := fstest.MapFS{
		"game/a.txt":   {Data: []byte("a"), ModTime: t0},                // same file, other case
		"GAME/B.TXT":   {Data: []byte("bb"), ModTime: t0},               // size changed
		"GAME/C.TXT":   {Data: []byte("x"), ModTime: t0.Add(time.Hour)}, // time changed
		"GAME/NEW.EXE": {Data: []byte("new"), ModTime: t0},
	}
	got, err := Diff(before, after)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, c := range got {
		lines = append(lines, c.Kind.String()+" "+c.Path)
	}
	want := "modified GAME/B.TXT,modified GAME/C.TXT,added GAME/NEW.EXE,deleted OLD.TXT"
	if strings.Join(lines, ",") != want {
		t.Fatalf("Diff = %v\nwant  %s", lines, want)
	}
}

func TestPickDir(t *testing.T) {
	tests := []struct {
		paths   []string
		want    string
		wantErr bool
	}{
		{[]string{"GAME/A.EXE", "GAME/SUB/B.DAT"}, "GAME", false},
		{[]string{"GAME/A.EXE", "OTHER/B.DAT"}, "", true},
		{[]string{"A.EXE", "B.CFG"}, "", false},     // only root files: the root
		{[]string{"A.CFG", "GAME/B.EXE"}, "", true}, // root and a folder
		{nil, "", true},
	}
	for _, tt := range tests {
		var cs []Change
		for _, p := range tt.paths {
			cs = append(cs, Change{Path: p, Kind: Added})
		}
		got, err := pickDir(cs)
		if got != tt.want || (err != nil) != tt.wantErr {
			t.Errorf("pickDir(%v) = %q, %v", tt.paths, got, err)
		}
	}
}

func TestExtract(t *testing.T) {
	src := fstest.MapFS{
		"GAME/A.EXE":     {Data: []byte("new a"), ModTime: t0},
		"GAME/SUB/B.DAT": {Data: []byte("b"), ModTime: t0},
		"OTHER/C.TXT":    {Data: []byte("c"), ModTime: t0},
	}
	changes := []Change{
		{Path: "GAME/A.EXE", Kind: Modified, Modified: t0},
		{Path: "GAME/SUB/B.DAT", Kind: Added, Modified: t0},
		{Path: "GAME/GONE.TXT", Kind: Deleted},
		{Path: "OTHER/C.TXT", Kind: Added, Modified: t0},
	}

	t.Run("writes the folder with times", func(t *testing.T) {
		dest := t.TempDir()
		res, err := Extract(src, changes, "game", dest, false, false, false)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Written) != 2 {
			t.Fatalf("written %v", res.Written)
		}
		info, err := os.Stat(filepath.Join(dest, "GAME/SUB/B.DAT"))
		if err != nil || !info.ModTime().Equal(t0) {
			t.Fatalf("B.DAT: %v, %v", info, err)
		}
		if _, err := os.Stat(filepath.Join(dest, "OTHER")); err == nil {
			t.Fatal("extracted outside the folder")
		}
	})

	t.Run("conflict needs force; identical is skipped", func(t *testing.T) {
		dest := t.TempDir()
		os.MkdirAll(filepath.Join(dest, "GAME/SUB"), 0o755)
		os.WriteFile(filepath.Join(dest, "GAME/A.EXE"), []byte("old a"), 0o644)
		os.WriteFile(filepath.Join(dest, "GAME/SUB/B.DAT"), []byte("b"), 0o644)
		if _, err := Extract(src, changes, "GAME", dest, false, false, false); err == nil {
			t.Fatal("conflict not reported")
		}
		if b, _ := os.ReadFile(filepath.Join(dest, "GAME/A.EXE")); string(b) != "old a" {
			t.Fatal("wrote despite the conflict")
		}
		res, err := Extract(src, changes, "GAME", dest, true, false, false)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Written) != 1 || len(res.Unchanged) != 1 {
			t.Fatalf("written %v unchanged %v", res.Written, res.Unchanged)
		}
	})

	t.Run("after a session, a modified file is no conflict", func(t *testing.T) {
		dest := t.TempDir()
		os.MkdirAll(filepath.Join(dest, "GAME"), 0o755)
		os.WriteFile(filepath.Join(dest, "GAME/A.EXE"), []byte("old a"), 0o644)
		res, err := Extract(src, changes, "GAME", dest, false, true, false)
		if err != nil || len(res.Written) != 2 {
			t.Fatalf("%v, %v", res, err)
		}
		if b, _ := os.ReadFile(filepath.Join(dest, "GAME/A.EXE")); string(b) != "new a" {
			t.Fatal("modified file not written")
		}
	})

	t.Run("dry run writes nothing", func(t *testing.T) {
		dest := t.TempDir()
		res, err := Extract(src, changes, "GAME", dest, false, false, true)
		if err != nil || len(res.Written) != 2 {
			t.Fatalf("%v, %v", res, err)
		}
		if entries, _ := os.ReadDir(dest); len(entries) != 0 {
			t.Fatal("dry run wrote files")
		}
	})
}

// zipBytes builds a ZIP from name -> content.
func zipBytes(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for n, d := range files {
		w, _ := zw.CreateHeader(&zip.FileHeader{Name: n, Modified: t0})
		w.Write([]byte(d))
	}
	zw.Close()
	return buf.Bytes()
}

func TestPureZip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.pure.zip")
	os.WriteFile(p, zipBytes(t, map[string]string{
		"GAME/GAME.EXE": "installed",
		"README.TXT":    "changed",
		"FILEMODS.DBP":  "DELETE|SETUP.TMP\r\nREDIRECTFILE|NEW.CFG|OLD.CFG\r\n",
	}), 0o644)
	pz, err := OpenPureZip(p)
	if err != nil {
		t.Fatal(err)
	}
	defer pz.Close()
	// DOS paths match case-insensitively: the content may spell them
	// differently from the save.
	content := fstest.MapFS{"Readme.txt": {Data: []byte("original")}}
	var lines []string
	for _, c := range pz.Changes(content) {
		lines = append(lines, c.Kind.String()+" "+c.Path)
	}
	if got, want := strings.Join(lines, ","), "added GAME/GAME.EXE,modified README.TXT,deleted SETUP.TMP"; got != want {
		t.Fatalf("changes %s, want %s", got, want)
	}
	if len(pz.Renamed) != 1 || pz.Renamed[0] != "NEW.CFG <- OLD.CFG" {
		t.Fatalf("renamed %v", pz.Renamed)
	}
}

func writeSource(t *testing.T, manifest string, files map[string]string) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "Example Game")
	for n, d := range files {
		p := filepath.Join(src, filepath.FromSlash(n))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(d), 0o644)
		os.Chtimes(p, t0, t0)
	}
	os.WriteFile(filepath.Join(src, "DOSZ.TOML"), []byte(manifest), 0o644)
	return src
}

func TestPrepare(t *testing.T) {
	src := writeSource(t, "format = 1\nname = \"Example Game\"\n\n[boot]\nos = \"dosbox_pure/Win98\"\n",
		map[string]string{"SETUP/SETUP.EXE": "MZ", "DOSBOX.BAT": "@echo off\r\n"})
	m, err := manifest.Load(src)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Prepare(src, m)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	zr, err := zip.OpenReader(s.Content)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	got := map[string]string{}
	for _, f := range zr.File {
		b, _ := fs.ReadFile(zr, f.Name)
		got[f.Name] = string(b)
		if f.Method != zip.Store {
			t.Errorf("%s not stored", f.Name)
		}
	}
	if got["AUTOBOOT.DBP"] != "O*dosbox_pure/Win98" || got["SETUP/SETUP.EXE"] != "MZ" || len(got) != 2 {
		t.Fatalf("content %v", got)
	}
	if _, ok := got["DOSBOX.BAT"]; ok || len(s.Skipped) != 1 {
		t.Fatalf("DOSBOX.BAT should be left out and reported: %v", s.Skipped)
	}
	if _, ok := got["DOSZ.TOML"]; ok {
		t.Fatal("manifest included")
	}
	if !strings.HasPrefix(filepath.Base(s.Content), "doszer-install-") {
		t.Fatalf("content %s", s.Content)
	}
}

func TestDiscover(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))

	// Nothing given or found: no system directory, doszer's own saves.
	e, err := Discover("", "")
	if err != nil {
		t.Fatal(err)
	}
	if e.SystemDir != "" || e.SaveDir != filepath.Join(home, "cache", "doszer", "saves") {
		t.Fatalf("dirs %q %q", e.SystemDir, e.SaveDir)
	}

	// RetroDECK's BIOS folder.
	bios := filepath.Join(home, "retrodeck", "bios")
	os.MkdirAll(bios, 0o755)
	cfg := filepath.Join(home, retroDECKConfig)
	os.MkdirAll(filepath.Dir(cfg), 0o755)
	os.WriteFile(cfg, []byte(`{"version": "0.10.10b", "paths": {"bios_path": "`+bios+`", "saves_path": "/elsewhere"}}`), 0o644)
	if e, err = Discover("", ""); err != nil || e.SystemDir != bios {
		t.Fatalf("system dir %q, err %v", e.SystemDir, err)
	}

	// A system/ folder in the working directory comes first.
	t.Chdir(home)
	os.MkdirAll(filepath.Join(home, "system"), 0o755)
	if e, err = Discover("", ""); err != nil || e.SystemDir != filepath.Join(home, "system") {
		t.Fatalf("system dir %q, err %v", e.SystemDir, err)
	}

	// Given directories win, and must exist.
	sys, saves := t.TempDir(), t.TempDir()
	if e, err = Discover(sys, saves); err != nil || e.SystemDir != sys || e.SaveDir != saves {
		t.Fatalf("dirs %q %q, err %v", e.SystemDir, e.SaveDir, err)
	}
	if _, err := Discover(filepath.Join(sys, "missing"), ""); err == nil {
		t.Fatal("missing system directory accepted")
	}
}

// fakeDOSBoxPure stands in for DOSBox Pure: launching a session copies the
// file in $FAKE_SAVE into the save directory under the session's content
// name, with the suffix in $FAKE_SUFFIX. It returns a system and save
// directory for Options.
func fakeDOSBoxPure(t *testing.T) (systemDir, saveDir string) {
	t.Helper()
	dir := t.TempDir()
	systemDir, saveDir = filepath.Join(dir, "system"), filepath.Join(dir, "saves")
	os.MkdirAll(saveDir, 0o755)
	os.MkdirAll(systemDir, 0o755)
	old := launch
	t.Cleanup(func() { launch = old })
	launch = func(e Env, s *Session) error {
		if s.CoreOptions["dosbox_pure_bootos_ramdisk"] != "diff" || s.CoreOptions["dosbox_pure_menu_time"] != "0" {
			t.Errorf("session core options %v", s.CoreOptions)
		}
		data, err := os.ReadFile(os.Getenv("FAKE_SAVE"))
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(e.SaveDir, s.Name+os.Getenv("FAKE_SUFFIX")), data, 0o644)
	}
	return systemDir, saveDir
}

func TestRunPlainSession(t *testing.T) {
	systemDir, saveDir := fakeDOSBoxPure(t)
	save := filepath.Join(t.TempDir(), "fixture.pure.zip")
	os.WriteFile(save, zipBytes(t, map[string]string{"GAME/GAME.EXE": "installed", "GAME/GAME.CFG": "cfg"}), 0o644)
	t.Setenv("FAKE_SAVE", save)
	t.Setenv("FAKE_SUFFIX", ".pure.zip")

	src := writeSource(t, "format = 1\nname = \"Example Game\"\n", map[string]string{"INSTALL/SETUP.EXE": "MZ"})
	rep, err := Run(Options{Source: src, SystemDir: systemDir, SaveDir: saveDir})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Dir != "GAME" || len(rep.Extracted.Written) != 2 {
		t.Fatalf("report %+v", rep)
	}
	if len(rep.Programs) != 1 || rep.Programs[0] != `C:\GAME\GAME.EXE` || rep.Exec != "" {
		t.Fatalf("programs %q, exec %q", rep.Programs, rep.Exec)
	}
	if b, _ := os.ReadFile(filepath.Join(src, "GAME/GAME.EXE")); string(b) != "installed" {
		t.Fatal("GAME.EXE not extracted")
	}
	if entries, _ := os.ReadDir(saveDir); len(entries) != 0 {
		t.Fatal("save file not cleaned up")
	}
	if matches, _ := filepath.Glob(filepath.Join(filepath.Dir(src), ".doszer-install-*")); len(matches) != 0 {
		t.Fatal("session content not cleaned up")
	}
}

func TestRunOSSession(t *testing.T) {
	systemDir, saveDir := fakeDOSBoxPure(t)
	writeOSImage(t, saveDir, "Win98.img", make([]byte, 64<<10))
	manifestText := "format = 1\nname = \"Example Game\"\n\n[boot]\nos = \"dosbox_pure/Win98\"\n"
	src := writeSource(t, manifestText, map[string]string{"GAME/GAME.INI": "level=1\r\n"})

	// The D: disk the session will build, to craft the guest's change: the
	// game rewrote GAME.INI (same size, new time) on D:.
	m, _ := manifest.Load(src)
	s, err := Prepare(src, m)
	if err != nil {
		t.Fatal(err)
	}
	disk, zr, err := vdisk.Open(s.Content, vdisk.DefaultFreeSpaceMB)
	if err != nil {
		t.Fatal(err)
	}
	sav := craftSave(t, disk, "GAME    INI", "level=1\r\n", "level=9\r\n")
	zr.Close()
	s.Close()

	savePath := filepath.Join(t.TempDir(), "d.sav")
	os.WriteFile(savePath, sav, 0o644)
	t.Setenv("FAKE_SAVE", savePath)
	t.Setenv("FAKE_SUFFIX", fmt.Sprintf("-%08X.sav", disk.Hash))

	rep, err := Run(Options{Source: src, SystemDir: systemDir, SaveDir: saveDir, Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Drive != "D:" || rep.Dir != "GAME" || len(rep.Changes) != 1 || rep.Changes[0].Kind != Modified {
		t.Fatalf("report %+v", rep)
	}
	if b, _ := os.ReadFile(filepath.Join(src, "GAME/GAME.INI")); string(b) != "level=9\r\n" {
		t.Fatalf("GAME.INI = %q", b)
	}
	if entries, _ := os.ReadDir(saveDir); len(entries) != 0 {
		t.Fatal("save file not cleaned up")
	}
}

// craftSave builds a D: .sav that changes a file's content (same length)
// and bumps its directory entry's time, as a guest write would.
func craftSave(t *testing.T, disk *vdisk.Disk, shortName, oldData, newData string) []byte {
	t.Helper()
	records := map[uint32][]byte{}
	buf := make([]byte, 512)
	for s := uint32(0); int64(s)*512 < disk.Size() && len(records) < 2; s++ {
		if _, err := disk.ReadAt(buf, int64(s)*512); err != nil {
			break
		}
		if i := bytes.Index(buf, []byte(shortName)); i >= 0 && i%32 == 0 {
			sec := append([]byte{}, buf...)
			binary.LittleEndian.PutUint16(sec[i+22:], binary.LittleEndian.Uint16(sec[i+22:])+1) // modification time
			records[s] = sec
		} else if bytes.HasPrefix(buf, []byte(oldData)) {
			sec := append([]byte{}, buf...)
			copy(sec, newData)
			records[s] = sec
		}
		if s > 1<<16 {
			break
		}
	}
	if len(records) != 2 {
		t.Fatalf("found %d of 2 sectors to change", len(records))
	}
	out := []byte("FFDD\x01")
	for s, d := range records {
		num := make([]byte, 4)
		binary.LittleEndian.PutUint32(num, s)
		out = append(append(out, num...), d...)
	}
	return out
}

// TestRunOSSessionDriveC: an OS image session that boots an install image
// (--os) instead of boot.os, and extracts from C: (--drive c), picking the
// game's folder over the changes Windows makes to itself.
func TestRunOSSessionDriveC(t *testing.T) {
	systemDir, saveDir := fakeDOSBoxPure(t)

	// A stand-in OS image: a FAT disk with Windows and the game's default
	// install folder on it.
	zb := zipBytes(t, map[string]string{"WINDOWS/WIN.INI": "[windows]\r\n", "SIERRA/GAME.CFG": "path=1\r\n"})
	zr, err := zip.NewReader(bytes.NewReader(zb), int64(len(zb)))
	if err != nil {
		t.Fatal(err)
	}
	disk, err := vdisk.Build(zr, "image.zip", 0)
	if err != nil {
		t.Fatal(err)
	}
	img := make([]byte, disk.Size())
	if _, err := disk.ReadAt(img, 0); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(systemDir, "dosbox_pure"), 0o755)
	os.WriteFile(filepath.Join(systemDir, "dosbox_pure", "Install.img"), img, 0o644)

	// The installer rewrote SIERRA/GAME.CFG; Windows touched WIN.INI.
	sav := craftSave(t, disk, "GAME    CFG", "path=1\r\n", "path=2\r\n")
	win := craftSave(t, disk, "WIN     INI", "[windows]\r\n", "[WINDOWS]\r\n")
	savePath := filepath.Join(t.TempDir(), "c.sav")
	os.WriteFile(savePath, append(sav, win[5:]...), 0o644)
	t.Setenv("FAKE_SAVE", savePath)
	t.Setenv("FAKE_SUFFIX", "-CDRIVE.sav")

	src := writeSource(t, "format = 1\nname = \"Example Game\"\n\n[boot]\nos = \"dosbox_pure/Win98\"\n", map[string]string{"SETUP.EXE": "MZ"})
	opts := Options{Source: src, SystemDir: systemDir, SaveDir: saveDir, Drive: "c", OS: "dosbox_pure/Install"}
	rep, err := Run(opts)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Drive != "C:" || rep.Dir != "SIERRA" || len(rep.Extracted.Written) != 1 {
		t.Fatalf("report %+v", rep)
	}
	if b, _ := os.ReadFile(filepath.Join(src, "SIERRA/GAME.CFG")); string(b) != "path=2\r\n" {
		t.Fatalf("GAME.CFG = %q", b)
	}

	// The same through the configured install image, and --drive rejected
	// outside the OS image model.
	opts.OS, opts.InstallOS, opts.Force = "", map[string]string{"dosbox_pure/Win98": "dosbox_pure/Install"}, true
	if _, err := Run(opts); err != nil {
		t.Fatalf("install_os: %v", err)
	}
	plain := writeSource(t, "format = 1\nname = \"Example Game\"\n", map[string]string{"SETUP.EXE": "MZ"})
	if _, err := Run(Options{Source: plain, SystemDir: systemDir, SaveDir: saveDir, Drive: "c"}); err == nil {
		t.Fatal("--drive c accepted for a plain session")
	}
}

// writeOSImage puts an OS image into fakeDOSBoxPure's system directory.
func writeOSImage(t *testing.T, saveDir, name string, data []byte) {
	t.Helper()
	dir := filepath.Join(filepath.Dir(saveDir), "system", "dosbox_pure")
	os.MkdirAll(dir, 0o755)
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// vhdFooter builds a VHD footer for a disk of size bytes.
func vhdFooter(diskType uint32, size, dataOffset uint64) []byte {
	b := make([]byte, 512)
	be := binary.BigEndian
	copy(b, "conectix")
	be.PutUint32(b[8:], 2)
	be.PutUint32(b[12:], 0x00010000)
	be.PutUint64(b[16:], dataOffset)
	be.PutUint64(b[40:], size)
	be.PutUint64(b[48:], size)
	be.PutUint32(b[60:], diskType)
	be.PutUint32(b[64:], vhd.Checksum(b, 64))
	return b
}

func TestCheckDiffSafe(t *testing.T) {
	const size = 4 << 20
	be := binary.BigEndian
	hdr := make([]byte, 1024)
	copy(hdr, "cxsparse")
	be.PutUint64(hdr[8:], ^uint64(0))
	be.PutUint64(hdr[16:], 1536)
	be.PutUint32(hdr[24:], 0x00010000)
	be.PutUint32(hdr[28:], 2)
	be.PutUint32(hdr[32:], 2<<20)
	be.PutUint32(hdr[36:], vhd.Checksum(hdr, 36))
	dynamic := append(append(append(vhdFooter(vhd.TypeDynamic, size, 512), hdr...), bytes.Repeat([]byte{0xFF}, 512)...), vhdFooter(vhd.TypeDynamic, size, 512)...)
	fixed := append(make([]byte, size), vhdFooter(vhd.TypeFixed, size, ^uint64(0))...)

	tests := []struct {
		name, file string
		data       []byte
		wantErr    bool
	}{
		{"raw image", "OS.img", make([]byte, size), false},
		{"fixed VHD", "OS.vhd", fixed, false},
		{"dynamic VHD", "OS.vhd", dynamic, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			system := t.TempDir()
			os.MkdirAll(filepath.Join(system, "dosbox_pure"), 0o755)
			os.WriteFile(filepath.Join(system, "dosbox_pure", tt.file), tt.data, 0o644)
			if err := checkDiffSafe(system, "dosbox_pure/OS"); (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// A session that changes nothing - setup run on a game that comes
// installed, settings saved unchanged - extracts nothing, without error,
// and still cleans up its save.
func TestRunSessionNoChanges(t *testing.T) {
	systemDir, saveDir := fakeDOSBoxPure(t)
	save := filepath.Join(t.TempDir(), "empty.pure.zip")
	os.WriteFile(save, zipBytes(t, map[string]string{}), 0o644)
	t.Setenv("FAKE_SAVE", save)
	t.Setenv("FAKE_SUFFIX", ".pure.zip")

	src := writeSource(t, "format = 1\nname = \"Example Game\"\n", map[string]string{"GAME.EXE": "MZ", "SETUP.EXE": "MZ"})
	rep, err := Run(Options{Source: src, SystemDir: systemDir, SaveDir: saveDir})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Changes) != 0 || rep.Extracted != nil || rep.Drive != "C:" {
		t.Fatalf("report %+v", rep)
	}
	if entries, _ := os.ReadDir(saveDir); len(entries) != 0 {
		t.Fatal("save file not cleaned up")
	}
}
