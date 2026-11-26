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
internal/icon/       DOSZ.ICO (spec §6.8.4): ICO file or NE/PE icon group to ICO; ICO decoding (PNG, DIB) for the window
internal/config/     configuration layering (README "Configuration": [paths] roms/system/saves, [defaults], [build], [install.os], [install.templates]; keys dotted), portable mode, default directories (system, saves, ROMs - several) and RetroDECK's
internal/cue/        CUE parsing, track byte ranges, normalized track hash (§7.2)
internal/iso9660/    ISO 9660 reader, for [[extract]] (§5.7)
internal/padmap/     [[input.pad]] (§5.9): PADMAP.DBP and DOS.YML encoding (§6.8.2, §6.8.3)
internal/verify/     doszer verify: an archive against its manifest and spec §8/§11
internal/platform/   AUTORUN.INF/program icon suggestion for init; what a program needs (MZ/NE/PE headers, NE expected and PE subsystem versions), a game's programs together (installed over disc, helpers aside, DOS versions), source scanning incl. disc images; for info and init
internal/env/        doszer env: this machine's config files, system/save directories and where they came from, shells and images (fixed/dynamic), configured names, Redump datfiles, ffmpeg
internal/info/       doszer info: an archive's hashes, boot, parents, programs by platform (MZ/NE/PE headers), compression, discs
internal/repack/     doszer repack: rebuild an archive from its own embedded manifest; doszer set: change boot settings in place (build.Refresh)
internal/prepare/    doszer prepare: game-ready copy of a Windows 3.x shell (RunExit) or Windows 9x image (STARTRUN.VBS, PRESSKEY, Program Manager .GRP writer, line-preserving INI editor)
internal/vhd/        VHD reader (fixed, dynamic), for install
internal/w9xreg/     Windows 9x registry (SYSTEM.DAT/USER.DAT) reader, diff, REGEDIT4 writer, for install
internal/fat/        FAT12/16/32 reader with long names, as an io/fs.FS, for install; Tree/Write rebuild a volume in its own geometry, for prepare os
internal/vdisk/      DOSBox Pure's synthesized D: disk + .sav diffs (D: and -CDRIVE), for install
internal/install/    doszer install and run: session content, launching the built-in DOSBox Pure, directory discovery, diffing, extraction
internal/chain/      an archive's dependencies as DOSBox Pure resolves them: parent markers, the AUTOBOOT.DBP in effect (the nearest), the shell/OS image in the system directory; reverse (UsedBy); for deps, library, the window
internal/library/    the window's game list: .dosz archives under the ROM directory (name, boot, DOSZ.ICO), play time (playtime.json)
internal/gui/        doszer gui: the SDL-drawn window: Games (list/grid, search, sort; games run as `doszer run` child processes), Systems, Settings, the first-run dialog, the menu bar (menubar.go), Add a game (addgame.go: dump/dump --from, init, build as one step run); widgets.go (fields, buttons, dropdowns, layers); os_*.go (Explorer/xdg-open, folder picker); TestScreenshot renders every page to PNGs (also as the Windows test binary through WSL interop)
internal/libretro/   built-in frontend: hosts a libretro core in-process (environment, directory-only VFS, SDL window/input/audio)
internal/sdl/        the few SDL2 functions internal/libretro uses, loaded at run time through purego
internal/dl/         dlopen/LoadLibrary without cgo
internal/builtin/    programs embedded at build time, extracted to the user cache: the DOSBox Pure core (and SDL DLLs on Windows, `make core`), redumper (`make redumper`); guest programs, the same on every platform: RunExit, PRESSKEY (`make guest`)
internal/dump/       doszer dump: a CD to BIN/CUE with redumper (Windows redumper through interop under WSL), resumable; or --from an existing image (Import)
internal/redump/     Redump datfile: load (Logiqx XML/zip, compact), embedded snapshot (`make dat`), cached run-time copy, track matching; any CUE image (or zip of one) read in Redump's track layout, read-offset correction; Mode 1 and Mode 2 Form 1 sector rebuild (EDC/ECC, sector.go; XA subheaders predicted from the ISO 9660 file system, xa.go) so an archive's ISO tracks hash as Redump's raw ones, both ways in one read (ArchiveImages, for redump and verify)
internal/interrupt/  removes registered temporary paths on SIGINT/SIGTERM
internal/status/     terminal progress: current step with a spinner, finished steps
internal/output/     --format (romctl's names): fancy/plain text, or a result as JSON/YAML/TOML/CSV
assets/              the icon (doszer.svg; doszer-small.svg for 16/24 px), its .ico; `make icon` renders it into cmd/doszer's .syso and internal/libretro/icon.png
examples/            complete example manifests (checked by internal/manifest's TestExamples)
Makefile             build/test/vet/install/release (see `make help`)
spec/MANIFEST.md     The specification (normative)
README.md            doszer usage, configuration, install workflow
```


## Conventions

- Go 1.24+. Code MUST pass `gofmt`, `go vet` and `go test ./...` (`make vet`,
  `make test`); `make build` for a local binary, `make help` for every
  target.
- Standard library first. Add a dependency when it's needed and the
  simplest good solution, not to save a little code; no unneeded
  dependencies. Current: `github.com/pelletier/go-toml/v2`,
  `github.com/spf13/pflag` (GNU-style options, the same library the
  sibling `romctl` uses through cobra - five commands don't need cobra),
  `golang.org/x/image` (the window's text: the opentype rasterizer and
  the Go fonts, BSD; v0.36.0, the last needing only Go 1.24, which
  brings `golang.org/x/text` for `sfnt`'s legacy cmaps),
  `github.com/ebitengine/purego` (loading the DOSBox Pure core and SDL2
  without cgo, so every release target still cross-compiles with
  `CGO_ENABLED=0`; v0.10.0, the last needing only Go 1.18 - v0.11 needs
  Go 1.25), `go.yaml.in/yaml/v3` (`--format yaml`; the YAML
  organization's maintained continuation of `gopkg.in/yaml.v3`, which
  `romctl` still uses).
- The built-in frontend embeds DOSBox Pure, built from a pinned release
  (`make core`, `DBP_REF` in the Makefile; `internal/builtin`'s
  `DOSBoxPureVersion` must match; `doszer version` lists it, redumper
  and the Windows SDL DLLs, read from their version resource, via
  `builtin.Programs`), for linux/amd64 and linux/arm64 (zig
  `c++ -target <arch>-linux-gnu.2.28`: static C++ runtime, glibc 2.27+
  as measured, any host) and windows/amd64 (MinGW-w64, plus
  sdl2-compat's `SDL2.dll` and `SDL3.dll`). No macOS support (dropped
  from releases, revisit if someone can build and test a core with
  Apple's SDK). The arm64 core is untested on hardware.
- Until doszer is set up (`config.Configured`: a config file exists), it
  writes no data of its own: `run` saves into a temporary folder removed
  after the session, the window keeps play time in memory. Caches (the
  extracted core, Redump's datfile) are not data and are kept. The
  window's first run offers portable, user profile or neither (the
  model of the sibling net-scope's config location dialog).
- `install` and `run` run only the built-in frontend. RetroArch and
  standalone DOSBox Pure support was removed: driving an external
  frontend meant writing its configuration and option files and guessing
  at its directories. doszer reads no frontend's configuration, with one
  exception: RetroDECK's `retrodeck.json` (`paths.bios_path`, `roms_path`,
  `saves_path`), read-only, as the last fallback for the system directory
  (after `paths.system`, `./system` and `system/` beside the executable),
  for the ROM directory (after `paths.roms`, `./roms`, `roms/` beside it) and
  for `run`'s save directory (after `paths.saves`, `./saves` and `saves/`
  beside the executable): `<saves_path>/<archive's folder>` for an archive
  under `roms_path`, matching RetroArch's sort-by-content-directory, so
  `run` shares saves with RetroDECK. `install` never searches: its
  session saves are throwaway. The staged
  binaries are gitignored; each platform's `MANIFEST` placeholder stays
  tracked so `go:embed` always has a directory. Upgrading DOSBox Pure
  means re-checking the DOSBox Pure facts below against the new source.
- Embedded programs (internal/builtin, run as separate processes):
  redumper for `dump`, its official release binaries pinned by tag and
  SHA-256 (`REDUMPER_REF` in the Makefile, `RedumperVersion` must match).
  It's always run with `--auto-detect` (sector order detection, which
  only acts on generic drives - e.g. a drive whose firmware revision
  isn't in redumper's database, matched exactly, `drive.ixx`
  `drive_find_in_database`) and from its work directory with relative
  paths, so nothing needs translating for the Windows build under WSL.
- Embedded guest programs (`internal/builtin/data/guest`, run inside
  DOSBox Pure, not on the host; `make guest`): RunExit for `prepare
  shell` (16-bit, Windows 3.x) and PRESSKEY for `prepare os` (32-bit,
  Windows 95+; no place in a 3.x shell), each official release binary
  and licence pinned by tag and SHA-256 (`RUNEXIT_REF`/`PRESSKEY_REF`;
  `RunExitVersion`/`PresskeyVersion` must match).
- Redump's PC datfile is embedded as a snapshot (`make dat`, gitignored,
  converted to a compact gzipped size/SHA-1 list: 5 MB against Redump's
  14 MB zip) and refreshed at run time (weekly, cached). It isn't
  committed: Redump states no redistribution terms.
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
  byte for byte, the archive comment included (spec §8.4: name, doszer
  version and homepage, `build.Provenance`; no date, so one version's
  builds of one source are the same file). Still
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
  before or after arguments, `--` ending them. `newFlags` gives every
  command `--format` (`setFormat`): a command with a result
  (`dataCommands`) encodes it under a structured format, through a
  struct tagged alike for json, yaml and toml. Help is hand-written,
  man-page brief, uppercase placeholders (`SOURCE`, `FILE`), shared option
  lines in `flag*` constants.
