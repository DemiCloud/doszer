package build_test

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/DemiCloud/doszer/internal/build"
	"github.com/DemiCloud/doszer/internal/manifest"
)

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readZip(t *testing.T, path string) map[string]*zip.File {
	t.Helper()
	r, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	t.Cleanup(func() { r.Close() })

	entries := map[string]*zip.File{}
	for _, f := range r.File {
		entries[f.Name] = f
	}
	return entries
}

func readZipFile(t *testing.T, f *zip.File) string {
	t.Helper()
	rc, err := f.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func buildFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	writeFile(t, dir, "DOSZ.TOML", `
format = 1
name   = "Test Game"

[boot]
exec = 'C:\GAME.EXE'

[layout]
trim    = ["**/*.HLP"]
flatten = true

[[layout.rewrite]]
from  = "OLDTOKEN"
to    = "NEWTOKEN"
files = ["**/*.BAT"]

[pack]
store = ["iso"]
`)
	writeFile(t, dir, "WRAPPER/DATA/GAME.EXE", "binary")
	writeFile(t, dir, "WRAPPER/DATA/AUTOEXEC.BAT", "OLDTOKEN\nSECOND LINE\n")
	writeFile(t, dir, "WRAPPER/DATA/README.HLP", "help text")
	writeFile(t, dir, "WRAPPER/DATA/desktop.ini", "junk")
	writeFile(t, dir, "WRAPPER/DATA/MUSIC.OGG", "fake ogg data")
	writeFile(t, dir, "WRAPPER/DATA/DATA.ISO", "fake iso data")
	writeFile(t, dir, "WRAPPER/DATA/OTHER.BIN", "fake bin data")

	return dir
}

func TestRunBuildsDeterministicArchive(t *testing.T) {
	dir := buildFixture(t)
	out := filepath.Join(t.TempDir(), "out.dosz")

	res, err := build.Run(build.Options{SourceDir: dir, OutputPath: out})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	want := []string{"AUTOBOOT.DBP", "DATA/AUTOEXEC.BAT", "DATA/DATA.ISO", "DATA/GAME.EXE", "DATA/MUSIC.OGG", "DATA/OTHER.BIN", "DOSZ.TOML"}
	sort.Strings(want)
	if got := res.Entries; !equalSlices(got, want) {
		t.Fatalf("Entries = %v, want %v", got, want)
	}

	entries := readZip(t, out)

	// Flatten: WRAPPER was a pass-through directory and must be gone;
	// trim: README.HLP must be gone; exclude: desktop.ini must be gone.
	for _, bad := range []string{"WRAPPER/DATA/GAME.EXE", "DATA/README.HLP", "DATA/desktop.ini"} {
		if _, ok := entries[bad]; ok {
			t.Errorf("archive contains %q, should not", bad)
		}
	}

	// Rewrite + text normalization.
	if got := readZipFile(t, entries["DATA/AUTOEXEC.BAT"]); got != "NEWTOKEN\r\nSECOND LINE\r\n" {
		t.Errorf("AUTOEXEC.BAT = %q", got)
	}

	// AUTOBOOT.DBP from boot.exec.
	if got := readZipFile(t, entries["AUTOBOOT.DBP"]); got != `C:\GAME.EXE` {
		t.Errorf("AUTOBOOT.DBP = %q", got)
	}

	// §8.1: .ogg always stored; iso stored via pack.store; bin not listed, deflated.
	if entries["DATA/MUSIC.OGG"].Method != zip.Store {
		t.Error("MUSIC.OGG should be stored")
	}
	if entries["DATA/DATA.ISO"].Method != zip.Store {
		t.Error("DATA.ISO should be stored")
	}
	if entries["DATA/OTHER.BIN"].Method != zip.Deflate {
		t.Error("OTHER.BIN should be deflated")
	}

	// §8.2: fixed timestamp, zero external attributes, no extra fields.
	for name, f := range entries {
		if f.Modified.UTC().Format("2006-01-02T15:04:05") != "1980-01-01T00:00:00" {
			t.Errorf("%s: Modified = %v, want 1980-01-01 00:00:00", name, f.Modified)
		}
		if f.ExternalAttrs != 0 {
			t.Errorf("%s: ExternalAttrs = %d, want 0", name, f.ExternalAttrs)
		}
		if len(f.Extra) != 0 {
			t.Errorf("%s: Extra = %v, want none", name, f.Extra)
		}
	}

	// §8.2: entries ordered by path.
	r, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var ordered []string
	for _, f := range r.File {
		ordered = append(ordered, f.Name)
	}
	if !sort.StringsAreSorted(ordered) {
		t.Errorf("archive entries not sorted: %v", ordered)
	}
}

