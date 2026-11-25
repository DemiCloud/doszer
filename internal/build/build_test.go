package build_test

import (
	"archive/zip"
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

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

	// §8.2: zero external attributes, no extra fields, for every entry.
	// Only the generated files (AUTOBOOT.DBP, DOSZ.TOML) use the fixed
	// epoch; a collected file keeps its own source timestamp.
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

func TestRunPreservesSourceTimestamp(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "DOSZ.TOML", `
format = 1
name   = "Timestamp Game"

[boot]
exec = 'C:\GAME.EXE'
`)
	writeFile(t, dir, "GAME.EXE", "binary")

	sourcePath := filepath.Join(dir, "GAME.EXE")
	sourceTime := time.Date(1994, 6, 15, 10, 30, 0, 0, time.UTC)
	if err := os.Chtimes(sourcePath, sourceTime, sourceTime); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(t.TempDir(), "out.dosz")
	if _, err := build.Run(build.Options{SourceDir: dir, OutputPath: out}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	entries := readZip(t, out)
	got := entries["GAME.EXE"].Modified.UTC()
	if !got.Equal(sourceTime) {
		t.Errorf("GAME.EXE Modified = %v, want the source file's own mtime %v (§8.2)", got, sourceTime)
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

func TestRunLeavesOutOwnOutput(t *testing.T) {
	// Built from inside the source, the archive lands in the source: the
	// next build must replace it, not pack it.
	for _, tc := range []struct{ name, output string }{
		{"default path", ""},
		{"given path", "out.dosz"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := buildFixture(t)
			wd, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Chdir(dir); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.Chdir(wd) })

			for i := 0; i < 2; i++ {
				res, err := build.Run(build.Options{SourceDir: ".", OutputPath: tc.output})
				if err != nil {
					t.Fatalf("Run %d: %v", i+1, err)
				}
				for _, e := range res.Entries {
					if strings.HasSuffix(e, ".dosz") {
						t.Fatalf("build %d packed %s", i+1, e)
					}
				}
			}
		})
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

func TestRunFailsWhenMediaNotFound(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "DOSZ.TOML", `
format = 1
name   = "Needs Media"

[[source.media]]
id = "disc1"
tracks = [{ type = "data", sha1 = "0123456789abcdef0123456789abcdef01234567" }]

[[extract]]
media = "disc1"
from  = "GAME"
to    = "GAME"
`)

	_, err := build.Run(build.Options{SourceDir: dir, OutputPath: filepath.Join(t.TempDir(), "out.dosz")})
	if err == nil || !strings.Contains(err.Error(), "disc1") {
		t.Fatalf("Run error = %v, want an error naming the unresolved media", err)
	}
}

func TestRunExtractsFromMedia(t *testing.T) {
	dir := t.TempDir()

	iso := buildMinimalISO(t, "DATA.TXT", []byte("extracted content"))
	writeFile(t, dir, "DISC/game.bin", string(iso))
	writeFile(t, dir, "DISC/game.cue", "FILE \"game.bin\" BINARY\nTRACK 01 MODE1/2048\nINDEX 01 00:00:00\n")

	sum := sha1.Sum(iso)
	writeFile(t, dir, "DOSZ.TOML", fmt.Sprintf(`
format = 1
name   = "Extract Game"

[[source.media]]
id = "disc1"
tracks = [{ type = "data", sha1 = "%x" }]

[[extract]]
media = "disc1"
from  = "DATA.TXT"
to    = "GAME/DATA.TXT"
`, sum))

	out := filepath.Join(t.TempDir(), "out.dosz")
	res, err := build.Run(build.Options{SourceDir: dir, OutputPath: out})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	found := false
	for _, e := range res.Entries {
		if e == "GAME/DATA.TXT" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Entries = %v, missing GAME/DATA.TXT", res.Entries)
	}

	entries := readZip(t, out)
	if got := readZipFile(t, entries["GAME/DATA.TXT"]); got != "extracted content" {
		t.Errorf("GAME/DATA.TXT = %q", got)
	}
}

func TestRunExtractsDirectoryRecursively(t *testing.T) {
	dir := t.TempDir()

	iso := buildISOWithSubdir(t, "RESOURCE", map[string][]byte{
		"A.DAT": []byte("file a"),
		"B.DAT": []byte("file b"),
	})
	writeFile(t, dir, "DISC/game.bin", string(iso))
	writeFile(t, dir, "DISC/game.cue", "FILE \"game.bin\" BINARY\nTRACK 01 MODE1/2048\nINDEX 01 00:00:00\n")

	sum := sha1.Sum(iso)
	writeFile(t, dir, "DOSZ.TOML", fmt.Sprintf(`
format = 1
name   = "Extract Dir Game"

[[source.media]]
id = "disc1"
tracks = [{ type = "data", sha1 = "%x" }]

[[extract]]
media = "disc1"
from  = "RESOURCE"
to    = "GAME/RESOURCE"

[pack]
store = ["dat"]
`, sum))

	out := filepath.Join(t.TempDir(), "out.dosz")
	res, err := build.Run(build.Options{SourceDir: dir, OutputPath: out})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Extracted files are written from ranges of the temporary disc image
	// (stored, so read with a seek); their content must be exact.
	entries := readZip(t, out)
	for name, want := range map[string]string{"GAME/RESOURCE/A.DAT": "file a", "GAME/RESOURCE/B.DAT": "file b"} {
		if got := readZipFile(t, entries[name]); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}

	want := []string{"GAME/RESOURCE/A.DAT", "GAME/RESOURCE/B.DAT"}
	for _, w := range want {
		found := false
		for _, e := range res.Entries {
			if e == w {
				found = true
			}
		}
		if !found {
			t.Errorf("Entries = %v, missing %s", res.Entries, w)
		}
	}
}

const isoSectorSize = 2048

// isoDirRecord builds one ECMA-119 Directory Record.
func isoDirRecord(id string, isDir bool, lba, size uint32) []byte {
	total := 33 + len(id)
	if total%2 != 0 {
		total++
	}
	rec := make([]byte, total)
	rec[0] = byte(total)
	binary.LittleEndian.PutUint32(rec[2:6], lba)
	binary.BigEndian.PutUint32(rec[6:10], lba)
	binary.LittleEndian.PutUint32(rec[10:14], size)
	binary.BigEndian.PutUint32(rec[14:18], size)
	if isDir {
		rec[25] = 0x02
	}
	rec[32] = byte(len(id))
	copy(rec[33:], id)
	return rec
}

func isoPadSector(b []byte) []byte {
	out := make([]byte, isoSectorSize)
	copy(out, b)
	return out
}

// isoVolumeDescriptors returns the system area, PVD (with the given root
// directory record) and terminator: every ISO image's first 18 sectors.
func isoVolumeDescriptors(root []byte, rootLBA uint32, rootSize int) []byte {
	var out bytes.Buffer
	out.Write(make([]byte, 16*isoSectorSize))

	pvd := make([]byte, isoSectorSize)
	pvd[0] = 1
	copy(pvd[1:6], "CD001")
	pvd[6] = 1
	copy(pvd[156:190], isoDirRecord("\x00", true, rootLBA, uint32(rootSize)))
	out.Write(pvd)

	term := make([]byte, isoSectorSize)
	term[0] = 255
	copy(term[1:6], "CD001")
	out.Write(term)

	return out.Bytes()
}

// buildMinimalISO builds a minimal valid ISO 9660 image (ECMA-119) with a
// single file at its root, for exercising extract end to end.
func buildMinimalISO(t *testing.T, fileName string, content []byte) []byte {
	t.Helper()

	const fileLBA, rootLBA = 18, 19
	root := bytes.Join([][]byte{
		isoDirRecord("\x00", true, 0, 0),
		isoDirRecord("\x01", true, 0, 0),
		isoDirRecord(fileName+";1", false, fileLBA, uint32(len(content))),
	}, nil)

	var out bytes.Buffer
	out.Write(isoVolumeDescriptors(root, rootLBA, len(root)))
	out.Write(isoPadSector(content)) // sector 18: file
	out.Write(isoPadSector(root))    // sector 19: root directory
	return out.Bytes()
}

// buildISOWithSubdir builds a minimal ISO 9660 image with one subdirectory
// containing two files, for exercising recursive directory extraction.
func buildISOWithSubdir(t *testing.T, subdir string, files map[string][]byte) []byte {
	t.Helper()

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	const firstFileLBA = 18
	subdirLBA := uint32(firstFileLBA + len(names))
	rootLBA := subdirLBA + 1

	subdirRecords := [][]byte{
		isoDirRecord("\x00", true, 0, 0),
		isoDirRecord("\x01", true, 0, 0),
	}
	var fileSectors bytes.Buffer
	for i, name := range names {
		lba := uint32(firstFileLBA + i)
		subdirRecords = append(subdirRecords, isoDirRecord(name+";1", false, lba, uint32(len(files[name]))))
		fileSectors.Write(isoPadSector(files[name]))
	}
	subdirContent := bytes.Join(subdirRecords, nil)

	root := bytes.Join([][]byte{
		isoDirRecord("\x00", true, 0, 0),
		isoDirRecord("\x01", true, 0, 0),
		isoDirRecord(subdir, true, subdirLBA, uint32(len(subdirContent))),
	}, nil)

	var out bytes.Buffer
	out.Write(isoVolumeDescriptors(root, rootLBA, len(root)))
	out.Write(fileSectors.Bytes())
	out.Write(isoPadSector(subdirContent)) // subdirLBA
	out.Write(isoPadSector(root))          // rootLBA
	return out.Bytes()
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

func TestRepackRunsTheFullPipeline(t *testing.T) {
	m, err := manifest.Parse([]byte(`
format = 1
name   = "Legacy Game"

[boot]
exec = 'C:\GAME.EXE'

[layout]
trim = ["**/*.SKC"]

[[layout.rewrite]]
from  = "OLDPATH"
to    = "NEWPATH"
files = ["**/*.BAT"]
`), "Legacy Game")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	entries := map[string]build.Entry{
		"GAME.EXE":          {Data: []byte("binary"), Modified: build.Epoch},
		"README.SKC":        {Data: []byte("trim me"), Modified: build.Epoch},
		"AUTOEXEC.BAT":      {Data: []byte("OLDPATH\nSECOND LINE\n"), Modified: build.Epoch}, // LF only, no CRLF yet
		"GAMENAME/DATA.DAT": {Data: []byte("nested"), Modified: build.Epoch},
	}

	res, err := build.Repack(entries, m, build.Options{OutputPath: filepath.Join(t.TempDir(), "out.dosz")})
	if err != nil {
		t.Fatalf("Repack: %v", err)
	}

	for _, want := range res.Entries {
		if want == "README.SKC" {
			t.Error("trim should have removed README.SKC")
		}
	}

	entries2 := readZip(t, res.OutputPath)
	if got := readZipFile(t, entries2["AUTOEXEC.BAT"]); got != "NEWPATH\r\nSECOND LINE\r\n" {
		t.Errorf("AUTOEXEC.BAT = %q, want rewrite + CRLF normalization applied", got)
	}
	if _, ok := entries2["GAMENAME/DATA.DAT"]; !ok {
		t.Error("nested entries not collected into the pipeline")
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

// TestDryRunSuggestsOnlyUncoveredReferences: flatten drops WRAP, so
// references to WRAP\ need a rewrite; the dry run suggests one unless the
// manifest's own rules already removed them.
func TestDryRunSuggestsOnlyUncoveredReferences(t *testing.T) {
	tests := []struct {
		name  string
		rules string
		want  int
	}{
		{"no rule", "", 1},
		{"rule covers it", "\n[[layout.rewrite]]\nfrom  = 'C:\\WRAP\\GAME'\nto    = 'D:\\GAME'\nfiles = [\"GAME/GAME.CFG\"]\n", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, "DOSZ.TOML", "format = 1\nname = \"Example Game\"\n\n[boot]\nexec = 'C:\\GAME\\GAME.EXE'\n\n[layout]\nflatten = true\n"+tt.rules)
			writeFile(t, dir, "WRAP/GAME/GAME.EXE", "MZ\x00")
			writeFile(t, dir, "WRAP/GAME/GAME.CFG", "path=C:\\WRAP\\GAME\\DATA\r\n")
			res, err := build.Run(build.Options{SourceDir: dir, DryRun: true, OutputPath: filepath.Join(t.TempDir(), "out.dosz")})
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Suggestions) != tt.want {
				t.Errorf("suggestions %q, want %d", res.Suggestions, tt.want)
			}
		})
	}
}

// §8.4: the provenance goes in the archive comment and changes nothing
// else - the entries are byte-identical to a build without it.
func TestRunWritesProvenanceComment(t *testing.T) {
	dir := buildFixture(t)
	tmp := t.TempDir()
	plain, commented := filepath.Join(tmp, "plain.dosz"), filepath.Join(tmp, "commented.dosz")
	if _, err := build.Run(build.Options{SourceDir: dir, OutputPath: plain}); err != nil {
		t.Fatal(err)
	}
	p := &build.Provenance{Generator: "doszer 1.2.3", Homepage: "https://example.com/doszer", Built: time.Date(2026, 11, 15, 23, 30, 0, 0, time.FixedZone("", -8*3600))}
	if _, err := build.Run(build.Options{SourceDir: dir, OutputPath: commented, Provenance: p}); err != nil {
		t.Fatal(err)
	}

	zr, err := zip.OpenReader(commented)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	want := "Test Game\r\nBuilt by doszer 1.2.3 on 2026-11-16 (DOSZ manifest format 1)\r\nhttps://example.com/doszer\r\n"
	if zr.Comment != want {
		t.Errorf("comment %q, want %q", zr.Comment, want)
	}

	a, _ := os.ReadFile(plain)
	b, _ := os.ReadFile(commented)
	// The end of central directory record is 22 bytes plus the comment;
	// everything before it, and the record up to its comment length, match.
	if n := len(a) - 2; !bytes.Equal(a[:n], b[:n]) {
		t.Error("entries differ with a comment")
	}
}

func TestProvenanceCommentWithoutHomepage(t *testing.T) {
	p := &build.Provenance{Generator: "x 1", Built: time.Unix(0, 0)}
	if got := p.Comment("Game", 1); got != "Game\r\nBuilt by x 1 on 1970-01-01 (DOSZ manifest format 1)\r\n" {
		t.Errorf("got %q", got)
	}
}

func TestRunRejectsISODirectoryLoop(t *testing.T) {
	dir := t.TempDir()

	// The root directory (sector 18) lists LOOP, which points back at the
	// root itself.
	const rootLBA = 18
	entry := func(size uint32) []byte {
		return bytes.Join([][]byte{
			isoDirRecord("\x00", true, 0, 0),
			isoDirRecord("\x01", true, 0, 0),
			isoDirRecord("LOOP", true, rootLBA, size),
		}, nil)
	}
	root := entry(uint32(len(entry(0))))
	var iso bytes.Buffer
	iso.Write(isoVolumeDescriptors(root, rootLBA, len(root)))
	iso.Write(isoPadSector(root))

	writeFile(t, dir, "DISC/game.bin", iso.String())
	writeFile(t, dir, "DISC/game.cue", "FILE \"game.bin\" BINARY\nTRACK 01 MODE1/2048\nINDEX 01 00:00:00\n")
	writeFile(t, dir, "DOSZ.TOML", fmt.Sprintf(`
format = 1
name   = "Loop Game"

[[source.media]]
id = "disc1"
tracks = [{ type = "data", sha1 = "%x" }]

[[extract]]
media = "disc1"
from  = "LOOP"
to    = "GAME"
`, sha1.Sum(iso.Bytes())))

	_, err := build.Run(build.Options{SourceDir: dir, OutputPath: filepath.Join(t.TempDir(), "out.dosz")})
	if err == nil || !strings.Contains(err.Error(), "directory loop") {
		t.Fatalf("Run error = %v, want a directory loop", err)
	}
}

func TestRunExtractsFromRawSectors(t *testing.T) {
	// A MODE1/2352 BIN can't be read in place: its data track is copied
	// while §7.3 hashes it, and extracted from that copy.
	iso := buildISOWithSubdir(t, "RESOURCE", map[string][]byte{"A.DAT": []byte("file a")})
	var bin bytes.Buffer
	for off := 0; off < len(iso); off += 2048 {
		sector := bytes.Repeat([]byte{0xEE}, 2352) // sync/header/ECC, stripped
		copy(sector[16:], iso[off:off+2048])
		bin.Write(sector)
	}
	dir := t.TempDir()
	writeFile(t, dir, "DISC/game.bin", bin.String())
	writeFile(t, dir, "DISC/game.cue", "FILE \"game.bin\" BINARY\nTRACK 01 MODE1/2352\nINDEX 01 00:00:00\n")
	writeFile(t, dir, "DOSZ.TOML", fmt.Sprintf(`
format = 1
name   = "Raw Extract Game"

[[source.media]]
id = "disc1"
tracks = [{ type = "data", sha1 = "%x" }]

[[extract]]
media = "disc1"
from  = "RESOURCE/A.DAT"
to    = "GAME/A.DAT"

[layout]
trim = ["DISC/**"]
`, sha1.Sum(iso)))

	outDir := t.TempDir()
	out := filepath.Join(outDir, "out.dosz")
	if _, err := build.Run(build.Options{SourceDir: dir, OutputPath: out}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := readZipFile(t, readZip(t, out)["GAME/A.DAT"]); got != "file a" {
		t.Errorf("GAME/A.DAT = %q", got)
	}
	left, _ := os.ReadDir(outDir)
	if len(left) != 1 {
		t.Errorf("output directory holds %d entries, want only the archive", len(left))
	}
}

func TestRunRejectsBackslashInSourceFileName(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a backslash is a separator on Windows")
	}
	dir := t.TempDir()
	writeFile(t, dir, "DOSZ.TOML", "format = 1\nname = \"Slash\"\n")
	if err := os.WriteFile(filepath.Join(dir, `GAME\X.TXT`), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := build.Run(build.Options{SourceDir: dir, OutputPath: filepath.Join(t.TempDir(), "out.dosz")})
	if err == nil || !strings.Contains(err.Error(), "must use / as separator") {
		t.Fatalf("Run error = %v, want the backslash rejected", err)
	}
}