- Every configuration setting is also available as a flag. Precedence:
  flags, `<source>/DOSZ.TOML`, `./doszer.toml`,
  `$XDG_CONFIG_HOME/doszer/config.toml`, defaults.
- Tests are table-driven. Build test fixtures (disc images, ZIPs, disk images)
  programmatically in tests; do not commit binary game data.
- Commit every big change - a fix, feature or refactor, with its spec,
  tests and docs - as soon as it is done and `make vet` and `make test`
  pass, without waiting to be asked. Commit to `main`, one commit per
  change: a subject line saying what changed, then a body saying why,
  citing the DOSBox Pure source where behaviour depends on it.

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
| Parent marker | Zero-byte root entry `<name>.parent`; parent resolved in the archive's directory; chains allowed. Two or more markers: the archive is refused ("DOSZ file has multiple parents"); a non-empty `.parent` file is ordinary content | `src/dos/drive_zip.cpp`, `zipDriveImpl`, `Open` |
| `DOSBOX.BAT` | Run instead of the start menu; bypasses `AUTOBOOT.DBP` | `dosbox_pure_libretro.cpp` |
| Shell startup | `WINDOWS.BAT`, then `AUTOEXEC.BAT`, then `WINDOWS\WIN.COM` (the first that exists, only it: a `WINDOWS.BAT` sets `PATH` itself). When it ends, `PUREMENU -FINISH` closes the core under `menu_time` `0` with an `AUTOBOOT.DBP` startup mode | `dosbox_pure_run.h`, `RunShell`, `BatchFileExec`; `dosbox_pure_osd.h`, `Menu::Run` |
| OS image model | Archive content is a synthesized FAT disk (D:); CD images appear as E: | `src/ints/bios_disk.cpp`, `fatFromDOSDrive` |
| Save files | C: changes for ZIP content: `<save dir>/<content>.pure.zip`. OS image C: diff: `<content>-CDRIVE.sav`. D: disk: `<content>-<HASH>.sav` | `DBP_GetSaveFile` |
| CD audio in CUE | PCM WAV and Ogg Vorbis only (`stb_vorbis`) | `src/dos/cdrom_image.cpp` |
| Large deflated files | Random access requires full decompression once; an index is cached in the save | README |
| `dosbox_pure_bootos_ramdisk` | `"false"` Keep (default: C: writes go into the OS image), `"true"` Discard, `"diff"` Save Difference Per Content. Only affects C:. Under `true` and `diff` the image is opened read-only (`MountOSIMG` with `needwritable` false); `true` keeps C: writes in memory for the session only (`imageDisk::Write_AbsoluteSector`'s `discard` branch stores every write, so dynamic VHDs are safe), `diff` keeps them per content, accumulating - including Windows' unclean-shutdown marker from a session closed while it ran, which makes every later boot offer Safe Mode. Authoring images uses Keep; spec §12's `true` is the production setting (e.g. RetroDECK) and `run`'s; `install` uses `diff` for its one-off session, to read C: back | `core_options.h`; `dosbox_pure_run.h`, `RunBoot`; `src/ints/bios_disk.cpp` |
| D: changes (OS image model) | Saved to `<content>-<HASH>.sav` under every ramdisk setting; only `dosbox_pure_bootos_dfreespace` = `discard`/`hide` disables it | `dosbox_pure_run.h`, `RunBoot`; `DBP_GetSaveFile(SFT_VIRTUALDISK)` |
| `diff` on a dynamic VHD | Loses writes: `Write_AbsoluteSector` compares a written sector against the image *file's* bytes at `sector*512` (not the VHD's sector) to decide if it changed; a match (typically an all-zero sector where the file holds zeros) is not saved, or frees a saved one, and reads back as the VHD's old content. Raw `.img` and fixed VHDs are unaffected (sector N at `N*512`). Seen live: install sessions on a dynamic Win98 VHD lost files' zero-padded last sectors and corrupted directory/FAT sectors; `install` refuses a dynamic VHD | `src/ints/bios_disk.cpp`, `imageDisk::Write_AbsoluteSector` |
| `.sav` disk diff format | `FFDD\x01`, then records of a little-endian u32 sector number + 512 bytes; sector `0xFFFFFFFF` is a freed slot. Used for D: (over the disk `fatFromDOSDrive` synthesizes from the content) and for C: under `diff` (`<content>-CDRIVE.sav`, over the OS image) | `src/ints/bios_disk.cpp`, `differencingDisk`, `imageDisk::SetDifferencingDisk` |
| Synthesized D: disk | `fatFromDOSDrive` builds a FAT disk from the content's DOS view: directory order is `zipDrive`'s hash map slot order (`BaseHashMap`, FNV-1 over sign-extended chars), short names come from `DBP_Make8dot3FileName`/`IncrementName` and are then regenerated Windows-style (`~N`) for long-named entries. Root holds the volume label (`Set_Label` form of `<name, ≤7 chars>-<ext>`). Hidden from D: `AUTOBOOT.DBP`, `PADMAP.DBP`, `FRONTEND.DBP`, `DOSBOX.CON`, `*.SKC`, and mountable images (ISO/CHD/CUE, validated IMG/IMA/VHD/JRC) except one sharing a CUE's name | `src/ints/bios_disk.cpp`, `fatFromDOSDrive`; `src/dos/drive_zip.cpp`; `dosbox_pure_libretro.cpp`, `DBP_GetSaveFile`, `init_dosbox_parse_drives` |
| D: save hash | The `<HASH>` in `<content>-<HASH>.sav` is a CRC32 chained over every unhidden path, size, date, time and attributes (seeded with `dfreespace`); it is also the disk serial, so the name identifies exactly which content layout the diff belongs to | `DBP_GetSaveFile(SFT_VIRTUALDISK)` |
| Per-content options | Standalone only (`#ifdef DBP_STANDALONE`): applies a `FRONTEND.DBP` JSON object in the content root as option overrides (and reboots to apply them); hidden from D: in both builds. The libretro core ignores it | `dosbox_pure_libretro.cpp`, `init_dosbox`, `DBPS_ApplyConfigOverrides` |
| libretro identity | `library_name` `DOSBox-pure` (RetroArch's per-core save/config subfolder), `need_fullpath`; saves go in the frontend's save directory (`RETRO_ENVIRONMENT_GET_SAVE_DIRECTORY`), OS images and shells in its system directory | `dosbox_pure_libretro.cpp`, `retro_get_system_info`, `DBP_GetSaveFile` |
| Unchanged files in `.pure.zip` | A file written with content identical to the original (same size and bytes) is left out of the save, so rewriting a file unchanged is no change | `src/dos/drive_union.cpp`, save loop ("If content matches, don't store in save file") |
| `.pure.zip` save | ZIP of changed/added files by DOS path, plus `FILEMODS.DBP` lines `DELETE|path`, `REDIRECTFILE|target|source`, `REDIRECTDIR|target|source` | `src/dos/drive_union.cpp`, `Union_Modification`, `ReadSaveFile` |
| VHD support | Fixed and dynamic only (a parent name is rejected); a dynamic block sector whose bitmap bit is clear reads as zeros even if the file holds bytes there | `src/ints/bios_disk.cpp`, `sparseVhd::Init`, `sparseVhd::SeeBlock` |
| CUE `FILE` type | `BINARY` reads raw sectors; any other type opens an `AudioFile`, which detects RIFF/WAVE (16-bit PCM only) or OggS from the file's own header (`C_SDL_SOUND` is undefined in `include/config.h`) | `src/dos/cdrom_image.cpp`, `LoadCueSheet`, `AudioFile::AudioFile` |
| CUE `PREGAP` | Adds to the track's start without reading data; an INDEX 00 span in a track's own FILE is skipped and *not* counted toward position | `src/dos/cdrom_image.cpp`, `AddTrack` |
| BIOS power-off | Windows' shutdown powering off (APM) calls `DBP_OnBIOSPoweroff`: the core resets and runs `PUREMENU -FINISH`, which closes it under `menu_time` `0` | `dosbox_pure_libretro.cpp`, `DBP_OnBIOSPoweroff`, `retro_run` |
| `PADMAP.DBP` | Read from C:'s root at startup (`init_dosbox_parse_drives`), before an OS image boots, so it works under every boot model; hidden from D:. Byte `0`, then per port a binding count and bindings `id \| (n-1)<<6` + action codes (2 per action for stick axes 16-19; wheel items id 20). Applies to ports in "Gamepad Mapper" mode, ahead of every other mapping. The mapper writes it back to C: (the save) on change, and deletes it when every port is back at its default | `dosbox_pure_pad.h`, `DBP_PadMapping::Load`/`Save`/`Apply`/`SetInputDescriptors`, `BindDecoder` |
| Action codes | 1-102 DOSBox `KBD_KEYS` (`DBP_YMLKeyCommands` names); 200-230 `DBP_SpecialMappings` (mouse, joystick, 225 menu, 226 wheel, 227-230 port N while held, which copies port N's binds onto the pad while held) | `dosbox_pure_libretro.cpp`; `dosbox_pure_osd.h`, `DBP_WheelShiftOSD` |
| `DOS.YML` | Read from the content root (not from the save under strict mode); `input_pad_<control>: keys+... Label` and `input_wheel_<n>:` become the "Content Provided Mapping": port 1's default without `PADMAP.DBP`, replacing the built-in game database, and the source of labels, matched by a binding's exact actions on any port. Applied as a preset, so L3 stays the menu. Not hidden from D:. In releases since 1.0-preview2; undocumented upstream (`keyb2joypad.yml` uses the same keys) | `dosbox_pure_run.h`, `DOSYMLLoader`; `dosbox_pure_pad.h`, `ParseInputYML`, `FindAutoMapButtonLabel`; `src/dos/drive_patch.cpp`, `AppendYML` |
| Controller ports | 8 (`DBP_MAX_PORTS`); standalone puts all in mapper mode, RetroArch only those whose device type is "Use Gamepad Mapper" (the default) | `dosbox_pure_libretro.cpp`, `DBP_PadMapping::SetPortMode` |
| Disc image list | Every ISO/CUE/CHD/... in the content is listed, except an image sharing a CUE's name minus extension (so `<base>.iso` beside `<base>.cue` is hidden) | `dosbox_pure_libretro.cpp`, "Filter image files that have the same name as a cue file" |

## libretro hosting facts

What the built-in frontend (`internal/libretro`) must provide, verified
against DOSBox Pure 1.0 Preview 8 (`dosbox_pure_libretro.cpp`) and its
`libretro-common/include/libretro.h`.

| Topic | Behaviour | Source |
|---|---|---|
| VFS | Needed (version 3) only to list directories: OS image/shell discovery (`DBP_ScanSystem`) and case-insensitive path matching; also `remove`. Files are opened natively unless the path is a `scheme://` URI (`VFSIO::Use`) | `DBP_ScanSystem`, `VFSIO`, `fpath_nocase` |
| Core options | `DBP_Option::Get` falls back to the option's default when `GET_VARIABLE` returns no value, so a frontend only answers the options it sets | `retro_get_variable`, `DBP_Option::Get` |
| Keyboard | Key events through `SET_KEYBOARD_CALLBACK`'s callback (RETROK codes; repeats ignored by the core); `input_state(RETRO_DEVICE_KEYBOARD)` must report keys held | `CallBacks::keyboard_event`, `DBP_ReleaseKeyEvents` |
| Mouse | `input_state(RETRO_DEVICE_MOUSE)` X/Y as per-poll deltas, buttons through its input binds | `retro_run` |
| Time | Without `GET_PERF_INTERFACE` the core uses its own clock | `time_cb = dbp_cpu_features_get_time_usec` |
| Logging | Without `GET_LOG_INTERFACE` the core `vfprintf`s to stderr | `retro_fallback_log` |
| Pixel format | XRGB8888 (SDL's `RGB888`), software rendered | `retro_load_game`, `SET_PIXEL_FORMAT` |
| `FRONTEND.DBP` | Ignored by the core (standalone only), so the built-in frontend passes core options itself | `init_dosbox`, `#ifdef DBP_STANDALONE` |
| Host cursor | Hidden while the mouse is captured (a transparent SDL cursor, `SDL_ShowCursor` as fallback), shown (the system arrow) while it isn't: released with Ctrl+F10, a hidden cursor left the user unable to see it over the window. Works on Windows; under WSLg the pointer stays visible (WSLg draws it through its RDP client, which ignored both), so test mouse feel with the Windows build | live tests, 2026-11-24/25 |

`internal/libretro`'s `TestProbe` runs real content headless and saves a
screenshot every few seconds (`DOSZER_PROBE_CONTENT`, `_SYSTEM`, `_OUT`,
`_FRAMES`, `_EVERY`; `_RAMDISK` for `bootos_ramdisk`, default `diff`;
`_PACED` to run in real time): how to see what a game does in an OS image
session without a window, e.g. a startup script that leaves the screen
black. It logs the frame at which the core closed the session, if it did.
Use a system directory of symlinks to the images, and `diff` or `true`,
so nothing is written to them. Saves are kept in `<out>/saves`: a startup
batch file can log to C:, read back through `vdisk.NewOverlay` over the
image and the `-CDRIVE.sav`, or to D:, whose `.sav` holds the written
sectors under any ramdisk setting (grep it for a marker).

A Windows 98 DOS window that has shown any output stays open, finished,
when its batch file ends, even hidden: `STARTRUN.VBS` waiting on
`WINDOWS.BAT` then never shuts Windows down. One `ECHO`, or a batch file
without `@ECHO OFF`, was enough (probe, 2026-11-25; silent batch files
closed the session, and Antara's closed it live). `prepare os` runs it as
`COMMAND.COM /C D:\WINDOWS.BAT >NUL`.

Windows 98 ships Windows Script Host 1.0 (`WSHOM.OCX` 5.0), with no
`SendKeys`/`AppActivate`; a startup script calling them fails behind the
hidden `WINDOWS.BAT` console and `START` waits on its unseen error box
forever. To get past a launcher, ship `PRESSKEY.EXE`
(`github.com/DemiCloud/presskey`) in the source and `START` it from
`WINDOWS.BAT` (`%` is `%%` there). Programs run under DOSBox Pure's
emulated CPU must not use CMOV/SSE (zig's default x86 CPU does).

## Status

| Command | State |
|---|---|
| `init` | Implemented; without a boot model it picks one from the programs' needs (internal/platform), which matched the hand-chosen model of all five local test sources |
| `build` | Implemented |
| `install` | Implemented; live plain and OS image (`--drive c`) sessions passed under standalone DOSBox Pure (before its support was removed); built-in frontend checked with the real core (headless test on Linux and on Windows via WSL interop, and a window under WSLg), no full live install yet; `--from` checked on a real D: save (Next steps) |
| `run` | Implemented; not yet played live |
| `repack` | Implemented |
| `set` | Implemented |
| `verify` | Implemented |
| `prepare` | Implemented. `shell`: a prepared real Win3.1 shell ran a program through RunExit and closed DOSBox Pure (headless probe). `os`: a prepared real Win98 image booted to Program Manager's generated Tools group (icons re-extracted) and ran a `WINDOWS.BAT` with PRESSKEY (headless probe); its shutdown didn't close the probe session, as with the hand-made reference image (Next steps) |
| `info` | Implemented |
| `deps` | Implemented; checked on a real parent chain (RetroDECK windows3x: games over BOWEP.shell over the Win31 shell) |
| `env` | Implemented |
| `config` | Implemented |
| `gui` | Implemented: Games, Systems, Settings, first-run dialog; every page rendered on Linux (WSLg) and Windows (TestScreenshot), setup choices tested; launching a game, Browse and saving settings not yet tried live |
| `associate` | Implemented (Windows, HKCU); dry run checked on Windows, not yet applied live |
| `version` | Implemented |

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
`build.BuildAutoboot`, `build.BuildPadmap`, `build.BuildDOSYML` and
`build.MediaDates` (§8.2's disc date: a disc image's files are dated by
its first data track's PVD volume creation date) are
exported from `internal/build` specifically so
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
a doszer-built archive is byte-for-byte identical (up to the archive
comment's doszer version, when repacked by another): checked on four real
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
on the ISO. A deflated archive entry can only be read from its start, so
a multi-track BIN is decompressed once, not once per track: `openRange`
keeps a part-read stream for the next in-order caller (§7.3's hashes),
and `materializeShared` decompresses a BIN that several conversion jobs
share to the build's temp directory first. A synthetic 225 MB, 16-track
deflated BIN/CUE repacked in 3.5s instead of 20.5s, byte-identical
(`TestStreamedSourceDecompressedOnce` counts the bytes read).

Tracks on disk are hashed concurrently (`verifyTracks`, `IdentifyMedia`);
an image in a compressed entry stays in order, for the reason above. For
a media an `[[extract]]` reads, `locateMedia` copies the first data track
to a temp file while hashing it (`teeTarget`), and a data track that is
already 2048-byte sectors on disk is extracted from in place
(`directISO`), so neither is read twice. `verify` reuses the CRC-32
result of every entry `build.ArchiveMedia` read through (`endChecker`
reads one byte past the end, since `archive/zip` checks only at
`io.EOF`), then checks the rest concurrently: a synthetic 297 MB archive
(300 files, two 16-track discs) verified in 2.1s instead of 4.6s.

## Next steps

Items 1-4 need someone at the machine (live sessions, RetroArch, a
Steam Deck); they come first. Item 6 can be done headless now. Item 7
waits on a game that needs it.

1. Play an archive with `doszer run` and run a live `install`: a
   plain CD install, and an OS image session (`bootos_ramdisk = diff`
   comes from `Config.Options`; check the image is untouched and C: lands
   in `-CDRIVE.sav`), on Linux/WSLg and as `doszer.exe` on Windows.
   Check keyboard (scancode table in `internal/libretro/keys.go`), mouse
   capture in Windows 9x, and audio. Checked live (2026-11-24/25, Windows
   build): Betrayal in Antara to its menu with audio and mouse, quitting
   closed doszer; keyboard in Recoil. Not done yet: Ctrl+F10, gamepads,
   fullscreen, fast-forward, a Linux/WSLg session, `install`.
2. Finish the live `install` test against real DOSBox Pure. Passed
   (2026-11-13): a plain session (Descent II, 32 files from the
   `.pure.zip`), and an OS image session with `--drive c` on a fixed
   Win98 VHD (Betrayal in Antara: 1233 of 1234 files identical to a
   known-good install, `RESOURCE.WIN` differing only in setup choices;
   the image left byte-identical). Earlier runs on a dynamic VHD hit
   DOSBox Pure's lost-write bug (DOSBox Pure facts table), which
   `install` now refuses. Still unchecked live: a D: extraction, `--os`
   with a second image.
3. Specification §12's OS image startup (README "Windows 9x OS image").
   Passed (2026-11-14) with the Betrayal in Antara example on the Win98
   image, under `diff`: `WINDOWS.BAT`'s `QUICKRES_RUNDLLENTRY 640x480x8`
   switched modes without a restart prompt (no 256-colour warning), the
   game started maximized without a title bar, and quitting it shut
   Windows down and closed DOSBox Pure. It also ran on a Steam Machine
   under RetroDECK. Without Explorer as the shell, opening Control Panel
   shows a harmless "Cannot find the file" error with a garbled name;
   open the applets directly (`control desk.cpl`).
4. Gamepad mappings (`[[input.pad]]`, spec §5.9, §6.8.2-3): implemented
   and tested against byte layouts derived from the source, not yet live.
   Check in DOSBox Pure (`doszer run`, and RetroArch, ideally on a Steam
   Deck): that a generated `PADMAP.DBP` loads without "Corrupt gamepad
   mapping data", bindings and stick axes behave, `DOS.YML` labels show in
   the Controller Mapper and the action wheel, `menu`/`hold_portN` work,
   and resetting the mapper falls back to the `DOS.YML` mapping. The
   Betrayal in Antara example has a mapping (from `ANTARA.HLP`'s hotkey
   list) to test with, under the OS image model.

5. (Resolved 2026-11-25.) OS image shutdown: `doszer run` closed after
   quitting Betrayal in Antara live (Windows build), and headless probes
   close too. The earlier probes hung because their `WINDOWS.BAT` printed
   output (DOSBox Pure facts, below the table); `prepare os` now
   redirects it.
6. `prepare os` follow-up: a black desktop needs the registry
   (`HKCU\Control Panel\Colors`, `Background`; Windows 98 ignores
   `WIN.INI [colors]`). Now unblocked: a session that applies a `.REG`
   and shuts down closes by itself (item 5), so `prepare os` could boot
   the new image once under Keep, headless, without risking Windows'
   unclean-shutdown marker. Writing `USER.DAT`
   directly would need `w9xreg` to rewrite a real registry, not just
   encode test fixtures. (Done: `install.os` keeps a clean image for
   `install`; `init` picks the boot model and image.) Other registry
   presets would follow the same path.
7. A 16-bit PRESSKEY for Windows 3.x launchers, only once a game needs
   one. Open Watcom v2 builds Win16 programs from Linux and publishes
   dated builds to pin (`2026-11-01-Build`'s `ow-snapshot.tar.xz`, 150
   MB, SHA-256 `89e90b75...bb48`). The hard part is pressing keys:
   Windows 3.1's `USER` exports `KEYBD_EVENT`, but as the keyboard
   driver's register-based callback (no prototype in `win16.h`), and a
   `WH_JOURNALPLAYBACK` hook must live in a DLL; `PostMessage` reaches
   only programs reading their message queue.

`internal/config` is implemented (README "Configuration"); `build` and
`repack` consume `build.ogg_quality`, `install` and `run` the rest. With the OS image
session using "Save Difference Per Content", `install.templates` only matter for
`install --from` a whole `.vhd`/`.img`.

## Resolved questions

- **A database for what a game runs on.** Wikidata (CC0) was checked on
  2026-11-25 as a second opinion to the executable headers
  (`internal/platform`) and set aside: 2,535 games carry platform DOS
  (Q170434) and 377 MS-DOS (Q47604), 146k plain Microsoft Windows
  (Q1406), but Windows 3.x (Q737508) only 15 and Windows 95 (Q83370)
  none, so it can't tell the versions apart, which the headers do, and
  matching it by title would add false hits. MobyGames separates DOS,
  Windows 3.x and Windows (not 95 from 98), behind a free non-commercial
  API key: the better candidate, as an optional online lookup.

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
  was already on the input archive's own entries: their MS-DOS date/time
  fields (`zipw.DOSModified`), the only timestamp DOSBox Pure reads, never
  `zip.File.Modified`, which prefers an extended timestamp field (UTC)
  and so shifted a Windows-made archive's dates by its maker's time zone. `verify` checks the
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
