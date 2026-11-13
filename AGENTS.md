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
Makefile             build/test/vet/install/release (see `make help`)
spec/MANIFEST.md     The specification (normative)
README.md            doszer usage, configuration, install workflow
```

Planned packages: `internal/vhd` and `internal/fat` (disk image reading for
`install`).

## Conventions

- Go 1.24+. Code MUST pass `gofmt`, `go vet` and `go test ./...` (`make vet`,
  `make test`); `make build` for a local binary, `make help` for every
  target.
- Standard library first. Add a dependency only when reimplementing it would
  be substantial; current dependencies: `github.com/pelletier/go-toml/v2`.
- External programs: only `ffmpeg`, for Ogg Vorbis encoding (no maintained
  pure-Go encoder). Invoke it with `-fflags +bitexact`.
- `internal/zipw` registers its own Deflate compressor at
  `flate.BestCompression` (level 9), scoped to its own `*zip.Writer`:
  `archive/zip`'s built-in one uses level 5 (`register.go`'s
  `newFlateWriter`), not even `flate.DefaultCompression` (6). Still weaker
  than a multi-pass encoder like 7-Zip's; that gap is a known, open
  tradeoff (see Reference), not a bug.
- Manifest fields use `omitempty` (`internal/manifest/manifest.go`) so a
  re-serialized manifest (`internal/build`'s `effectiveManifest`, used via
  `addRuntimeFiles` by both `build` and `repack`) doesn't emit every unset
  field as noise (`exec = ''`, `extract = []`, etc.) - confirmed against
  go-toml/v2 to omit a whole empty table, not just an empty scalar, before
  relying on it.
- Errors: wrap with context, return to `main`, which prints
  `[YYYY-MM-DD HH:MM:SS] ERROR: <message>` to stderr and exits 1. Validation
  collects every error and reports them together (`errors.Join`).
- Commands and flags follow the patterns in `cmd/doszer/main.go`: one
  `flag.FlagSet` per command, flags accepted before or after positional
  arguments.
- Every configuration setting is also available as a flag. Precedence:
  flags, `<source>/DOSZ.TOML`, `./doszer.toml`,
  `$XDG_CONFIG_HOME/doszer/config.toml`, defaults.
- Tests are table-driven. Build test fixtures (disc images, ZIPs, disk images)
  programmatically in tests; do not commit binary game data.

## DOSBox Pure facts

Verified against DOSBox Pure 1.0 Preview 8 source.

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

## Status

| Command | State |
|---|---|
| `init` | Implemented |
| `build` | Implemented except whole-disc-image conversion (§6.4) |
| `install` | Not implemented |
| `repack` | Implemented |
| `verify` | Implemented |

Manifest parsing and every manifest rule in specification §11 are
implemented and tested. `build` implements §6 in full except media
conversion (§6.4: converting a disc image already sitting in the build to
ISO/Ogg/WAV with a rewritten CUE, independent of `[[extract]]`), and
enforces the archive-content rules of §11 (shell-owned files, `DOSBOX.BAT`).
`verify` (`internal/verify`) applies those same rules, plus §8's
determinism checks, §6.6's text normalization, §6.7's exclusions, and
§6.8's runtime files - checking their *content* against what the embedded
manifest implies (e.g. AUTOBOOT.DBP's bytes must match
`build.BuildAutoboot(&m.Boot)` exactly, not just exist), to an already-built
archive and its embedded manifest - including one not built by doszer.
`build.ValidateArchiveContent`, `build.ShouldStore`, `build.IsTextFile`,
`build.NormalizeCRLF`, `build.IsExcluded`, `build.NeedsAutoboot` and
`build.BuildAutoboot` are exported from `internal/build` specifically so
`verify` (and `repack`) can reuse them rather than duplicate the rules.

`[[extract]]` and media identification (§7) are implemented: `build` finds
the CUE sheet in the source tree matching each declared `[[source.media]]`
entry by hashing its tracks (`internal/build/media.go`), rejecting the build
if none matches or a hash differs, then resolves `[[extract]]`'s `from`
path through `internal/iso9660` against the matched media's data track
(materialized to a temp file via `internal/cue`).

`repack` (`internal/repack`) rebuilds an archive from nothing but its own
embedded manifest - no source directory, since none is available. The
archive's own entries stand in for §6.1's collect step (extract and media
identification don't apply - whatever they'd have produced is already in
the archive), then §6.2 onward runs exactly as it does for `build`:
`build.Run` and `build.Repack` now share a `runPipeline` helper, so trim,
flatten, rewrite and §6.6 text normalization genuinely apply, not just the
runtime-file regeneration. This matters for retrofitting a manifest onto a
legacy (non-doszer) archive, not only for re-packing one doszer already
built - an earlier version of this command only regenerated the runtime
files, silently skipping layout.trim/rewrite/§6.6 against a freshly
inserted manifest, which a real retrofit attempt caught. It also
canonicalizes an archive that didn't conform to §8's determinism to begin
with, since output always goes through the same `internal/zipw` writer
`build` uses.

## Next steps

1. Media conversion (§6.4): ISO/Ogg/WAV output and rewritten CUE for a disc
   image already sitting in the build. The Ogg/WAV CUE `FILE` type keyword
   DOSBox Pure's `cdrom_image.cpp` expects needs verifying against its
   source before writing this (ground rule: verify, don't assume).
2. `install`: VHD (dynamic and fixed) and FAT32 reading, template diffing,
   `.pure.zip` extraction, launching DOSBox Pure, consuming
   `internal/config`'s `SystemDir`/`SaveDir`/`DosboxPure`/`Templates`.

`internal/config` is implemented (README "Configuration") but has no
consumer yet: no command currently needs `system_dir`, `save_dir`,
`dosbox_pure` or `templates`.

## Open questions

Raised by a real retrofit attempt (repacking a legacy archive); not yet
decided, so not yet acted on:

- **§8.2's fixed 1980-01-01 timestamp discards original file dates.**
  A collected/extracted file's real mtime (e.g. a 1993 game's) is lost;
  only the three generated runtime files (`AUTOBOOT.DBP`, the parent
  marker, `DOSZ.TOML`) arguably need a fixed timestamp for determinism,
  since §9 equivalence is defined over uncompressed content only anyway -
  timestamps were never part of it. Changing this is a spec change
  (ground rule: `spec/MANIFEST.md` first), not just a code change, and
  needs its own reasoning about whether a per-file source timestamp is
  itself deterministic enough to rely on (e.g. after a checkout or a
  re-extraction from media) before `§8.2` is reworded.
- **Deflate still trails a multi-pass encoder (e.g. 7-Zip) by a few
  percent**, even at `flate.BestCompression` (see Conventions). Spec §9
  doesn't require closing this gap (equivalence is over uncompressed
  content), so it's a quality tradeoff, not a bug. Options if it's worth
  pursuing: a stronger pure-Go encoder (e.g. a zopfli port - still
  standard, deterministic Deflate, just a slower search), or a pluggable
  compressor setting. Either is a new dependency or a user-facing option,
  so it wants a decision, not a silent pick.

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