func TestRunEmbedsEffectiveName(t *testing.T) {
	dir := buildFixture(t)
	out := filepath.Join(t.TempDir(), "out.dosz")

	_, err := build.Run(build.Options{SourceDir: dir, OutputPath: out, Name: "Override Name"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	entries := readZip(t, out)
	data := readZipFile(t, entries["DOSZ.TOML"])

	m, err := manifest.Parse([]byte(data), "fallback")
	if err != nil {
		t.Fatalf("embedded manifest does not parse: %v\n%s", err, data)
	}
	if m.Name != "Override Name" {
		t.Errorf("embedded name = %q, want %q", m.Name, "Override Name")
	}
}

func TestRunDefaultOutputPath(t *testing.T) {
	dir := buildFixture(t)

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(wd) })

	res, err := build.Run(build.Options{SourceDir: dir})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.OutputPath != "Test Game.dosz" {
		t.Errorf("OutputPath = %q, want %q", res.OutputPath, "Test Game.dosz")
	}
	if _, err := os.Stat(filepath.Join(tmp, "Test Game.dosz")); err != nil {
		t.Errorf("archive not written at default path: %v", err)
	}
}

func TestRunDryRunWritesNothing(t *testing.T) {
	dir := buildFixture(t)
	out := filepath.Join(t.TempDir(), "out.dosz")

	res, err := build.Run(build.Options{SourceDir: dir, OutputPath: out, DryRun: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("dry run wrote %s", out)
	}
	if len(res.Trimmed) != 1 || res.Trimmed[0] != "WRAPPER/DATA/README.HLP" {
		t.Errorf("Trimmed = %v", res.Trimmed)
	}
	if len(res.Moved) == 0 {
		t.Error("expected flatten to report moved paths")
	}
	if len(res.RewriteHits) != 1 {
		t.Errorf("RewriteHits = %v", res.RewriteHits)
	}
}

func TestRunRejectsExtract(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "DOSZ.TOML", `
format = 1
name   = "Needs Media"

[[source.media]]
id = "disc1"
tracks = [{ type = "data", sha1 = "0123456789abcdef0123456789abcdef01234567" }]

[[extract]]
media = "disc1"
from  = "/GAME"
to    = "GAME"
`)

	_, err := build.Run(build.Options{SourceDir: dir, OutputPath: filepath.Join(t.TempDir(), "out.dosz")})
	if err == nil || !strings.Contains(err.Error(), "extract") {
		t.Fatalf("Run error = %v, want an error mentioning extract", err)
	}
}

func TestRunStrictFailsOnUnmatchedTrim(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "DOSZ.TOML", `
format = 1
name   = "No Match"

[layout]
trim = ["**/*.NOPE"]
`)
	writeFile(t, dir, "FILE.TXT", "content")

	_, err := build.Run(build.Options{SourceDir: dir, OutputPath: filepath.Join(t.TempDir(), "out.dosz"), Strict: true})
	if err == nil {
		t.Fatal("expected strict mode to fail on an unmatched trim pattern")
	}

	// Without --strict, the same manifest builds successfully.
	res, err := build.Run(build.Options{SourceDir: dir, OutputPath: filepath.Join(t.TempDir(), "out.dosz")})
	if err != nil {
		t.Fatalf("non-strict Run: %v", err)
	}
	if len(res.TrimWarnings) != 1 {
		t.Errorf("TrimWarnings = %v", res.TrimWarnings)
	}
}

func TestRunRejectsShellOwnedFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "DOSZ.TOML", `
format = 1
name   = "Shell Game"

[boot]
shell = "dosbox_pure/Win31"
`)
	writeFile(t, dir, "WINDOWS/WIN.INI", "junk")

	_, err := build.Run(build.Options{SourceDir: dir, OutputPath: filepath.Join(t.TempDir(), "out.dosz")})
	if err == nil {
		t.Fatal("expected an error for a shell-owned file under the shell model")
	}
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	sort.Strings(a)
	sort.Strings(b)
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
