# AGENTS.md

Guidance for AI coding agents working in this repository.

## Project

`doszer` builds `.dosz` archives for DOSBox Pure from `DOSZ.TOML` manifests.
It is the reference implementation of the DOSZ Manifest Specification in
`spec/MANIFEST.md`. Other implementations of the specification may exist; the
specification, not this code, defines the format.

## Ground rules

- **The specification is normative.** Change `spec/MANIFEST.md` first, then
  the code. Behaviour not described in the specification belongs in
  `README.md` as implementation detail.
- **Keep the specification implementation-neutral.** No `doszer` CLI flags,
  config files or workflows in `spec/`. Use RFC 2119/8174 keywords there.
- **Verify DOSBox Pure behaviour against its source**, not tutorials or
  forum posts. Cite the file and function in commit messages or comments
  where behaviour depends on it.
- **Documentation stays generic.** Use placeholders (`/path/to/system`,
  `Example Game`, `example.com`); never real user names, home paths or
  hostnames.

## Layout

```
cmd/doszer/          CLI entry point and command dispatch
internal/manifest/   DOSZ.TOML parsing, key checking, validation, init template
internal/build/      build pipeline (spec §6), archive-content validation (§11)
internal/zipw/       deterministic ZIP writer (spec §8)
internal/config/     configuration layering (README "Configuration")
internal/cue/        CUE parsing, track byte ranges, normalized track hash (§7.2)
internal/iso9660/    ISO 9660 reader, for [[extract]] (§5.7)
internal/verify/     doszer verify: an archive against its manifest and spec §8/§11
internal/repack/     doszer repack: rebuild an archive from its own embedded manifest
internal/vhd/        VHD reader (fixed, dynamic), for install
internal/fat/        FAT12/16/32 reader with long names, as an io/fs.FS, for install
internal/vdisk/      DOSBox Pure's synthesized D: disk + .sav diffs (D: and -CDRIVE), for install
internal/install/    doszer install: session content, launching DOSBox Pure, diffing, extraction
internal/interrupt/  removes registered temporary paths on SIGINT/SIGTERM
examples/            complete example manifests (checked by internal/manifest's TestExamples)
Makefile             build/test/vet/install/release (see `make help`)
spec/MANIFEST.md     The specification (normative)
README.md            doszer usage, configuration, install workflow
```


## Conventions

- Go 1.24+. Code MUST pass `gofmt`, `go vet` and `go test ./...` (`make vet`,
  `make test`); `make build` for a local binary, `make help` for every
  target.
