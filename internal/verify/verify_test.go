package verify_test

import (
	"archive/zip"
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DemiCloud/doszer/internal/build"
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

// buildArchive builds a real, conforming archive from a minimal source, and
// returns its path.
func buildArchive(t *testing.T, manifestBody string) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "DOSZ.TOML", manifestBody)
	writeFile(t, dir, "GAME.EXE", "binary")

	out := filepath.Join(t.TempDir(), "out.dosz")
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

func TestVerifyAcceptsAConformingArchive(t *testing.T) {
	path := buildArchive(t, plainManifest)
	if err := verify.Verify(path); err != nil {
		t.Errorf("Verify(conforming archive) = %v, want nil", err)
	}
}

func TestVerifyRejectsMissingArchive(t *testing.T) {
	if err := verify.Verify(filepath.Join(t.TempDir(), "nope.dosz")); err == nil {
		t.Fatal("Verify should fail for a missing file")
	}
}

func TestVerifyRejectsMissingManifest(t *testing.T) {
	path := buildArchive(t, plainManifest)
	rewriteZip(t, path, func(w *zip.Writer, f *zip.File) error {
		if f.Name == "DOSZ.TOML" {
			return nil // drop it
		}
		return copyEntry(w, f)
	})

	err := verify.Verify(path)
	if err == nil || !contains(err.Error(), "missing DOSZ.TOML") {
		t.Fatalf("Verify() = %v, want an error about the missing manifest", err)
	}
}

func TestVerifyAcceptsNonEpochOrdinaryFile(t *testing.T) {
	// An ordinary collected file keeps its own source timestamp (§8.2);
	// verify must not reject that.
	path := buildArchive(t, plainManifest)
	rewriteZip(t, path, func(w *zip.Writer, f *zip.File) error {
		if f.Name != "GAME.EXE" {
			return copyEntry(w, f)
		}
		fh := f.FileHeader
		fh.Modified = time.Time{}
		fh.ModifiedDate, fh.ModifiedTime = toDOSTime(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
		fw, err := w.CreateHeader(&fh)
		if err != nil {
			return err
		}
		data, err := readEntry(f)
		if err != nil {
			return err
		}
		_, err = fw.Write(data)
		return err
	})

	if err := verify.Verify(path); err != nil {
		t.Errorf("Verify() = %v, want nil (an ordinary file's own timestamp is not checked)", err)
	}
}

func TestVerifyRejectsNonEpochAutoboot(t *testing.T) {
	// AUTOBOOT.DBP is generated (§6.8) and must use the fixed epoch
	// (§8.2), even if its content still matches the manifest's boot
	// settings.
	path := buildArchive(t, plainManifest)
	rewriteZip(t, path, func(w *zip.Writer, f *zip.File) error {
		if f.Name != "AUTOBOOT.DBP" {
			return copyEntry(w, f)
		}
		fh := f.FileHeader
		fh.Modified = time.Time{}
		fh.ModifiedDate, fh.ModifiedTime = toDOSTime(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
		fw, err := w.CreateHeader(&fh)
		if err != nil {
			return err
		}
		data, err := readEntry(f)
		if err != nil {
			return err
		}
		_, err = fw.Write(data)
		return err
	})

	err := verify.Verify(path)
	if err == nil || !contains(err.Error(), "fixed epoch") {
		t.Fatalf("Verify() = %v, want a fixed-epoch error", err)
	}
}

func TestVerifyRejectsNonZeroExternalAttrs(t *testing.T) {
	path := buildArchive(t, plainManifest)
	rewriteZip(t, path, func(w *zip.Writer, f *zip.File) error {
		if f.Name != "GAME.EXE" {
			return copyEntry(w, f)
		}
		fh := f.FileHeader
		fh.Modified = time.Time{}
		fh.ExternalAttrs = 0o644 << 16
		fw, err := w.CreateHeader(&fh)
		if err != nil {
			return err
		}
		data, err := readEntry(f)
		if err != nil {
			return err
		}
		_, err = fw.Write(data)
		return err
	})

	err := verify.Verify(path)
	if err == nil || !contains(err.Error(), "external attributes") {
		t.Fatalf("Verify() = %v, want an external-attributes error", err)
	}
}

func TestVerifyRejectsUnsortedEntries(t *testing.T) {
	path := buildArchive(t, plainManifest)
	rewriteZipReordered(t, path, func(files []*zip.File) []*zip.File {
		// Swap the first two entries, breaking sort order.
		if len(files) >= 2 {
			files[0], files[1] = files[1], files[0]
		}
		return files
	})

	err := verify.Verify(path)
	if err == nil || !contains(err.Error(), "not ordered by path") {
		t.Fatalf("Verify() = %v, want an ordering error", err)
	}
}

func TestVerifyRejectsWrongStoreMethod(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "DOSZ.TOML", `
format = 1
name   = "Test Game"

[boot]
exec = 'C:\GAME.EXE'

[pack]
store = ["iso"]
`)
	writeFile(t, dir, "DATA.ISO", "iso content")
	out := filepath.Join(t.TempDir(), "out.dosz")
	if _, err := build.Run(build.Options{SourceDir: dir, OutputPath: out}); err != nil {
		t.Fatalf("build.Run: %v", err)
	}

	rewriteZip(t, out, func(w *zip.Writer, f *zip.File) error {
		if f.Name != "DATA.ISO" {
			return copyEntry(w, f)
		}
		fh := f.FileHeader
		fh.Modified = time.Time{}
		fh.Method = zip.Deflate
		fw, err := w.CreateHeader(&fh)
		if err != nil {
			return err
		}
		data, err := readEntry(f)
		if err != nil {
			return err
		}
		_, err = fw.Write(data)
		return err
	})

	err := verify.Verify(out)
	if err == nil || !contains(err.Error(), "must be stored") {
		t.Fatalf("Verify() = %v, want a store-method error", err)
	}
}

func TestVerifyRejectsForbiddenShellFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "DOSZ.TOML", `
format = 1
name   = "Shell Game"

[boot]
shell = "dosbox_pure/Win31"
`)
	out := filepath.Join(t.TempDir(), "out.dosz")
	if _, err := build.Run(build.Options{SourceDir: dir, OutputPath: out}); err != nil {
		t.Fatalf("build.Run: %v", err)
	}

	rewriteZip(t, out, func(w *zip.Writer, f *zip.File) error {
		if err := copyEntry(w, f); err != nil {
			return err
		}
		if f.Name == "DOSZ.TOML" {
			fw, err := w.Create("WINDOWS/WIN.INI")
			if err != nil {
				return err
			}
			_, err = fw.Write([]byte("junk"))
			return err
		}
		return nil
	})

	err := verify.Verify(out)
	if err == nil || !contains(err.Error(), "shell-owned file") {
		t.Fatalf("Verify() = %v, want a shell-owned-file error", err)
	}
}

