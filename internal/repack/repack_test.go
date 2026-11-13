package repack_test

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DemiCloud/doszer/internal/build"
	"github.com/DemiCloud/doszer/internal/manifest"
	"github.com/DemiCloud/doszer/internal/repack"
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

// buildArchive builds a real archive via build.Run, for repack to work on.
func buildArchive(t *testing.T, manifestBody string, extraFiles map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "DOSZ.TOML", manifestBody)
	for name, content := range extraFiles {
		writeFile(t, dir, name, content)
	}

	out := filepath.Join(t.TempDir(), "in.dosz")
	if _, err := build.Run(build.Options{SourceDir: dir, OutputPath: out}); err != nil {
		t.Fatalf("build.Run: %v", err)
	}
	return out
}

const plainManifest = `
format = 1
name   = "Test Game"

[boot]
exec = 'C:\GAME.EXE'
`

func TestRepackReproducesContent(t *testing.T) {
	in := buildArchive(t, plainManifest, map[string]string{"GAME.EXE": "binary content"})
	out := filepath.Join(t.TempDir(), "out.dosz")

	res, err := repack.Run(repack.Options{InputPath: in, OutputPath: out})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	inEntries := readZip(t, in)
	outEntries := readZip(t, out)
	if len(inEntries) != len(outEntries) {
		t.Fatalf("entry count = %d, want %d (%v)", len(outEntries), len(inEntries), res.Entries)
	}
	if got := readZipFile(t, outEntries["GAME.EXE"]); got != "binary content" {
		t.Errorf("GAME.EXE = %q", got)
	}
}

func TestRepackDefaultOutputPath(t *testing.T) {
	in := buildArchive(t, plainManifest, map[string]string{"GAME.EXE": "x"})

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(wd) })

	res, err := repack.Run(repack.Options{InputPath: in})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.OutputPath != "Test Game.dosz" {
		t.Errorf("OutputPath = %q, want %q", res.OutputPath, "Test Game.dosz")
	}
}

func TestRepackIsDeterministic(t *testing.T) {
	in := buildArchive(t, plainManifest, map[string]string{"GAME.EXE": "x"})
	out := filepath.Join(t.TempDir(), "out.dosz")

	if _, err := repack.Run(repack.Options{InputPath: in, OutputPath: out}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	entries := readZip(t, out)
	for name, f := range entries {
		if got := f.Modified.UTC().Format("2006-01-02T15:04:05"); got != "1980-01-01T00:00:00" {
			t.Errorf("%s: Modified = %v", name, f.Modified)
		}
		if f.ExternalAttrs != 0 {
			t.Errorf("%s: ExternalAttrs = %d, want 0", name, f.ExternalAttrs)
		}
		if len(f.Extra) != 0 {
			t.Errorf("%s: Extra = %v, want none", name, f.Extra)
		}
	}

	r, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var names []string
	for _, f := range r.File {
		names = append(names, f.Name)
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] >= names[i] {
			t.Errorf("entries not sorted: %v", names)
			break
		}
	}
}