- Standard library first. Add a dependency only when reimplementing it would
  be substantial; current dependencies: `github.com/pelletier/go-toml/v2`,
  `github.com/spf13/pflag` (GNU-style options, the same library the
  sibling `romctl` uses through cobra - five commands don't need cobra).
- External programs: only `ffmpeg`, for Ogg Vorbis encoding (no maintained
  pure-Go encoder). Invoke it with `-fflags +bitexact` (fixed Ogg stream
  serial numbers) and `-flags +bitexact` (no encoder version string):
  with both, two builds produce byte-identical Ogg files.
- `internal/zipw` deflates at `flate.BestCompression` (level 9) itself,
  in parallel: input is split into 1 MiB blocks, each primed with the
  previous 32 KiB as its dictionary and sync-flushed, concatenated in order
  (pigz's scheme; `deflate.go`). Output depends only on the input, not on
  the CPU count. Measured on a local Descent II build: 28.8s to 7.1s on 32
  CPUs, for 5.6 KB more output on 320 MB. `build.writeArchive` also
  prepares entries concurrently (`zipw.Prepare`, up to one per CPU ahead
  of the entry being written, written in path order; one semaphore bounds
  Deflate blocks across all of them): Antara's 1235 small files built in
  1.0s instead of 4.6s, with byte-identical output. Block sizes must stay
  exact - a buffer grown with `append` once let blocks exceed 1 MiB by
  the runtime's size-class rounding (`TestDeflateShortReads`).
  `archive/zip`'s built-in compressor uses level 5 (`register.go`'s
  `newFlateWriter`), not even `flate.DefaultCompression` (6). It writes
  every entry through `CreateRaw` with CRC-32 and sizes in the local
  header and no data descriptor - `Add` computes them first (a stored
  seekable source is read twice; anything else goes through a spool,
  in memory up to 32 MB, then a temp file beside the output) - so an
  entry's bytes are the same whether `Add` compressed it or `AddRaw`
  copied it through, and repacking a doszer archive reproduces it
  byte for byte. Still
  weaker than a multi-pass encoder like 7-Zip's - investigated going
  further (see Resolved questions) and found no responsible option yet.
- Manifest fields use `omitempty` (`internal/manifest/manifest.go`) so a
  re-serialized manifest (`internal/build`'s `effectiveManifest`, used via
  `addRuntimeFiles` by both `build` and `repack`) doesn't emit every unset
  field as noise (`exec = ''`, `extract = []`, etc.) - confirmed against
  go-toml/v2 to omit a whole empty table, not just an empty scalar, before
  relying on it.
- Errors: wrap with context, return to `main`, which prints
  `[YYYY-MM-DD HH:MM:SS] ERROR: <message>` to stderr and exits 1. Validation
  collects every error and reports them together (`errors.Join`).
- Commands and options follow the patterns in `cmd/doszer/main.go`: one
  `pflag.FlagSet` per command (`newFlags`, `parse`), GNU-style - long
  `--option[=VALUE]`, short forms only for `-n`/`--dry-run`,
  `-f`/`--force`, `-h`/`--help` (and top-level `-V`/`--version`), options
  before or after arguments, `--` ending them. Help is hand-written,
  man-page brief, uppercase placeholders (`SOURCE`, `FILE`), shared option
  lines in `flag*` constants.
- Every configuration setting is also available as a flag. Precedence:
  flags, `<source>/DOSZ.TOML`, `./doszer.toml`,
  `$XDG_CONFIG_HOME/doszer/config.toml`, defaults.
- Tests are table-driven. Build test fixtures (disc images, ZIPs, disk images)
  programmatically in tests; do not commit binary game data.

## DOSBox Pure facts

Verified against DOSBox Pure 1.0 Preview 8 source (now at
`codeberg.org/schelling/dosbox-pure`; the GitHub repository is only a
pointer to it).

| Topic | Behaviour | Source |
|---|---|---|
| `AUTOBOOT.DBP` line 1 | Program path (`RUN_EXEC`), `S*<shell>`, `O*<image>`, `I*<machine>` or `V*<variant>` | `dosbox_pure_run.h`, `ReadAutoBoot` |
| `AUTOBOOT.DBP` line 2 | Frames to skip; only used when line 1 selects a startup mode | `ReadAutoBoot`, `autoboot.use` |
| `AUTOBOOT.DBP` line 3 | Disc image label to mount at startup; works without a startup mode | `ReadAutoBoot` |
| `O*`/`S*` paths | Relative to the system directory, extension omitted, exact case-sensitive match | `ReadAutoBoot` |
| OS image and shell discovery | Recursive scan of the system directory; `DOSBoxPureMidiCache.txt` caches the list | `dosbox_pure_libretro.cpp` |
| Parent marker | Zero-byte root entry `<name>.parent`; parent resolved in the archive's directory; chains allowed | `src/dos/drive_zip.cpp` |
| `DOSBOX.BAT` | Run instead of the start menu; bypasses `AUTOBOOT.DBP` | `dosbox_pure_libretro.cpp` |
| Shell startup | `WINDOWS.BAT`, then `AUTOEXEC.BAT`, then `WINDOWS\WIN.COM` | `dosbox_pure_run.h` |
| OS image model | Archive content is a synthesized FAT disk (D:); CD images appear as E: | `src/ints/bios_disk.cpp`, `fatFromDOSDrive` |
| Save files | C: changes for ZIP content: `<save dir>/<content>.pure.zip`. OS image C: diff: `<content>-CDRIVE.sav`. D: disk: `<content>-<HASH>.sav` | `DBP_GetSaveFile` |
| CD audio in CUE | PCM WAV and Ogg Vorbis only (`stb_vorbis`) | `src/dos/cdrom_image.cpp` |
| Large deflated files | Random access requires full decompression once; an index is cached in the save | README |
| `dosbox_pure_bootos_ramdisk` | `"false"` Keep (default: C: writes go into the OS image), `"true"` Discard, `"diff"` Save Difference Per Content. Only affects C:. Authoring uses Keep; spec §12's `diff` is the production setting (e.g. RetroDECK), which must never modify the image | `core_options.h`; `dosbox_pure_run.h`, `RunBoot` |
| D: changes (OS image model) | Saved to `<content>-<HASH>.sav` under every ramdisk setting; only `dosbox_pure_bootos_dfreespace` = `discard`/`hide` disables it | `dosbox_pure_run.h`, `RunBoot`; `DBP_GetSaveFile(SFT_VIRTUALDISK)` |
| `diff` on a dynamic VHD | Loses writes: `Write_AbsoluteSector` compares a written sector against the image *file's* bytes at `sector*512` (not the VHD's sector) to decide if it changed; a match (typically an all-zero sector where the file holds zeros) is not saved, or frees a saved one, and reads back as the VHD's old content. Raw `.img` and fixed VHDs are unaffected (sector N at `N*512`). Seen live: install sessions on a dynamic Win98 VHD lost files' zero-padded last sectors and corrupted directory/FAT sectors; `install` refuses a dynamic VHD | `src/ints/bios_disk.cpp`, `imageDisk::Write_AbsoluteSector` |
| `.sav` disk diff format | `FFDD\x01`, then records of a little-endian u32 sector number + 512 bytes; sector `0xFFFFFFFF` is a freed slot. Used for D: (over the disk `fatFromDOSDrive` synthesizes from the content) and for C: under `diff` (`<content>-CDRIVE.sav`, over the OS image) | `src/ints/bios_disk.cpp`, `differencingDisk`, `imageDisk::SetDifferencingDisk` |
| Synthesized D: disk | `fatFromDOSDrive` builds a FAT disk from the content's DOS view: directory order is `zipDrive`'s hash map slot order (`BaseHashMap`, FNV-1 over sign-extended chars), short names come from `DBP_Make8dot3FileName`/`IncrementName` and are then regenerated Windows-style (`~N`) for long-named entries. Root holds the volume label (`Set_Label` form of `<name, ≤7 chars>-<ext>`). Hidden from D: `AUTOBOOT.DBP`, `PADMAP.DBP`, `FRONTEND.DBP`, `DOSBOX.CON`, `*.SKC`, and mountable images (ISO/CHD/CUE, validated IMG/IMA/VHD/JRC) except one sharing a CUE's name | `src/ints/bios_disk.cpp`, `fatFromDOSDrive`; `src/dos/drive_zip.cpp`; `dosbox_pure_libretro.cpp`, `DBP_GetSaveFile`, `init_dosbox_parse_drives` |
| D: save hash | The `<HASH>` in `<content>-<HASH>.sav` is a CRC32 chained over every unhidden path, size, date, time and attributes (seeded with `dfreespace`); it is also the disk serial, so the name identifies exactly which content layout the diff belongs to | `DBP_GetSaveFile(SFT_VIRTUALDISK)` |
| Standalone launch | `DOSBoxPure.exe <content> [disc...]`: first non-`-` argument is the content. `DOSBoxPure.cfg` (JSON) is taken from the working directory if present, else `%APPDATA%\DOSBoxPure` if that exists, else beside the exe; `path_saves`/`path_system` override the `saves`/`system` directories beside it | dosbox-pure-unleashed `main.cpp`, `sDOSBoxPure::Load`, `OnLoad` |
| Per-content options | Standalone applies a `FRONTEND.DBP` JSON object in the content root as option overrides (and reboots to apply them); it is hidden from D: | `dosbox_pure_libretro.cpp`, `init_dosbox`, `DBPS_ApplyConfigOverrides` |
| `.pure.zip` save | ZIP of changed/added files by DOS path, plus `FILEMODS.DBP` lines `DELETE|path`, `REDIRECTFILE|target|source`, `REDIRECTDIR|target|source` | `src/dos/drive_union.cpp`, `Union_Modification`, `ReadSaveFile` |
| VHD support | Fixed and dynamic only (a parent name is rejected); a dynamic block sector whose bitmap bit is clear reads as zeros even if the file holds bytes there | `src/ints/bios_disk.cpp`, `sparseVhd::Init`, `sparseVhd::SeeBlock` |
| CUE `FILE` type | `BINARY` reads raw sectors; any other type opens an `AudioFile`, which detects RIFF/WAVE (16-bit PCM only) or OggS from the file's own header (`C_SDL_SOUND` is undefined in `include/config.h`) | `src/dos/cdrom_image.cpp`, `LoadCueSheet`, `AudioFile::AudioFile` |
| CUE `PREGAP` | Adds to the track's start without reading data; an INDEX 00 span in a track's own FILE is skipped and *not* counted toward position | `src/dos/cdrom_image.cpp`, `AddTrack` |
| Disc image list | Every ISO/CUE/CHD/... in the content is listed, except an image sharing a CUE's name minus extension (so `<base>.iso` beside `<base>.cue` is hidden) | `dosbox_pure_libretro.cpp`, "Filter image files that have the same name as a cue file" |

