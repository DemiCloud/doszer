# doszer

Builds `.dosz` archives for [DOSBox Pure](https://github.com/libretro/dosbox-pure)
from `DOSZ.TOML` manifests, so games run without per-game frontend
configuration.

`doszer` is the reference implementation of the
[DOSZ Manifest Specification](spec/MANIFEST.md).

Conformance level: Recipe.

## Status

Early development. Every command is implemented: `init`, `build`
(including `[[extract]]`, media identification (§7) and disc image
conversion (§6.4)), `install`, `repack` and `verify`.

## Requirements

- Go 1.24 or later, to build
- `ffmpeg` with `libvorbis`, for Ogg Vorbis audio tracks (not needed with
  `audio = "wav"`)
- For `install`: nothing on Windows; SDL2 (`libSDL2-2.0.so.0`, installed
  on most desktops and SteamOS) on Linux. doszer carries its own copy of
  the DOSBox Pure core (built by `make core`) and runs it in its own
  window. RetroArch or standalone DOSBox Pure can be used instead.
- To build the embedded core: [zig](https://ziglang.org/) for the Linux
  ones (`make core`), and MinGW-w64 for the Windows one (`make core-all`,
  which `make release` runs)

## Building

```bash
make build   # → build/doszer
make test
make help    # every target
```

## Commands

```
doszer dump    DIR [--drive DRIVE] [--dat FILE]
doszer redump  [SHA1|FILE...] [--update] [--dat FILE]
doszer init    SOURCE [--os PATH | --shell PATH | --exec PATH] [--name NAME] [--no-media] [-f]
doszer build   SOURCE [OUTPUT.dosz] [--name NAME] [-n] [--strict] [--ogg-quality N]
doszer install SOURCE [--dir PATH] [--from FILE [--content FILE] [--template FILE]] [-f] [-n] [--keep]
               [--os PATH] [--drive c|d] [--frontend builtin|retroarch|standalone] [--frontend-dir DIR]
               [--system-dir DIR] [--save-dir DIR]
doszer repack  INPUT.dosz [OUTPUT.dosz] [--manifest FILE] [-n] [--strict] [--ogg-quality N]
doszer verify  FILE.dosz
```

Options are GNU-style: `-n`/`--dry-run`, `-f`/`--force` and `-h`/`--help`
have short forms (combinable, e.g. `-nf`); a long option's value may follow
it or be attached with `=` (`--name=NAME`); options may come before or after
arguments, and `--` ends them.

On a terminal, each command shows the step it's on with a spinner (and a
count where there is one, e.g. files written), then each finished step
with `✓`, or `✗` for the one that failed. Piped or redirected, only
results, warnings and errors are printed.

| Command | Purpose |
|---|---|
| `init` | Write a commented `DOSZ.TOML` into the source. |
| `build` | Build an archive from a source. Writes only the output file. |
| `install` | Run an installer under DOSBox Pure and extract the chosen folder into the source. |
| `repack` | Rebuild an archive from its embedded manifest. |
| `verify` | Check an archive against its embedded manifest and the specification. |

`init` also hashes every disc image (CUE sheet) in the source into
`[[source.media]]` entries (spec §7.2), labelled with the CUE sheet's name;
A CUE sheet with tracks in lossy audio files (Ogg) can't be hashed and is
skipped. `--no-media` skips this.

`build` and `repack` check each `[[source.media]]` entry against the disc
images in the build (spec §7.3). Tracks stored as Ogg can't be checked and
are skipped with a warning; use `audio = "wav"` to keep a disc fully
verifiable. A disc that isn't found fails the build only when `[[extract]]`
reads from it, and is otherwise a warning (a failure under `--strict`).

`build` and `repack` record the game's name, the `doszer` version, the
build date (UTC, or `SOURCE_DATE_EPOCH`'s) and this project's URL in the
ZIP comment, which `unzip -z` and most archive tools show. It is outside
the specification's reproducibility rules (§8.4): two builds of the same
source differ only there, and `verify` ignores it.

Paths match case-insensitively (spec §2), so two source files or archive
entries whose paths differ only in case are one file: `build` and `repack`
keep the one that sorts last (e.g. `readme.txt` over `README.TXT`) and
warn (a failure under `--strict`). A `[[file]]` entry replaces a file at
its path whatever the case.

Every path in a manifest and every entry name in an archive is `/`
separated, with no empty, `.` or `..` component and no `\` (spec §2,
§8.2): `GAME/./X.TXT`, `GAME//X.TXT` or a trailing `/` is rejected, not
cleaned up. `[[extract]] from = "/"` names the disc's root directory.
`build` rejects a source file whose name contains a `\`. `repack` reads
a legacy archive's entry names as paths: `GAME\X.TXT` (written by some
Windows tools), `./GAME/X.TXT` and `/GAME/X.TXT` become `GAME/X.TXT`,
and an entry outside the archive (`../X.TXT`) or two entries naming the
same path are an error.

The default output is `<name>.dosz` in the current directory. `--name`
overrides the manifest's `name`, which defaults to the source directory name.

`build --dry-run` prints the planned trim, flatten, media conversion and
rewrite operations without writing anything. For every path changed by flattening, it searches
files up to 8 MB for references, suggests `[[layout.rewrite]]` rules, and
reports references in binary files (containing a NUL byte) that cannot be
rewritten. A rewrite rule applies to any file without a NUL byte that its
`files` patterns name, e.g. an INI-style `RESOURCE.CFG` (spec §6.5).

## Dumping and verifying discs

`dump` reads a CD with redumper into `DIR`, as `disc<N>.cue` and its
track files. redumper's work files (an image of the raw disc and its
state) are kept beside `DIR`, in `.<DIR>.doszer-dump`, until the dump is
done, so a failed or interrupted dump resumes, re-reading only what's
missing; its log is kept in `doszer/dumps` in the user cache directory.
A drive that can't read the lead-out can't read the last few samples a
positive read offset shifts into it: the dump is finished with them left
as zeros, as they almost always are on the disc, and reported.

The dump is then checked against Redump's IBM PC datfile, matching every
track's size and SHA-1: `Verified`, or the closest disc and the tracks
that differ, or no match (a bad dump, or a disc not in Redump yet). The
datfile is, in order: `--dat`/`redump_dat` (any Logiqx datfile); a copy
fetched from redump.org at most weekly and cached in the user cache
directory; the snapshot built into doszer (`make dat`). `doszer redump`
shows which is in use, `--update` fetches the current one, and given
SHA-1s or track files (or a CUE sheet) it looks them up.

## Media conversion

`build` and `repack` convert every disc image in the build (spec §6.4):
data tracks become ISO images, audio tracks stored as raw CD-DA become Ogg
Vorbis or WAV, and the CUE sheet is rewritten to match. Implementation
details:

- Ogg Vorbis is encoded by `ffmpeg`'s `libvorbis` at `ogg_quality`
  (default 6; see Configuration), with `-fflags +bitexact -flags +bitexact`
  so the same `ffmpeg` and `libvorbis` versions produce identical files.
  WAV files are written directly.
- Tracks are encoded in parallel, one per CPU, and written to a temporary
  `.doszer-build-*` directory next to the output archive, removed when the
  build ends. A data track `[[extract]]` reads from is read in place if
  it is already 2048-byte sectors (an ISO, or a `MODE1/2048` BIN), else
  copied to a temporary `.doszer-media-*.iso` file there while it is
  being identified.
- Disc images inside an archive being repacked are streamed, not loaded
  into memory.
- A CUE sheet whose referenced files aren't all in the build, or that uses
  `MOTOROLA` (big-endian) files, is left unconverted with a warning
  (a failure under `--strict`).
- Audio already encoded (any CUE `FILE` type other than `BINARY`) is kept
  as it is, even if `[pack].audio` asks for the other encoding.
- `verify` reports a CUE sheet that §6.4 would still change, and warns
  about a `[[source.media]]` entry the archive's disc images don't match.

## Gamepad mappings

An archive can ship its own controller mapping, so a game is playable on a
gamepad-only device (e.g. a Steam Deck or Steam Machine) with no setup.
Without one, DOSBox Pure uses its built-in mapping for games it
recognizes, else generic keyboard keys. Give one `[[input.pad]]` per
controller port (spec §5.9):

```toml
[[input.pad]]
b            = "space Fire"          # action, then an optional label
a            = "leftctrl+enter Use"  # up to 4 actions pressed together
start        = "esc Menu"
lstick_up    = "up"
lstick_down  = "down"
lstick_left  = "left"
lstick_right = "right"
rstick_left  = "mouse_move_left"     # mouse moves with the stick
rstick_right = "mouse_move_right"
rstick_up    = "mouse_move_up"
rstick_down  = "mouse_move_down"
r2           = "mouse_left_click"
r            = "wheel"               # hold for the action wheel
wheel        = ["f1 Help", "f5 Save", "f9 Load"]
```

Controls use libretro's RetroPad names, which are positions, not the
letters printed on the buttons:

| RetroPad | Steam Deck / Xbox | PlayStation |
|---|---|---|
| `b` (bottom) | A | Cross |
| `a` (right) | B | Circle |
| `y` (left) | X | Square |
| `x` (top) | Y | Triangle |
| `l`, `r` / `l2`, `r2` | LB, RB / LT, RT | L1, R1 / L2, R2 |
| `l3`, `r3` | stick clicks | L3, R3 |
| `select`, `start` | View, Menu | Share, Options |

The Deck's back buttons (L4/R4/L5/R5) aren't RetroPad controls: bind them
to one of the above in Steam Input.

`build` writes the mapping as `PADMAP.DBP`, the file DOSBox Pure's own
Controller Mapper saves, and port 1's mapping again as `input_pad_` lines
in `DOS.YML` (spec §6.8). DOSBox Pure uses `PADMAP.DBP`; `DOS.YML` adds the
labels, shown in the Controller Mapper and the action wheel, and is what
port 1 falls back to if a player resets their mapping. A stick axis is one
binding there, so its two directions show one joined label
(`lstick_up = "up Forward"` and `lstick_down = "down Backward"` both show
"Forward / Backward"). A player's own
changes are saved to their save file, over the archive's. Lines of a
`DOS.YML` already in the source other than `input_pad_`/`input_wheel_` are
kept.

L3 opens DOSBox Pure's menu and on-screen keyboard unless the mapping binds
it (`Input > Use L3 Button to Show Menu`). Bind `menu` to another control
to keep that if L3 is needed for something else. `hold_port2` makes a
control a shift key: while it's held, the pad uses `[[input.pad]]` #2's
mapping, doubling the controls one pad has.

To try a mapping out, open the Controller Mapper from DOSBox Pure's menu
in the game, then copy what works into the manifest. A `PADMAP.DBP` made
with the Controller Mapper can also be shipped as it is: copy it from the
save file (`<save dir>/<content>.pure.zip`) into the source's root. It is
binary, labels are lost, and `[[input.pad]]` replaces it.

In RetroArch, each port's device type (Quick Menu, Controls) must be the
default, "Use Gamepad Mapper" (it may show as `[Pad Mapper] ...`); a preset
there ignores the archive's mapping for that port.

## Boot models

Every archive runs one of three ways (specification §4). Pick by what the
game needs, from its box, manual or installer:

| The game needs | Boot model | Manifest | The game's files are on |
|---|---|---|---|
| DOS | Plain | `exec` = program to start, or nothing (start menu) | C: |
| An environment that runs on DOS, e.g. Windows 3.x | Shell | `shell` = the shell's path | C:, merged with the shell |
| An operating system that boots itself, e.g. Windows 95 or 98 | OS image | `os` = the image's path | D: |

- **Plain**: DOSBox Pure's built-in DOS, nothing else needed. Most DOS
  games.
- **Shell**: a `.dosz` of files laid under C: and run by the built-in DOS,
  e.g. a Windows 3.x installation shared by several games. It starts
  `C:\WINDOWS.BAT`, else `C:\AUTOEXEC.BAT`, else `C:\WINDOWS\WIN.COM`.
- **OS image**: a hard disk image (`.vhd`/`.img`) with an installed
  operating system, which boots itself. Windows 95 and 98 have to be
  images, not shells: they start from their own DOS, which has to boot
  from a disk. So does a game that needs a real MS-DOS rather than
  DOSBox's.

Shells and images aren't part of an archive. Each player provides their
own, in the frontend's system directory; `shell` and `os` give its path
there, without extension, and are the same for every game that uses it.

A CD with both a DOS and a Windows version can use either; the DOS
version needs nothing beyond DOSBox Pure.

## Example

```bash
mkdir "Example Game"
# copy the game's files and disc images into it
doszer init --os path/to/image "Example Game"
# edit Example Game/DOSZ.TOML
doszer build --dry-run "Example Game"
doszer build "Example Game"
```

[examples/](examples/) holds complete manifests for real games:
[Betrayal in Antara](examples/betrayal-in-antara/DOSZ.TOML), a Windows 95
game under an OS image, with trimming, flattening, rewrites, a startup
`WINDOWS.BAT` and its discs identified by track hash; and
[King's Quest VII](examples/kings-quest-vii/DOSZ.TOML), a DOS game
installed from CD under the plain model.

## From a CD to a .dosz

A DOS game that installs from CD, start to finish, under the plain model
(see "Boot models" for the others). This assumes a disc you own.
[examples/kings-quest-vii](examples/kings-quest-vii/DOSZ.TOML) is the
result for one.

`install` needs no setup: it runs DOSBox Pure in a window of its own.
To use RetroArch or standalone DOSBox Pure instead, see "Install".

1. **Dump the disc** with the CD in the drive:

   ```bash
   doszer dump "Example Game"
   ```

   This writes `disc1.cue` and its tracks to `Example Game/`; run it again
   with the next disc of a multi-disc game. doszer carries
   [redumper](https://github.com/superg/redumper), which corrects the
   drive's read offset, so the dump matches anyone else's of the same disc,
   and then checks it against [Redump](http://redump.org)'s database:
   `Verified` means every track matches a disc others have dumped and
   cross-checked, so the dump is exact. Under WSL it runs the Windows
   redumper, since WSL can't reach optical drives; `--drive E:` picks a
   drive. An interrupted or failed dump resumes where it stopped when run
   again. A BIN/CUE or ISO from any other ripping tool works too
   (`doszer redump disc.cue` checks it). File names don't matter: `build`
   renames the disc after the game.

2. **Write a manifest:**

   ```bash
   doszer init "Example Game"
   ```

   It identifies the disc by its content, so later builds can check they
   have the right one.

3. **Install the game:**

   ```bash
   doszer install "Example Game"
   ```

   DOSBox Pure starts with the CD mounted as `D:`. Its start menu lists
   every program on the CD: pick the installer (`INSTALL.BAT`,
   `INSTALL.EXE`, `SETUP.EXE`, ...), or go to the command line and type
   `D:` and then `INSTALL`. Install to `C:` and keep the default folder.
   - Sound: DOSBox Pure emulates a Sound Blaster 16 at address 220, IRQ 7,
     DMA 1 (high DMA 5); General MIDI (MPU-401 at 330) only once a MIDI
     soundfont or device is chosen in its settings.
   - If the installer offers to copy game data from the CD to the hard
     disk for speed, choose the least: in the archive, the CD and the hard
     disk are the same file, so a copy only makes it bigger.
   - Errors from commands DOSBox doesn't have (e.g. `Illegal command:
     mode.`) are usually harmless; the installer carries on.
   - Note how the installer says to start the game.

   Start the game once if you like, then close DOSBox Pure (or RetroArch).
   `doszer`
   copies the folder the installer created (e.g. `C:\GAME`) into
   `Example Game/GAME/`.

4. **Check the startup program.** `install` lists the batch files and
   programs the installer wrote, and if exactly one batch file sits at
   the top of the new folder (the one installers write to start the
   game), sets it as `exec` in `Example Game/DOSZ.TOML`:

   ```
   Programs installed:
     C:\GAME\GAME.BAT
     C:\GAME\GAME.EXE
     C:\GAME\SETUP.EXE
   Set boot.exec = 'C:\GAME\GAME.BAT' in DOSZ.TOML
   ```

   Otherwise, set it yourself to what the installer said to run:

   ```toml
   [boot]
   exec = 'C:\GAME\GAME.EXE'
   ```

   Keep the folder where it is: installers write their own path (and the
   CD's drive letter, `D:`) into the game's batch and configuration files.

5. **Build and check:**

   ```bash
   doszer build --dry-run "Example Game"
   doszer build "Example Game"
   doszer verify "Example Game.dosz"
   ```

6. **Play:** put `Example Game.dosz` with your other DOSBox Pure content.
   It starts the game directly.

## Configuration

Every setting can be given as a flag. Precedence, highest first:

1. Command-line flags
2. `<source>/DOSZ.TOML`'s `[x-doszer]` table (per-game settings)
3. `./doszer.toml`
4. `$XDG_CONFIG_HOME/doszer/config.toml` (default `~/.config/doszer/config.toml`)
5. Built-in defaults

`doszer.toml` and the user config file take the settings at top level:

```toml
system_dir  = "/path/to/system"      # DOSBox Pure's system directory (OS images, shells)
save_dir    = "/path/to/saves"       # DOSBox Pure's save directory
frontend    = "builtin"              # DOSBox Pure for install: builtin, retroarch or standalone
frontend_dir = "/path/to/install"    # retroarch or standalone install directory (default: detected)
ogg_quality = 6

[templates]                          # pristine OS images used by install
"path/to/image" = "/path/to/image.template"

[install_os]                         # image install sessions boot, by boot.os
"path/to/image" = "path/to/install-image"
```

A manifest overrides them the same way, under `[x-doszer]` (spec §5.10's
extension-key namespace, so the manifest schema doesn't reject it):

```toml
[x-doszer]
ogg_quality = 10
```

`templates` and `install_os` merge by key across layers; every other setting is replaced
whole by the most specific layer that sets it. `repack` has no source
directory, so it skips the `[x-doszer]` layer.

## Install

`install` runs an installer under DOSBox Pure (in RetroArch, or
standalone) and extracts what it wrote into the source. It packs the source (install media included,
`DOSZ.TOML` and `DOSBOX.BAT` left out) into a temporary archive with a
unique name, so the session's save files are fresh, starts DOSBox Pure on
it, and waits for DOSBox Pure (or RetroArch) to exit. Then it reads the save files:

| Boot model | Session starts | Install to | Changes read from |
|---|---|---|---|
| OS image | the OS image (`boot.os`) | D: | `<name>-<HASH>.sav` over the D: disk DOSBox Pure builds from the archive |
| Shell | the shell (`boot.shell`) | C: | `<name>.pure.zip` |
| Plain | DOSBox Pure's start menu | C: | `<name>.pure.zip` |

An OS image session sets "Save Difference Per Content" for itself only
(standalone: a `FRONTEND.DBP` in the content; RetroArch: a game-specific
core options file), so the OS image is never modified and C: changes land
in `<name>-CDRIVE.sav`; they're listed, not extracted, since every archive
shares C:. Nothing in your DOSBox Pure or RetroArch configuration changes.

The OS image must be a raw `.img` or a fixed-size VHD, not a dynamic
(growing) VHD: DOSBox Pure's "Save Difference" mode compares written
sectors against the wrong bytes of a dynamic VHD and silently drops some
writes, corrupting the session's C: (and, in a frontend, any game's C:
changes). `install` refuses a dynamic VHD. To convert one:
`qemu-img convert -O vpc -o subformat=fixed dynamic.vhd fixed.vhd`.

An OS image session boots `--os`, else the image `install_os` maps
`boot.os` to, else `boot.os` itself: an archive's own image may start the
game through a custom shell, while installing wants a plain Windows
desktop. `--drive c` extracts from C: instead of D:, for an installer that
only works in its default folder on C:; the folder is `--dir`, or the one
top-level folder outside `WINDOWS` every change is under. The game's paths
then name C:, which the manifest's `[[layout.rewrite]]` rules can fix
(`build --dry-run` suggests rules for paths that move).

The folder to extract is `--dir`, or the one top-level folder every
change is under, or the root when every change is a file there (`--dir /`
asks for the root). A session that changed nothing extracts nothing:
DOSBox Pure doesn't save a file rewritten with the same content, so
running a setup program and keeping its settings leaves no change. A
game that comes installed doesn't need `install`; `init` and `build` it
as it is. Other changes are listed. A file already in the source
with different content stops the extraction unless `--force` is given.
Save files are removed after a successful extraction unless `--keep` is
given.

For a plain model install, `install` lists the `.BAT`, `.COM` and `.EXE`
files the installer added in the extracted folder or one folder below it,
and if the manifest has no `exec` and exactly one batch file is at the
shallowest level, sets `exec` to it (not under `--dry-run`, which only
reports it). Under the other models the shell or OS image starts the
game (`WINDOWS.BAT`), so there is no `exec` to set.

Installers for Windows 95 and 98 often record settings in the registry,
on C:, which the archive boots as it was, without them. So whenever C:
changes are read from a Windows 9x OS image (an OS image session, or
`--from` a `-CDRIVE.sav` or an image with a template), `install` compares
its registry (`WINDOWS\SYSTEM.DAT`, `WINDOWS\USER.DAT`) before and after,
and writes what the installer added for its program as `INSTALL.REG` in
the extracted folder: keys under `Software`, except the ones Windows
changes on its own (`Microsoft`, `Classes`, `Description`). It prints the
line `WINDOWS.BAT` needs to restore them at each startup:

```bat
REGEDIT /S D:\GAME\INSTALL.REG
```

A game that reads nothing from the registry runs without it.

The extracted folder is on D: in the archive wherever the game was
installed, so with `--drive c`, paths into it in `INSTALL.REG` are moved
from C: to D:.

Installers also put runtime DLLs into Windows' own folders
(`C:\WINDOWS\SYSTEM`), which the archive doesn't carry either. So
`install` reads the import tables of the extracted folder's programs and
copies each DLL the installer added to or changed in `C:\WINDOWS` or
`C:\WINDOWS\SYSTEM` that one of them imports (and the DLLs those import)
beside the program: Windows looks in a program's own folder first. Other
changes to Windows, such as fonts, are listed but not carried over.

`install --from <file>` skips DOSBox Pure and extracts from an existing
save or disk image:

| `--from` | Also needs |
|---|---|
| `<content>-<HASH>.sav` (D:) | `--content`, the archive it was made over; the hash must match |
| `<content>-CDRIVE.sav` (C:) | the image it was made over: `--os`, `install_os` or `boot.os` |
| `.pure.zip` | (`--content` to tell modified files from added ones) |
| `.vhd`, `.img` | `--template` or a configured template to compare against, else `--dir` |

`install` runs DOSBox Pure under `frontend`:

| `frontend` | Runs |
|---|---|
| `builtin` (default) | The DOSBox Pure libretro core doszer embeds (`make core`), in doszer's own window |
| `retroarch` | The DOSBox Pure core in RetroArch, where archives are meant to run (the default in a build without the embedded core) |
| `standalone` | Standalone DOSBox Pure |

`builtin` is the same core RetroArch runs, built from DOSBox Pure
1.0 Preview 8, which doszer's save-file handling was checked against. The
core is unpacked to the user cache directory on first use. doszer answers
its questions itself: the session's core options go straight to the core,
saves go to `save_dir` (default: `doszer/saves` in the user cache
directory), and OS images and shells come from `system_dir`, else
RetroArch's system directory if RetroArch is installed. Click the window
to capture the mouse; Ctrl+F10 releases it. Close the window when the
install is done. It's built for linux/amd64 and linux/arm64 (glibc 2.28
or later and the system's SDL2; WSLg under WSL) and windows/amd64 (it
carries SDL2); elsewhere use `retroarch` or `standalone`.

For `retroarch` and `standalone`, `frontend_dir` is the folder holding
that frontend's executable (`retroarch`/`retroarch.exe`,
`DOSBoxPure`/`DOSBoxPure.exe`). Unset, `install` looks for RetroArch with the DOSBox Pure core on `PATH`,
in `RetroArch-Win64`, `RetroArch`, `Program Files\RetroArch` and Steam's
`steamapps\common\RetroArch` on every drive (under Windows and WSL), in
`/Applications` (macOS), and as a Flatpak (`org.libretro.RetroArch`, then
RetroDECK's `net.retrodeck.retrodeck`); standalone DOSBox Pure, on `PATH`.

RetroArch's settings come from the `retroarch.cfg` beside it, else its
platform's default (`%APPDATA%\RetroArch`, `~/.config/retroarch`, a
Flatpak's `~/.var/app/<id>/config/retroarch`): the core from
`libretro_directory`, the system and save directories from
`system_directory` and `savefile_directory` (in its `DOSBox-pure`
subfolder with "Sort Saves into Folders by Core Name"), unless
`system_dir`/`save_dir` are set. RetroArch runs with that configuration
plus a session file (`--appendconfig`) that names those directories, turns
off saving the configuration on exit, the content history and save-state
autoload, and quits RetroArch when the content closes. Its config
directory points at the session folder, which holds the content's
game-specific core options: your DOSBox Pure core options, with the
session's over them. Your configuration, core options and overrides are
left unchanged, and not used for the session apart from those options.

Standalone DOSBox Pure's system and save directories come from
`DOSBoxPure.cfg` beside the executable (`path_system`, `path_saves`, else
`system/` and `saves/`), unless `system_dir`/`save_dir` are set. Then
`install` runs DOSBox Pure from its session folder with a copy of that
`DOSBoxPure.cfg` naming those directories (DOSBox Pure prefers a
`DOSBoxPure.cfg` in its working directory), so it boots OS images and
shells from, and saves to, the same places doszer reads; your own
`DOSBoxPure.cfg` is left unchanged, and settings changed during the
session aren't kept.

Under WSL, a Windows `retroarch.exe` or `DOSBoxPure.exe` is run with paths
converted by `wslpath`.

Limitations: a change is detected by size or modification time; the
content must not have a `<content>.pure.zip` of its own layered over it,
or parent archives.

## Frontend setup

Archives assume these DOSBox Pure core options (see the specification, §12):

| Option | Value |
|---|---|
| `dosbox_pure_bootos_ramdisk` | `diff` |
| `dosbox_pure_menu_time` | `0` |

OS images and system shells go in a `dosbox_pure/` subdirectory of the
frontend's system directory, e.g. `dosbox_pure/<image>.vhd`, named
however you like: a manifest's `shell` or `os` names the file by its path
there (`dosbox_pure/<image>`), so archives that share one must use the
same name.

### Windows 9x OS image

Specification §12's startup behaviour, for a Windows 98 image: in
`C:\WINDOWS\SYSTEM.INI`, `[boot]`, replace `shell=Explorer.exe` with

```ini
shell=wscript.exe C:\WINDOWS\STARTRUN.VBS
```

and add `C:\WINDOWS\STARTRUN.VBS`:

```vbs
Set fso = CreateObject("Scripting.FileSystemObject")
Set sh  = CreateObject("WScript.Shell")
If fso.FileExists("D:\WINDOWS.BAT") Then
    sh.Run "D:\WINDOWS.BAT", 0, True
    sh.Run "RUNDLL32.EXE SHELL32.DLL,SHExitWindowsEx 1", 0, False
Else
    sh.Run "C:\WINDOWS\PROGMAN.EXE", 1, False
End If
```

Window style `0` hides `WINDOWS.BAT`'s console window. The fallback is
Program Manager: Explorer started by a script that isn't the configured
shell opens a folder window, not the desktop. AutoRun is Explorer's, so
with Explorer never running as the shell, discs are not AutoRun.

Use a fixed VHD or raw image: DOSBox Pure loses writes to a dynamic VHD
under `diff` (see "Install" above to convert one).

## Credits

doszer exists for [DOSBox Pure](https://codeberg.org/schelling/dosbox-pure)
by Bernhard Schelling, the DOSBox-based libretro core and standalone
emulator its archives run in, and embeds a build of it for `install`. DOSBox Pure is built on
[DOSBox](https://www.dosbox.com/). Both are licensed under the GNU General
Public License, version 2 or later; their source is at those links, and
`make core` builds the embedded core from DOSBox Pure's 1.0 Preview 8
release tag. Thank you to Bernhard Schelling and the DOSBox team.

`dump` runs [redumper](https://github.com/superg/redumper) (GNU General
Public License, version 3), embedded from its official release binaries;
its source is at that link.

The built-in frontend also uses [SDL](https://www.libsdl.org/) (zlib
license; its Windows DLLs are embedded) and
[purego](https://github.com/ebitengine/purego) (Apache 2.0), and follows
the [libretro](https://www.libretro.com/) API.