func TestVerifyRejectsMissingAutoboot(t *testing.T) {
	path := buildArchive(t, plainManifest)
	rewriteZip(t, path, func(w *zip.Writer, f *zip.File) error {
		if f.Name == "AUTOBOOT.DBP" {
			return nil
		}
		return copyEntry(w, f)
	})

	err := verify.Verify(path)
	if err == nil || !contains(err.Error(), "missing AUTOBOOT.DBP") {
		t.Fatalf("Verify() = %v, want a missing-AUTOBOOT.DBP error", err)
	}
}

func TestVerifyRejectsNonEmptyParentMarker(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "DOSZ.TOML", `
format = 1
name   = "Child Game"

[boot]
exec   = 'C:\GAME.EXE'
parent = "Shared.shell"
`)
	writeFile(t, dir, "GAME.EXE", "binary")
	out := filepath.Join(t.TempDir(), "out.dosz")
	if _, err := build.Run(build.Options{SourceDir: dir, OutputPath: out}); err != nil {
		t.Fatalf("build.Run: %v", err)
	}

	rewriteZip(t, out, func(w *zip.Writer, f *zip.File) error {
		if f.Name != "Shared.shell.parent" {
			return copyEntry(w, f)
		}
		fh := f.FileHeader
		fw, err := w.CreateHeader(&fh)
		if err != nil {
			return err
		}
		_, err = fw.Write([]byte("not empty"))
		return err
	})

	err := verify.Verify(out)
	if err == nil || !contains(err.Error(), "must be zero bytes") {
		t.Fatalf("Verify() = %v, want a non-zero-parent-marker error", err)
	}
}