## Status

| Command | State |
|---|---|
| `init` | Implemented |
| `build` | Implemented |
| `install` | Implemented; live plain and OS image (`--drive c`) sessions passed; `--from` checked on a real D: save (Next steps) |
| `repack` | Implemented |
| `verify` | Implemented |

Manifest parsing and every manifest rule in specification §11 are
implemented and tested. `build` implements §6 in full and enforces the
archive-content rules of §11 (shell-owned files, `DOSBOX.BAT`).
`verify` (`internal/verify`) applies those same rules, plus §6.4's
conversion (via `build.UnconvertedMedia`), §7.3's media matching as
warnings (via `build.ArchiveMedia`, `verify.Check`), §8's
determinism checks, §6.6's text normalization, §6.7's exclusions, and
§6.8's runtime files - checking their *content* against what the embedded
manifest implies (e.g. AUTOBOOT.DBP's bytes must match
`build.BuildAutoboot(&m.Boot)` exactly, not just exist), to an already-built
archive and its embedded manifest - including one not built by doszer.
`build.ValidateArchiveContent`, `build.ShouldStore`, `build.IsTextFile`,
`build.NormalizeCRLF`, `build.IsExcluded`, `build.NeedsAutoboot` and
`build.BuildAutoboot` are exported from `internal/build` specifically so
`verify` (and `repack`) can reuse them rather than duplicate the rules.

