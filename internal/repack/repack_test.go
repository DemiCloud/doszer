package repack_test

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/DemiCloud/doszer/internal/build"
	"github.com/DemiCloud/doszer/internal/manifest"
	"github.com/DemiCloud/doszer/internal/repack"
	"github.com/DemiCloud/doszer/internal/verify"
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

func TestRepackPreservesEntryTimestamp(t *testing.T) {
	in := buildArchive(t, plainManifest, map[string]string{"GAME.EXE": "x"})

	// Rewrite GAME.EXE's timestamp via the legacy DOS fields directly (not
	// FileHeader.Modified, to avoid archive/zip adding its own extra
	// timestamp field on this rewrite), as if this were a legacy archive
	// with a real date.
	sourceTime := time.Date(1994, 6, 15, 10, 30, 0, 0, time.UTC)
	dosDate := uint16(sourceTime.Day() + int(sourceTime.Month())<<5 + (sourceTime.Year()-1980)<<9)
	dosTimeField := uint16(sourceTime.Second()/2 + sourceTime.Minute()<<5 + sourceTime.Hour()<<11)

	r, err := zip.OpenReader(in)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, f := range r.File {
		fh := f.FileHeader
		// Zero Modified before CreateHeader: archive/zip derives
		// ModifiedDate/ModifiedTime from it (overwriting any override
		// below) and adds its own extra timestamp field whenever it's
		// non-zero, which §8.2 forbids.
		fh.Modified = time.Time{}
		if f.Name == "GAME.EXE" {
			fh.ModifiedDate, fh.ModifiedTime = dosDate, dosTimeField
		}
		fw, err := w.CreateHeader(&fh)
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

	out := filepath.Join(t.TempDir(), "out.dosz")
	if _, err := repack.Run(repack.Options{InputPath: in, OutputPath: out}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	entries := readZip(t, out)
	got := entries["GAME.EXE"].Modified.UTC()
	if !got.Equal(sourceTime) {
		t.Errorf("GAME.EXE Modified = %v, want the preserved source time %v (§8.2)", got, sourceTime)
	}
}

func TestRepackIsDeterministic(t *testing.T) {
	in := buildArchive(t, plainManifest, map[string]string{"GAME.EXE": "x"})
	out := filepath.Join(t.TempDir(), "out.dosz")

	if _, err := repack.Run(repack.Options{InputPath: in, OutputPath: out}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	entries := readZip(t, out)
	generated := map[string]bool{"AUTOBOOT.DBP": true, "DOSZ.TOML": true}
	for name, f := range entries {
		wantEpoch := generated[name]
		gotEpoch := f.Modified.UTC().Format("2006-01-02T15:04:05") == "1980-01-01T00:00:00"
		if gotEpoch != wantEpoch {
			t.Errorf("%s: Modified = %v, epoch = %v, want epoch = %v", name, f.Modified, gotEpoch, wantEpoch)
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
		fh := f.FileHeader
		fw, err := w.CreateHeader(&fh)
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
			fh := f.FileHeader
			fw, err := w.CreateHeader(&fh)
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
// TestRepackKeepsDOSDateOverExtendedTimestamp: an archive made on Windows
// (or by Info-ZIP) records each entry's time twice, as local MS-DOS
// date/time fields and as a UTC extended timestamp field. DOSBox Pure reads
// only the DOS fields, so repack must keep those, not shift every date by
// the time zone the archive was made in.
func TestRepackKeepsDOSDateOverExtendedTimestamp(t *testing.T) {
	in := buildArchive(t, plainManifest, map[string]string{"GAME.EXE": "x"})
	madeAt := time.Date(1994, 6, 15, 10, 30, 0, 0, time.FixedZone("PST", -8*3600))

	r, err := zip.OpenReader(in)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, f := range r.File {
		fh := f.FileHeader
		fh.Modified = time.Time{}
		if f.Name == "GAME.EXE" {
			fh.Modified = madeAt // archive/zip writes local DOS fields and a UTC extra field
		}
		fw, err := w.CreateHeader(&fh)
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
	if f := readZip(t, in)["GAME.EXE"]; f.Modified.UTC().Hour() != 18 {
		t.Fatalf("fixture: Modified = %v, want the extra field's 18:30 UTC", f.Modified.UTC())
	}

	out := filepath.Join(t.TempDir(), "out.dosz")
	if _, err := repack.Run(repack.Options{InputPath: in, OutputPath: out}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	f := readZip(t, out)["GAME.EXE"]
	want := uint16(10<<11 | 30<<5)
	if f.ModifiedTime != want { //nolint:staticcheck // the DOS field is the point
		t.Errorf("GAME.EXE DOS time = %#x, want the input's DOS time %#x (10:30)", f.ModifiedTime, want) //nolint:staticcheck
	}
}

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

// stripManifest rewrites path without its DOSZ.TOML entry, as if it were a
// legacy archive with no embedded manifest at all.
func stripManifest(t *testing.T, path string) {
	t.Helper()
	r, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, f := range r.File {
		if strings.EqualFold(f.Name, manifest.FileName) {
			continue
		}
		fh := f.FileHeader
		fw, err := w.CreateHeader(&fh)
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
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRunManifestPathRetrofitsMissingManifest(t *testing.T) {
	in := buildArchive(t, plainManifest, map[string]string{"GAME.EXE": "x"})
	stripManifest(t, in)

	manifestPath := filepath.Join(t.TempDir(), "external.toml")
	if err := os.WriteFile(manifestPath, []byte(plainManifest), 0o644); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(t.TempDir(), "out.dosz")
	if _, err := repack.Run(repack.Options{InputPath: in, OutputPath: out, ManifestPath: manifestPath}); err != nil {
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

func TestRunWithoutManifestPathOrEmbeddedManifestFails(t *testing.T) {
	in := buildArchive(t, plainManifest, map[string]string{"GAME.EXE": "x"})
	stripManifest(t, in)

	out := filepath.Join(t.TempDir(), "out.dosz")
	if _, err := repack.Run(repack.Options{InputPath: in, OutputPath: out}); err == nil {
		t.Fatal("Run: expected an error with no manifest available")
	}
}

func TestRunManifestPathOverridesEmbeddedManifest(t *testing.T) {
	in := buildArchive(t, plainManifest, map[string]string{"GAME.EXE": "x"})

	manifestPath := filepath.Join(t.TempDir(), "external.toml")
	overrideBody := strings.Replace(plainManifest, "Test Game", "Overridden Game", 1)
	if err := os.WriteFile(manifestPath, []byte(overrideBody), 0o644); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(t.TempDir(), "out.dosz")
	if _, err := repack.Run(repack.Options{InputPath: in, OutputPath: out, ManifestPath: manifestPath}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	entries := readZip(t, out)
	data := readZipFile(t, entries["DOSZ.TOML"])
	m, err := manifest.Parse([]byte(data), "fallback")
	if err != nil {
		t.Fatalf("re-embedded manifest does not parse: %v\n%s", err, data)
	}
	if m.Name != "Overridden Game" {
		t.Errorf("Name = %q, want %q (ManifestPath should override the embedded manifest)", m.Name, "Overridden Game")
	}
}

// openRawBytes reads f's content without decompressing it.
func openRawBytes(t *testing.T, f *zip.File) []byte {
	t.Helper()
	r, err := f.OpenRaw()
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(io.LimitReader(r, int64(f.CompressedSize64)))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestRepackPassesThroughUnchangedCompressedBytes(t *testing.T) {
	// Large and repetitive enough that archive/zip's Deflate compressor
	// actually produces a multi-block compressed stream, so a corrupted
	// pass-through (e.g. wrong size or CRC) wouldn't go unnoticed.
	content := strings.Repeat("The quick brown fox jumps over the lazy dog. ", 4096)
	in := buildArchive(t, plainManifest, map[string]string{"GAME.EXE": content})

	inEntries := readZip(t, in)
	inGame := inEntries["GAME.EXE"]
	if inGame.Method != zip.Deflate {
		t.Fatalf("GAME.EXE Method = %d, want Deflate - test assumes the default pack.store", inGame.Method)
	}
	wantRaw := openRawBytes(t, inGame)

	out := filepath.Join(t.TempDir(), "out.dosz")
	if _, err := repack.Run(repack.Options{InputPath: in, OutputPath: out}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	outEntries := readZip(t, out)
	outGame := outEntries["GAME.EXE"]
	if outGame.CRC32 != inGame.CRC32 {
		t.Fatalf("GAME.EXE CRC32 = %#x, want %#x", outGame.CRC32, inGame.CRC32)
	}
	gotRaw := openRawBytes(t, outGame)
	if !bytes.Equal(gotRaw, wantRaw) {
		t.Errorf("GAME.EXE's compressed bytes changed across an untouched repack (%d bytes -> %d bytes); "+
			"an entry nothing in the pipeline reads or changes should be copied through unchanged, not recompressed", len(wantRaw), len(gotRaw))
	}

	// The content itself must still decompress correctly.
	got := readZipFile(t, outGame)
	if got != content {
		t.Error("GAME.EXE content changed across an untouched repack")
	}
}

func TestRepackDropsDirectoryEntries(t *testing.T) {
	in := buildLegacyArchive(t, plainManifestForDirs, map[string]string{
		"GAME/":         "",
		"GAME/GAME.EXE": "binary",
	})
	out := filepath.Join(t.TempDir(), "out.dosz")
	if _, err := repack.Run(repack.Options{InputPath: in, OutputPath: out}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	entries := readZip(t, out)
	if _, ok := entries["GAME/"]; ok {
		t.Error("directory entry GAME/ carried into the output")
	}
	if _, ok := entries["GAME/GAME.EXE"]; !ok {
		t.Error("GAME/GAME.EXE missing")
	}
	if err := verify.Verify(out); err != nil {
		t.Errorf("Verify: %v", err)
	}
}

const plainManifestForDirs = `
format = 1
name   = "Legacy Game"

[boot]
exec = 'C:\GAME\GAME.EXE'
`

// TestRepackOfABuildIsIdentical: an entry's bytes don't depend on whether
// build compressed it or repack copied it through, so repacking a fresh
// build reproduces it exactly.
func TestRepackOfABuildIsIdentical(t *testing.T) {
	in := buildArchive(t, plainManifest+"\n[pack]\nstore = [\"dat\"]\n", map[string]string{
		"GAME.EXE":   strings.Repeat("deflated ", 1000),
		"DATA.DAT":   strings.Repeat("stored ", 1000),
		"README.TXT": "text\n",
	})
	out := filepath.Join(t.TempDir(), "out.dosz")
	if _, err := repack.Run(repack.Options{InputPath: in, OutputPath: out}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	a, _ := os.ReadFile(in)
	b, _ := os.ReadFile(out)
	if !bytes.Equal(a, b) {
		t.Error("repacking a fresh build changed its bytes")
	}
}

// TestRepackOfAnInputPadBuildIsIdentical: DOS.YML is merged, not replaced
// (§6.8.3), so regenerating it from the archive's own copy must reproduce
// it - including a source DOS.YML's own lines.
func TestRepackOfAnInputPadBuildIsIdentical(t *testing.T) {
	in := buildArchive(t, plainManifest+"\n[[input.pad]]\nb = \"space Fire\"\nl3 = \"menu\"\n", map[string]string{
		"GAME.EXE": "binary",
		"DOS.YML":  "cpu_cycles: 20000\ninput_pad_a: enter\n",
	})
	out := filepath.Join(t.TempDir(), "out.dosz")
	if _, err := repack.Run(repack.Options{InputPath: in, OutputPath: out}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	a, _ := os.ReadFile(in)
	b, _ := os.ReadFile(out)
	if !bytes.Equal(a, b) {
		t.Error("repacking a fresh build changed its bytes")
	}
}

// TestRepackInPlace: writing over the input must not destroy it while it
// is still being read (the archive is renamed into place once complete).
func TestRepackInPlace(t *testing.T) {
	in := buildArchive(t, plainManifest, map[string]string{
		"GAME.EXE": strings.Repeat("binary content ", 10000),
		"DATA.BIN": strings.Repeat("more data ", 10000),
	})
	before, _ := os.ReadFile(in)
	if _, err := repack.Run(repack.Options{InputPath: in, OutputPath: in}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := verify.Verify(in); err != nil {
		t.Fatalf("Verify after an in-place repack: %v", err)
	}
	after, _ := os.ReadFile(in)
	if !bytes.Equal(before, after) {
		t.Error("an in-place repack of a fresh build changed it")
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(in), ".doszer-*")); len(left) > 0 {
		t.Errorf("temporary files left behind: %v", left)
	}
}

// legacyArchive writes a plain ZIP (not built by doszer) with the given
// entries, in order.
func legacyArchive(t *testing.T, entries [][2]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "legacy.zip")
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		w, err := zw.Create(e[0])
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(e[1]))
	}
	zw.Close()
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRepackCaseDuplicates(t *testing.T) {
	manifestPath := filepath.Join(t.TempDir(), "DOSZ.TOML")
	writeFile(t, filepath.Dir(manifestPath), "DOSZ.TOML", "format = 1\nname = \"Case Game\"\n\n[[file]]\npath = \"SETUP.CFG\"\ntext = \"from the manifest\"\n")

	// Both orders of the archive's entries must give the same result: one
	// README kept with a warning, and the [[file]] entry replacing the
	// archive's differently-cased setup.cfg.
	for _, order := range [][][2]string{
		{{"readme.txt", "lower"}, {"README.TXT", "upper"}, {"setup.cfg", "archive"}},
		{{"setup.cfg", "archive"}, {"README.TXT", "upper"}, {"readme.txt", "lower"}},
	} {
		out := filepath.Join(t.TempDir(), "out.dosz")
		res, err := repack.Run(repack.Options{InputPath: legacyArchive(t, order), OutputPath: out, ManifestPath: manifestPath})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.PathWarnings) != 1 || !strings.Contains(res.PathWarnings[0], "keeping readme.txt") {
			t.Errorf("PathWarnings = %q, want one keeping readme.txt", res.PathWarnings)
		}
		entries := readZip(t, out)
		if f, ok := entries["readme.txt"]; !ok || readZipFile(t, f) != "lower" {
			t.Errorf("readme.txt missing or wrong; entries %v", keys(entries))
		}
		if _, ok := entries["README.TXT"]; ok {
			t.Error("README.TXT kept alongside readme.txt")
		}
		if f, ok := entries["SETUP.CFG"]; !ok || readZipFile(t, f) != "from the manifest" {
			t.Errorf("SETUP.CFG missing or not the [[file]] entry; entries %v", keys(entries))
		}
		if _, ok := entries["setup.cfg"]; ok {
			t.Error("the archive's setup.cfg kept alongside the [[file]] entry")
		}
	}
}

func keys(m map[string]*zip.File) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestRepackNormalizesLegacyEntryNames(t *testing.T) {
	manifestPath := filepath.Join(t.TempDir(), "DOSZ.TOML")
	writeFile(t, filepath.Dir(manifestPath), "DOSZ.TOML", "format = 1\nname = \"Legacy\"\n")

	tests := []struct {
		name    string
		entries [][2]string
		want    []string // entry paths, DOSZ.TOML aside
		wantErr string
	}{
		{"backslashes", [][2]string{{`GAME\DATA\X.DAT`, "x"}, {`GAME\`, ""}}, []string{"GAME/DATA/X.DAT"}, ""},
		{"dot and doubled slash", [][2]string{{"./README.TXT", "r"}, {"GAME//Y.DAT", "y"}, {"/Z.DAT", "z"}}, []string{"GAME/Y.DAT", "README.TXT", "Z.DAT"}, ""},
		{"leaves the archive", [][2]string{{"../EVIL.EXE", "x"}}, nil, "not a file path within the archive"},
		{"leaves it further in", [][2]string{{`GAME\..\..\EVIL.EXE`, "x"}}, nil, "not a file path within the archive"},
		{"same entry twice", [][2]string{{`GAME\X.DAT`, "1"}, {"GAME/X.DAT", "2"}}, nil, `are both GAME/X.DAT`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "out.dosz")
			_, err := repack.Run(repack.Options{InputPath: legacyArchive(t, tt.entries), OutputPath: out, ManifestPath: manifestPath})
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want one containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for name := range readZip(t, out) {
				if name != manifest.FileName {
					got = append(got, name)
				}
			}
			sort.Strings(got)
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Errorf("entries = %v, want %v", got, tt.want)
			}
			if err := verify.Verify(out); err != nil {
				t.Errorf("Verify: %v", err)
			}
		})
	}
}

// TestRepackStrictAfterLayout: the archive's own trim patterns and
// rewrite rules were applied when it was built, so finding nothing more
// on a repack is no warning, and --strict passes; a new rule from
// another manifest that matches nothing still fails it.
func TestRepackStrictAfterLayout(t *testing.T) {
	const layout = plainManifest + "[layout]\ntrim = ['*.TMP']\n[[layout.rewrite]]\nfrom = 'OLD'\nto = 'NEW'\nfiles = ['GAME.CFG']\n"
	in := buildArchive(t, layout, map[string]string{"GAME.EXE": "MZ", "X.TMP": "x", "GAME.CFG": "dir=OLD\r\n"})
	out := filepath.Join(t.TempDir(), "out.dosz")
	if _, err := repack.Run(repack.Options{InputPath: in, OutputPath: out, Strict: true}); err != nil {
		t.Fatalf("strict repack of its own layout: %v", err)
	}
	other := filepath.Join(t.TempDir(), "DOSZ.TOML")
	os.WriteFile(other, []byte(layout+"[[layout.rewrite]]\nfrom = 'NONE'\nto = 'X'\nfiles = ['GAME.CFG']\n"), 0o644)
	if _, err := repack.Run(repack.Options{InputPath: in, OutputPath: out, ManifestPath: other, Strict: true}); err == nil {
		t.Error("a new rule matching nothing passed --strict")
	}
}
