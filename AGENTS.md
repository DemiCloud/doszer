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
spec/MANIFEST.md     The specification (normative)
README.md            doszer usage, configuration, install workflow
```

Planned packages: `internal/cue` (CUE parsing, track splitting),
`internal/iso9660`, `internal/vhd` and `internal/fat` (disk image reading for
`install`).

## Conventions

- Go 1.24+. Code MUST pass `gofmt`, `go vet` and `go test ./...`.
- Standard library first. Add a dependency only when reimplementing it would
  be substantial; current dependencies: `github.com/pelletier/go-toml/v2`.
- External programs: only `ffmpeg`, for Ogg Vorbis encoding (no maintained
  pure-Go encoder). Invoke it with `-fflags +bitexact`.
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
| `build` | Implemented except `[[extract]]` and media processing (§6.4) |
| `install` | Not implemented |
| `repack` | Not implemented |
| `verify` | Not implemented |

Manifest parsing and every manifest rule in specification §11 are
implemented and tested. `build` implements §6 in full except `[[extract]]`
(§6.1) and media conversion (§6.4), which it rejects with a clear error, and
enforces the archive-content rules of §11 (shell-owned files, `DOSBOX.BAT`).
Those rules still apply independently to `verify`, once implemented, for
archives built by other implementations.

## Next steps

1. Media processing (`internal/cue`, `internal/iso9660`): CUE parsing, track
   splitting, ISO extraction, normalized track hashes (§7), Ogg/WAV output,
   then `[[extract]]` support in `build`.
2. `repack` and `verify`.
3. `install`: VHD (dynamic and fixed) and FAT32 reading, template diffing,
   `.pure.zip` extraction, launching DOSBox Pure, consuming
   `internal/config`'s `SystemDir`/`SaveDir`/`DosboxPure`/`Templates`.

`internal/config` is implemented (README "Configuration") but has no
consumer yet: no command currently needs `system_dir`, `save_dir`,
`dosbox_pure` or `templates`.

## Reference

A Bash predecessor, `dosz.sh`, implemented `audio`, `extract` and `pack`.
Behaviour worth preserving from it:

- Single-BIN CUE splitting drops INDEX 00 pregap audio; compensate with a
  `PREGAP` equal to INDEX 01 minus INDEX 00 so absolute track positions are
  unchanged.
- Raw CD-DA in BIN files is 16-bit little-endian.
- Some ISO 9660 images have directory records that strict parsers reject;
  parse leniently.