func TestRepackRegeneratesManifestWithCurrentName(t *testing.T) {
	in := buildArchive(t, plainManifest, map[string]string{"GAME.EXE": "x"})
	out := filepath.Join(t.TempDir(), "out.dosz")

	if _, err := repack.Run(repack.Options{InputPath: in, OutputPath: out}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	entries := readZip(t, out)
	data := readZipFile(t, entries["DOSZ.TOML"])
	m, err := manifest.Parse([]byte(data), "fallback")
	if err != nil {
		t.Fatalf("re-embedded manifest does not parse: %v\n%s", err, data)
	}
	if m.Name != "Test Game" {
		t.Errorf("Name = %q, want %q", m.Name, "Test Game")
	}
}

func TestRepackRefreshesStaleAutoboot(t *testing.T) {
	in := buildArchive(t, plainManifest, map[string]string{"GAME.EXE": "x"})

	// Hand-corrupt the archive's AUTOBOOT.DBP, as if it had drifted from
	// the manifest (e.g. hand-edited).
	r, err := zip.OpenReader(in)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, f := range r.File {
		fh := f.FileHeader
		fw, err := w.CreateHeader(&fh)
		if err != nil {
			t.Fatal(err)
		}
		if f.Name == "AUTOBOOT.DBP" {
			if _, err := fw.Write([]byte("STALE GARBAGE")); err != nil {
				t.Fatal(err)
			}
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(fw, rc); err != nil {
			t.Fatal(err)
		}
		rc.Close()
	}
	r.Close()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(in, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(t.TempDir(), "out.dosz")
	if _, err := repack.Run(repack.Options{InputPath: in, OutputPath: out}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	entries := readZip(t, out)
	if got, want := readZipFile(t, entries["AUTOBOOT.DBP"]), `C:\GAME.EXE`; got != want {
		t.Errorf("AUTOBOOT.DBP = %q, want %q (regenerated from the manifest)", got, want)
	}
}

func TestRepackRejectsMissingManifest(t *testing.T) {
	in := buildArchive(t, plainManifest, map[string]string{"GAME.EXE": "x"})

	r, err := zip.OpenReader(in)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, f := range r.File {
		if f.Name == "DOSZ.TOML" {
			continue
		}
		fw, err := w.CreateHeader(&f.FileHeader)
		if err != nil {
			t.Fatal(err)
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(fw, rc); err != nil {
			t.Fatal(err)
		}
		rc.Close()
	}
	r.Close()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(in, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = repack.Run(repack.Options{InputPath: in, OutputPath: filepath.Join(t.TempDir(), "out.dosz")})
	if err == nil {
		t.Fatal("Run should fail when the archive has no manifest")
	}
}

func TestRepackRejectsForbiddenShellFile(t *testing.T) {
	in := buildArchive(t, `
format = 1
name   = "Shell Game"

[boot]
shell = "dosbox_pure/Win31"
`, nil)

	r, err := zip.OpenReader(in)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, f := range r.File {
		if err := func() error {
			fw, err := w.CreateHeader(&f.FileHeader)
			if err != nil {
				return err
			}
			rc, err := f.Open()
			if err != nil {
				return err
			}
			defer rc.Close()
			_, err = io.Copy(fw, rc)
			return err
		}(); err != nil {
			t.Fatal(err)
		}
	}
	fw, err := w.Create("WINDOWS/WIN.INI")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write([]byte("junk")); err != nil {
		t.Fatal(err)
	}
	r.Close()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(in, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = repack.Run(repack.Options{InputPath: in, OutputPath: filepath.Join(t.TempDir(), "out.dosz")})
	if err == nil {
		t.Fatal("Run should reject a shell-owned file under the shell model")
	}
}

// buildLegacyArchive builds an archive with a plain zip.Writer, never going
// through doszer build - simulating a pre-existing game archive someone
// wants to retrofit a manifest onto, or any archive from a different
// implementation.
func buildLegacyArchive(t *testing.T, manifestBody string, files map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "legacy.dosz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	write := func(name, content string) {
		fw, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	write(manifest.FileName, manifestBody)
	for name, content := range files {
		write(name, content)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestRepackAppliesLayoutToALegacyArchive is the regression case a real
// retrofit run found: repack must actually run trim/rewrite/text
// normalization against the archive's own content when a manifest with
// layout rules is inserted into a legacy (non-doszer-built) archive, not
// just regenerate the runtime files.
func TestRepackAppliesLayoutToALegacyArchive(t *testing.T) {
	in := buildLegacyArchive(t, `
format = 1
name   = "Legacy Game"

[boot]
exec = 'C:\GAME.EXE'

[layout]
trim = ["**/*.SKC"]

[[layout.rewrite]]
from  = 'C:\OLDPATH'
to    = 'C:\NEWPATH'
files = ["**/*.BAT"]
`, map[string]string{
		"GAME.EXE":    "binary",
		"INSTALL.SKC": "should be trimmed",
		"dosbox.bat":  "@ECHO OFF\r\nC:\\OLDPATH\\GAME.EXE\r\nexit\n",
	})

	out := filepath.Join(t.TempDir(), "out.dosz")
	res, err := repack.Run(repack.Options{InputPath: in, OutputPath: out})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, name := range res.Entries {
		if strings.EqualFold(filepath.Ext(name), ".skc") {
			t.Errorf("trim should have removed %s", name)
		}
	}

	entries := readZip(t, out)
	if _, ok := entries["INSTALL.SKC"]; ok {
		t.Error("INSTALL.SKC present in output, trim did not run")
	}

	got := readZipFile(t, entries["dosbox.bat"])
	want := "@ECHO OFF\r\nC:\\NEWPATH\\GAME.EXE\r\nexit\r\n"
	if got != want {
		t.Errorf("dosbox.bat = %q, want %q (rewrite + CRLF normalization applied)", got, want)
	}
}