func TestVerifyRejectsNonCRLFTextFile(t *testing.T) {
	path := buildArchive(t, plainManifest)
	rewriteZip(t, path, func(w *zip.Writer, f *zip.File) error {
		if f.Name != "AUTOBOOT.DBP" {
			return copyEntry(w, f)
		}
		// Add a batch file with LF-only line endings after the fact,
		// simulating a repack (or a legacy archive) that skipped §6.6.
		if err := copyEntry(w, f); err != nil {
			return err
		}
		fw, err := w.Create("AUTOEXEC.BAT")
		if err != nil {
			return err
		}
		_, err = fw.Write([]byte("@ECHO OFF\nGAME.EXE\n"))
		return err
	})

	err := verify.Verify(path)
	if err == nil || !contains(err.Error(), "not CRLF-normalized") {
		t.Fatalf("Verify() = %v, want a CRLF-normalization error", err)
	}
}

func TestVerifyRejectsExcludedFile(t *testing.T) {
	path := buildArchive(t, plainManifest)
	rewriteZip(t, path, func(w *zip.Writer, f *zip.File) error {
		if err := copyEntry(w, f); err != nil {
			return err
		}
		if f.Name != "DOSZ.TOML" {
			return nil
		}
		fw, err := w.Create("desktop.ini")
		if err != nil {
			return err
		}
		_, err = fw.Write([]byte("[.ShellClassInfo]"))
		return err
	})

	err := verify.Verify(path)
	if err == nil || !contains(err.Error(), "must not be in the archive") {
		t.Fatalf("Verify() = %v, want an exclusion error", err)
	}
}

func TestVerifyRejectsStaleAutoboot(t *testing.T) {
	path := buildArchive(t, plainManifest)
	rewriteZip(t, path, func(w *zip.Writer, f *zip.File) error {
		if f.Name != "AUTOBOOT.DBP" {
			return copyEntry(w, f)
		}
		fh := f.FileHeader
		fw, err := w.CreateHeader(&fh)
		if err != nil {
			return err
		}
		_, err = fw.Write([]byte("STALE"))
		return err
	})

	err := verify.Verify(path)
	if err == nil || !contains(err.Error(), "does not match the manifest's boot settings") {
		t.Fatalf("Verify() = %v, want a stale-AUTOBOOT.DBP error", err)
	}
}

// --- test helpers: rewrite an existing archive, entry by entry ---