`[[extract]]` and media identification (§7) are implemented:
`locateMedia` (`internal/build/media.go`) matches each declared
`[[source.media]]` entry to a disc image among the collected files (a CUE
sheet, or a bare `.iso` as one data track; a `WAVE` file's tracks hash only
its PCM `data` chunk) by track layout plus every verifiable hash. It works
over the `fileSet`, so `build` runs it on the source and `repack` on the
archive's own entries. Tracks in lossy files (Ogg) can't reproduce their
hash and are skipped with a warning (`cue.ResolvedTrack.Verifiable`). A
media nothing matches fails the build only if `[[extract]]` references it,
otherwise it's a warning naming any same-layout, different-hash images.
`[[extract]]`'s `from` path is then resolved through `internal/iso9660`
against the matched media's data track (materialized to a temp file,
kept until the archive is written: an extracted file is a byte range of
it, `rangeEntry`, never read into memory - a whole-disc extract of
Descent II peaked at 268 MB, down from 773 MB).

`repack` (`internal/repack`) rebuilds an archive from nothing but a
manifest - no source directory, since none is available. By default that
manifest is the one already embedded in the archive; `--manifest` supplies
a different one instead (an edited copy, or one retrofitted onto an
archive with no embedded manifest at all - the input no longer needs a
`DOSZ.TOML` of its own). The archive's own entries stand in for §6.1's
collect step (extract doesn't apply - whatever it produced is already in
the archive - but the archive's disc images are verified against
`[[source.media]]`), then §6.2 onward runs
exactly as it does for `build`: `build.Run` and `build.Repack` now share a
`runPipeline` helper, so trim, flatten, rewrite and §6.6 text normalization
genuinely apply, not just the runtime-file regeneration. This matters for
retrofitting a manifest onto a legacy (non-doszer) archive, not only for
re-packing one doszer already built - an earlier version of this command
only regenerated the runtime files, silently skipping
layout.trim/rewrite/§6.6 against a freshly inserted manifest, which a real
retrofit attempt caught. It also canonicalizes an archive that didn't
conform to §8's determinism to begin with, since output always goes
through the same `internal/zipw` writer `build` uses.

