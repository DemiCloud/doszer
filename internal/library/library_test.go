package library

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeArchive(t *testing.T, p string, files map[string]string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for name, data := range files {
		fw, _ := w.Create(name)
		fw.Write([]byte(data))
	}
	w.Close()
	f.Close()
}

func TestScan(t *testing.T) {
	dir := t.TempDir()
	writeArchive(t, filepath.Join(dir, "dos", "zeta.dosz"), map[string]string{
		"DOSZ.TOML": "format = 1\nname = \"Alpha Game\"\n", "AUTOBOOT.DBP": "C:\\GAME.EXE", "DOSZ.ICO": "ICON",
	})
	writeArchive(t, filepath.Join(dir, "windows9x", "Beta.dosz"), map[string]string{
		"AUTOBOOT.DBP": "O*dosbox_pure/Windows 98\r\n\r\nBeta",
	})
	writeArchive(t, filepath.Join(dir, "Gamma.DOSZ"), map[string]string{"AUTOBOOT.DBP": "S*dosbox_pure/Windows 3.1"})
	os.WriteFile(filepath.Join(dir, "broken.dosz"), []byte("not a zip"), 0o644)
	os.WriteFile(filepath.Join(dir, "other.zip"), []byte("ignored"), 0o644)

	stats, err := LoadStats(filepath.Join(t.TempDir(), "s", "playtime.json"), []string{dir})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	if err := stats.Add(filepath.Join(dir, "windows9x", "Beta.dosz"), start, 90*time.Second); err != nil {
		t.Fatal(err)
	}
	stats.Add(filepath.Join(dir, "windows9x", "Beta.dosz"), start.Add(time.Hour), 30*time.Second)

	// Read back from the file, as a later session would.
	stats, err = LoadStats(stats.path, []string{dir})
	if err != nil {
		t.Fatal(err)
	}
	games, err := Scan([]string{dir}, stats)
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		name, rel string
		kind      Kind
		system    string
		icon      bool
		err       bool
		played    time.Duration
	}
	want := []row{
		{"Alpha Game", "dos/zeta.dosz", KindDOS, "", true, false, 0},
		{"Beta", "windows9x/Beta.dosz", KindOS, "Windows 98", false, false, 2 * time.Minute},
		{"broken", "broken.dosz", KindDOS, "", false, true, 0},
		{"Gamma", "Gamma.DOSZ", KindShell, "Windows 3.1", false, false, 0},
	}
	if len(games) != len(want) {
		t.Fatalf("got %d games: %+v", len(games), games)
	}
	for i, w := range want {
		g := games[i]
		got := row{g.Name, g.Rel, g.Kind, g.System, g.Icon != nil, g.Err != "", g.PlayTime}
		if got != w {
			t.Errorf("game %d = %+v, want %+v", i, got, w)
		}
	}
	if !games[1].LastPlayed.Equal(start.Add(time.Hour)) {
		t.Errorf("LastPlayed = %v", games[1].LastPlayed)
	}
	if k := stats.key(filepath.Join(dir, "windows9x", "Beta.dosz")); k != "windows9x/Beta.dosz" {
		t.Errorf("key = %q, want relative to the ROM directory", k)
	}
}

func TestScanSeveralFolders(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	writeArchive(t, filepath.Join(a, "dos", "One.dosz"), map[string]string{"AUTOBOOT.DBP": "C:\\ONE.EXE"})
	writeArchive(t, filepath.Join(b, "Two.dosz"), map[string]string{"AUTOBOOT.DBP": "C:\\TWO.EXE"})
	stats, _ := LoadStats("", []string{a, b})
	stats.Add(filepath.Join(b, "Two.dosz"), time.Now(), time.Minute)
	if k := stats.key(filepath.Join(b, "Two.dosz")); k != "Two.dosz" {
		t.Errorf("key = %q, want relative to its own folder", k)
	}
	games, err := Scan([]string{a, b, filepath.Join(a, "missing")}, stats)
	if err == nil {
		t.Error("a missing folder wasn't reported")
	}
	if len(games) != 2 || games[0].Root != a || games[0].Rel != "dos/One.dosz" || games[1].Root != b || games[1].PlayTime != time.Minute {
		t.Errorf("games = %+v", games)
	}
}

// A game is verified when every disc it declares has a Redump match; a
// game with no disc has nothing to verify.
func TestVerified(t *testing.T) {
	dir := t.TempDir()
	disc := func(id, redump string) string {
		s := "\n[[source.media]]\nid = '" + id + "'\n"
		if redump != "" {
			s += "redump_name = '" + redump + "'\n"
		}
		return s + "tracks = [{ type = 'data', sha1 = '" + strings.Repeat("a", 40) + "' }]\n"
	}
	head := "format = 1\nname = 'Game'\n\n[boot]\nexec = 'C:\\GAME.EXE'\n"
	tests := []struct {
		name, manifest string
		want           bool
	}{
		{"none", head, false},
		{"matched", head + disc("disc1", "Game (USA) (Disc 1)") + disc("disc2", "Game (USA) (Disc 2)"), true},
		{"one unmatched", head + disc("disc1", "Game (USA) (Disc 1)") + disc("disc2", ""), false},
	}
	for _, tt := range tests {
		writeArchive(t, filepath.Join(dir, tt.name+".dosz"), map[string]string{"DOSZ.TOML": tt.manifest})
	}
	games, err := Scan([]string{dir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, g := range games {
		if g.Err != "" {
			t.Fatalf("%s: %s", g.Rel, g.Err)
		}
		got[strings.TrimSuffix(g.Rel, ".dosz")] = g.Verified
	}
	for _, tt := range tests {
		if got[tt.name] != tt.want {
			t.Errorf("%s: verified %v, want %v", tt.name, got[tt.name], tt.want)
		}
	}
}