func readEntry(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(rc); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func copyEntry(w *zip.Writer, f *zip.File) error {
	fh := f.FileHeader
	// Zero Modified before CreateHeader: archive/zip derives
	// ModifiedDate/ModifiedTime from it and adds its own extra timestamp
	// field whenever it's non-zero, which §8.2 forbids. The legacy fields
	// already carry the real timestamp; copying them is enough.
	fh.Modified = time.Time{}
	fw, err := w.CreateHeader(&fh)
	if err != nil {
		return err
	}
	data, err := readEntry(f)
	if err != nil {
		return err
	}
	_, err = fw.Write(data)
	return err
}

// rewriteZip rewrites path's entries through visit, which may copy, modify,
// drop or add entries via w.
func rewriteZip(t *testing.T, path string, visit func(w *zip.Writer, f *zip.File) error) {
	t.Helper()
	r, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, f := range r.File {
		if err := visit(w, f); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// rewriteZipReordered rewrites path with its entries passed through
// reorder, to produce an archive whose entries aren't stored in sorted
// order (zip.Writer itself doesn't care about order).
func rewriteZipReordered(t *testing.T, path string, reorder func([]*zip.File) []*zip.File) {
	t.Helper()
	r, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, f := range reorder(r.File) {
		if err := copyEntry(w, f); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func contains(s, substr string) bool {
	return bytes.Contains([]byte(s), []byte(substr))
}

// toDOSTime converts t to MS-DOS date/time encoding (2-second resolution).
func toDOSTime(t time.Time) (date, timeField uint16) {
	date = uint16(t.Day() + int(t.Month())<<5 + (t.Year()-1980)<<9)
	timeField = uint16(t.Second()/2 + t.Minute()<<5 + t.Hour()<<11)
	return date, timeField
}

func TestVerifyDetectsCorruptBinaryEntry(t *testing.T) {
	// GAME.EXE isn't a text file, so verify streams it rather than
	// reading it into memory; the CRC-32 check must still catch damage.
	path := buildArchive(t, plainManifest+"\n[pack]\nstore = [\"exe\"]\n")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	i := bytes.Index(data, []byte("binary"))
	if i < 0 {
		t.Fatal("stored GAME.EXE content not found")
	}
	copy(data[i:], "BINARY")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verify.Verify(path); err == nil {
		t.Fatal("Verify accepted a corrupted entry")
	}
}

func TestCheckMedia(t *testing.T) {
	iso := bytes.Repeat([]byte("ISO user data. "), 2048) // 15 sectors
	sum := sha1.Sum(iso)
	right := hex.EncodeToString(sum[:])
	wrong := strings.Repeat("0", 40)

	tests := []struct {
		name     string
		sha1     string
		warnings int
	}{
		{"matching disc", right, 0},
		{"different disc", wrong, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, "DOSZ.TOML", plainManifest+`
[[source.media]]
id     = "disc1"
tracks = [{ type = "data", sha1 = "`+tt.sha1+`" }]
`)
			writeFile(t, dir, "GAME.EXE", "binary")
			writeFile(t, dir, "Test Game.iso", string(iso))
			out := filepath.Join(t.TempDir(), "out.dosz")
			if _, err := build.Run(build.Options{SourceDir: dir, OutputPath: out}); err != nil {
				t.Fatalf("build.Run: %v", err)
			}
			warnings, err := verify.Check(out)
			if err != nil {
				t.Fatalf("Check: %v", err)
			}
			if len(warnings) != tt.warnings {
				t.Errorf("warnings %q, want %d", warnings, tt.warnings)
			}
		})
	}
}

func TestVerifyRejectsDirectoryEntry(t *testing.T) {
	path := buildArchive(t, plainManifest)
	first := true
	rewriteZip(t, path, func(w *zip.Writer, f *zip.File) error {
		if first {
			// "A/" sorts before every entry buildArchive writes.
			first = false
			if _, err := w.CreateHeader(&zip.FileHeader{Name: "A/", Method: zip.Store}); err != nil {
				return err
			}
		}
		return copyEntry(w, f)
	})
	err := verify.Verify(path)
	if err == nil || !contains(err.Error(), "directory entry") {
		t.Fatalf("Verify() = %v, want a directory entry error", err)
	}
}

const padManifest = plainManifest + `
[[input.pad]]
b = "space Fire"
l3 = "menu"
wheel = ["f1 Help"]
`

func TestVerifyAcceptsInputPad(t *testing.T) {
	if err := verify.Verify(buildArchive(t, padManifest)); err != nil {
		t.Fatalf("Verify() = %v", err)
	}
}

func TestVerifyRejectsStaleInputPadFiles(t *testing.T) {
	for _, name := range []string{"PADMAP.DBP", "DOS.YML"} {
		t.Run(name, func(t *testing.T) {
			path := buildArchive(t, padManifest)
			rewriteZip(t, path, func(w *zip.Writer, f *zip.File) error {
				if f.Name != name {
					return copyEntry(w, f)
				}
				fh := f.FileHeader
				fh.Modified = time.Time{}
				fw, err := w.CreateHeader(&fh)
				if err != nil {
					return err
				}
				_, err = fw.Write([]byte("input_pad_b: enter\r\n"))
				return err
			})
			err := verify.Verify(path)
			if err == nil || !contains(err.Error(), name+": content does not match the manifest's [[input.pad]]") {
				t.Fatalf("Verify() = %v, want a stale-%s error", err, name)
			}
		})
	}
}

func TestVerifyDetectsCorruptDiscImage(t *testing.T) {
	// The ISO is read through by the media check (§7.3), whose CRC-32
	// result verify reuses instead of reading it again: damage must still
	// be an error, not only a media warning.
	iso := bytes.Repeat([]byte("ISO user data. "), 2048)
	sum := sha1.Sum(iso)
	dir := t.TempDir()
	writeFile(t, dir, "DOSZ.TOML", plainManifest+`
[[source.media]]
id     = "disc1"
tracks = [{ type = "data", sha1 = "`+hex.EncodeToString(sum[:])+`" }]

[pack]
store = ["iso"]
`)
	writeFile(t, dir, "GAME.EXE", "binary")
	writeFile(t, dir, "Test Game.iso", string(iso))
	out := filepath.Join(t.TempDir(), "out.dosz")
	if _, err := build.Run(build.Options{SourceDir: dir, OutputPath: out}); err != nil {
		t.Fatalf("build.Run: %v", err)
	}
	if err := verify.Verify(out); err != nil {
		t.Fatalf("Verify before damage: %v", err)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	i := bytes.Index(data, []byte("ISO user data."))
	copy(data[i+5000:], "DAMAGE")
	if err := os.WriteFile(out, data, 0o644); err != nil {
		t.Fatal(err)
	}
	err = verify.Verify(out)
	if err == nil || !strings.Contains(err.Error(), "Test Game.iso: zip: checksum error") {
		t.Fatalf("Verify = %v, want a checksum error for the ISO", err)
	}
}

func TestVerifyRejectsMalformedEntryNames(t *testing.T) {
	for _, bad := range []string{"./GAME.CFG", "GAME//X.DAT", `GAME\X.DAT`, "GAME/./X.DAT", "GAME/../X.DAT"} {
		t.Run(bad, func(t *testing.T) {
			path := buildArchive(t, plainManifest)
			rewriteZip(t, path, func(w *zip.Writer, f *zip.File) error {
				if f.Name == "DOSZ.TOML" { // keep the bad entry where sorting puts it
					if _, err := w.CreateRaw(&zip.FileHeader{Name: bad, Method: zip.Store}); err != nil {
						return err
					}
				}
				return copyEntry(w, f)
			})
			err := verify.Verify(path)
			if err == nil || !strings.Contains(err.Error(), "entry name") || !strings.Contains(err.Error(), "§8.2") {
				t.Fatalf("Verify = %v, want an entry name error", err)
			}
		})
	}
}

// rawDisc is a one-track Mode 1 BIN/CUE whose primary volume descriptor
// records date.
func rawDisc(t *testing.T, dir, date string) {
	t.Helper()
	bin := make([]byte, 20*2352)
	for i := 0; i < 20; i++ {
		bin[i*2352+15] = 1 // Mode 1
	}
	pvd := bin[16*2352+16:]
	pvd[0] = 1
	copy(pvd[1:], "CD001")
	copy(pvd[813:], date)
	writeFile(t, dir, "disc.bin", string(bin))
	writeFile(t, dir, "disc.cue", "FILE \"disc.bin\" BINARY\r\n  TRACK 01 MODE1/2352\r\n    INDEX 01 00:00:00\r\n")
}

func TestDiscImageFilesUseTheDiscsDate(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "DOSZ.TOML", plainManifest)
	writeFile(t, dir, "GAME.EXE", "binary")
	rawDisc(t, dir, "1996110912120000")
	out := filepath.Join(t.TempDir(), "out.dosz")
	if _, err := build.Run(build.Options{SourceDir: dir, OutputPath: out}); err != nil {
		t.Fatal(err)
	}
	r, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	want := time.Date(1996, 11, 9, 12, 12, 0, 0, time.UTC)
	for _, f := range r.File {
		if strings.HasPrefix(f.Name, "disc.") && !f.Modified.UTC().Equal(want) {
			t.Errorf("%s: modified %v, want %v", f.Name, f.Modified.UTC(), want)
		}
	}
	if err := verify.Verify(out); err != nil {
		t.Errorf("Verify: %v", err)
	}
}

func TestVerifyRejectsWrongDiscDate(t *testing.T) {
	for _, tc := range []struct{ name, cueFile string }{
		{"same case", "disc.bin"},
		// A legacy archive's sheet often spells its files in another
		// case: the date check must still find the entry (§2).
		{"other case", "DISC.BIN"},
	} {
		t.Run(tc.name, func(t *testing.T) { testWrongDiscDate(t, tc.cueFile) })
	}
}

func testWrongDiscDate(t *testing.T, cueFile string) {
	src := t.TempDir()
	rawDisc(t, src, "1996110912120000")
	writeFile(t, src, "disc.cue", "FILE \""+cueFile+"\" BINARY\r\n  TRACK 01 MODE1/2352\r\n    INDEX 01 00:00:00\r\n")
	out := filepath.Join(t.TempDir(), "out.dosz")
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, name := range []string{"disc.bin", "disc.cue"} {
		data, err := os.ReadFile(filepath.Join(src, name))
		if err != nil {
			t.Fatal(err)
		}
		// The legacy MS-DOS fields, so archive/zip adds no extra field:
		// 2001-01-01 00:00:00.
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store, ModifiedDate: 21<<9 | 1<<5 | 1})
		if err != nil {
			t.Fatal(err)
		}
		w.Write(data)
	}
	zw.Close()
	f.Close()
	err = verify.Verify(out)
	if err == nil || !strings.Contains(err.Error(), "disc.bin: a disc image file must use the disc's date, 1996-11-09 12:12:00") {
		t.Fatalf("Verify = %v, want a disc date error", err)
	}
}