An entry the pipeline never reads or changes, and whose store decision
(§8.1) still matches the compression method it already had, is copied
through in its already-compressed form (`build.RawSource`,
`zipw.Writer.AddRaw`, `archive/zip`'s `OpenRaw`/`CreateRaw`) instead of
being decompressed and recompressed - repack's main cost on an archive
most of whose content a given run leaves untouched. Measured on a real
1.7 GB, 1246-entry archive (stored, uncompressed disc images): 57s when a
test manifest's `pack.store` didn't match the input's actual stored
extension (every entry recompressed, since the store decision changed),
7.5s once it did (nearly all entries passed through raw). A passed-through
entry is written exactly as `zipw.Add` would write it fresh, so repacking
a doszer-built archive is byte-for-byte identical: checked on four real
archives (a legacy archive's first repack canonicalizes it; the second
reproduces the first).

`renameMediaCue` (`internal/build/medianame.go`) is the part of §6.4 this
implements now: a CUE sheet whose track layout (audio/data sequence, read
directly from its text - not re-hashed) matches a declared
`[[source.media]]` entry is renamed to `<name>.cue` (or `<name> (<id>).cue`
if more than one matches), updating `boot.disc` to match if it referenced
the old name. It runs for both `build` and `repack`, since a legacy
archive's over-long, dump-specific CUE name (e.g. a Redump filename with
region/version tags) is exactly what a retrofit needs fixed, not only a
fresh build. It keeps the sheet's directory, since its `FILE` names are
relative to it.

`convertMedia` (`internal/build/convert.go`) is the rest of §6.4, run
right after `renameMediaCue`. It plans every CUE sheet whose files are
all in the build (`cue.ResolveWith` against the fileSet), then runs the
conversions in parallel and applies them. Each converted track's output is
exactly its §7.2 normalized content (`cue.ResolvedTrack.Normalize`), so a
WAV/ISO build re-hashes to the manifest's own track hashes. A sheet already
in converted form plans to nothing, which is both what makes repack
idempotent and how `verify` checks §6.4 (`UnconvertedMedia`: "would §6.4
change this sheet?"). A 2048-byte data track that is already its whole
file is reused as the same `fileEntry`, so repack can still pass it through
raw. Entries are streamed through `fileEntry.openRange` (`RawSource.Open`
for an archive entry), never loaded whole: repacking a deflated 700 MB
disc image peaked at 19 MB RSS. Measured on a real 9-track disc
(418 MB data, 8 audio tracks): 30s to build with Ogg, most of it Deflate
on the ISO.

## Next steps

1. Finish the live `install` test against real DOSBox Pure. Passed
   (2026-11-13): a plain session (Descent II, 32 files from the
   `.pure.zip`), and an OS image session with `--drive c` on a fixed
   Win98 VHD (Betrayal in Antara: 1233 of 1234 files identical to a
   known-good install, `RESOURCE.WIN` differing only in setup choices;
   the image left byte-identical). Earlier runs on a dynamic VHD hit
   DOSBox Pure's lost-write bug (DOSBox Pure facts table), which
   `install` now refuses. Still unchecked live: a D: extraction, `--os`
   with a second image, and that `FRONTEND.DBP`'s override takes effect
   on its own (that machine's `DOSBoxPure.cfg` already sets
   `bootos_ramdisk` to `diff`).
2. Specification §12's OS image startup (README "Windows 9x OS image").
   Passed (2026-11-14) with the Betrayal in Antara example on the Win98
   image, under `diff`: `WINDOWS.BAT`'s `QUICKRES_RUNDLLENTRY 640x480x8`
   switched modes without a restart prompt (no 256-colour warning), the
   game started maximized without a title bar, and quitting it shut
   Windows down and closed DOSBox Pure. It also ran on a Steam Machine
   under RetroDECK. Without Explorer as the shell, opening Control Panel
   shows a harmless "Cannot find the file" error with a garbled name;
   open the applets directly (`control desk.cpl`).
3. Gamepad mappings. Games need controller bindings to be playable on a
   gamepad-only device (e.g. a Steam Machine). DOSBox Pure reads
   `PADMAP.DBP` from the content root (hidden from D:, see the facts
   table), so an archive could ship a per-game mapping, as it ships
   `WINDOWS.BAT`. Not yet researched: its format, where DOSBox Pure
   writes it when a mapping is changed in its menu (content or save),
   and whether it belongs in the specification (a §6.8 runtime file or
   a `[[file]]`) or only the README. Verify against the source first.

`internal/config` is implemented (README "Configuration"); `build` and
`repack` consume `ogg_quality`, `install` the rest. With the OS image
session using "Save Difference Per Content", `templates` only matter for
`install --from` a whole `.vhd`/`.img`.

## Resolved questions

Raised by a real retrofit attempt (repacking a legacy archive):

- **§8.2's fixed 1980-01-01 timestamp discarded original file dates.**
  Fixed: §8.2 now requires a collected or extracted file's own
  modification time (the source directory file's mtime, or the date
  recorded in the media's file system for an extracted one); only the
  three generated files (`AUTOBOOT.DBP`, the parent marker, `DOSZ.TOML`)
  use the fixed epoch, since they have no source of their own.
  `internal/zipw.Writer.Add` takes the modification time per entry now,
  converting it to MS-DOS date/time fields directly (never
  `FileHeader.Modified`, for the same extra-field reason as before) and
  clamping to the representable 1980-2107 range. `fileEntry` carries this
  through `internal/build`'s pipeline - `rewrite`/`normalizeText` preserve
  it when they change content, `collect` reads it from `os.DirEntry.Info`,
  `internal/iso9660` parses it from each Directory Record's recording
  date/time field (ECMA-119 §9.1.5), and `repack` carries forward whatever
  was already on the input archive's own entries. `verify` checks the
  epoch only on the three generated files, not on an ordinary entry, since
  it has no independent way to know what an arbitrary file's timestamp
  should be.
- **Deflate trails a multi-pass encoder (e.g. 7-Zip) by about 6%**,
  even at `flate.BestCompression`. Investigated going further and decided
  against it, for now:
  - A pure-Go zopfli port (`foobaz/go-zopfli`) exists but has no stated
    license, no dated commits or releases, and reports ~100 kB/s
    throughput - for a game's largest files (a data track can be
    hundreds of MB), that's minutes added to every build, not a one-time
    release cost.
  - `klauspost/compress/flate` is a maintained, permissively-licensed,
    widely-adopted drop-in, but its own docs note that "the size of
    actual Write calls will affect output size" - a real determinism
    risk for a spec whose own §8.2/§9 exist specifically to guarantee
    reproducible output - for a reportedly modest, data-dependent ratio
    gain, not a zopfli-level fix.
  - Shelling out to a second external binary (7z or similar) would
    contradict this file's own "only ffmpeg" convention above; that's a
    bigger, deliberate convention change, not a silent addition.

  Measured on 2026-11-13 (`klauspost/compress` v1.20.4, level 9 vs
  `compress/flate` level 9, every deflated entry of three real repacked
  archives; 7-Zip `-tzip -mx=9 -mm=Deflate` for scale):

  | Data | stdlib | klauspost | 7-Zip |
  |---|---|---|---|
  | Descent II, 672 MB (mostly the ISO) | 472.5 MB, 46s | 468.9 MB (-0.75%), 28s | 442.8 MB (-6.3%), 142s |
  | Betrayal at Krondor, 127 MB | 90.0 MB, 3.6s | 89.7 MB (-0.35%), 2.3s | |
  | Betrayal in Antara files, 68 MB | 17.06 MB, 4.2s | 17.06 MB (0%), 2.7s | |

  `klauspost`'s output never changed with write sizes (whole buffer,
  32 KiB, 4 KiB and random 1 B-100 KiB writes, on all 1302 entries), so
  the determinism caveat didn't show here. But it closes about a tenth
  of the gap to 7-Zip: not worth a dependency for size. Its ~40% speed
  gain is the only real argument for it. Closing the gap needs a
  multi-pass encoder, which none of the options above provide
  responsibly.

## Reference

A Bash predecessor, `dosz.sh`, implemented `audio`, `extract` and `pack`.
Behaviour worth preserving from it:

- Single-BIN CUE splitting drops INDEX 00 pregap audio; compensate with a
  `PREGAP` equal to INDEX 01 minus INDEX 00 so absolute track positions are
  unchanged. Doesn't apply to `internal/cue`: it reads each track directly
  from its resolved byte range rather than physically splitting the BIN, so
  there's no shared absolute addressing to preserve.
- Raw CD-DA in BIN files is 16-bit little-endian (`internal/cue` copies it
  unchanged; no byte-swap needed for WAV, which is also little-endian).
- Some ISO 9660 images have directory records that strict parsers reject;
  parse leniently (`internal/iso9660` skips a malformed record and keeps
  going rather than failing the whole read).
