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

## Building

```bash
make build   # → build/doszer
make test
make help    # every target
```

## Commands

```
doszer init    SOURCE [--os PATH | --shell PATH | --exec PATH] [--name NAME] [--no-media] [-f]
doszer build   SOURCE [OUTPUT.dosz] [--name NAME] [-n] [--strict] [--ogg-quality N]
doszer install SOURCE [--dir PATH] [--from FILE [--content FILE] [--template FILE]] [-f] [-n] [--keep]
               [--os PATH] [--drive c|d] [--dosbox-pure FILE] [--system-dir DIR] [--save-dir DIR]
doszer repack  INPUT.dosz [OUTPUT.dosz] [--manifest FILE] [-n] [--strict] [--ogg-quality N]
doszer verify  FILE.dosz
```

Options are GNU-style: `-n`/`--dry-run`, `-f`/`--force` and `-h`/`--help`
have short forms (combinable, e.g. `-nf`); a long option's value may follow
it or be attached with `=` (`--name=NAME`); options may come before or after
arguments, and `--` ends them.

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

The default output is `<name>.dosz` in the current directory. `--name`
overrides the manifest's `name`, which defaults to the source directory name.

`build --dry-run` prints the planned trim, flatten, media conversion and
rewrite operations without writing anything. For every path changed by flattening, it searches
files up to 8 MB for references, suggests `[[layout.rewrite]]` rules, and
reports references in binary files (containing a NUL byte) that cannot be
rewritten. A rewrite rule applies to any file without a NUL byte that its
`files` patterns name, e.g. an INI-style `RESOURCE.CFG` (spec §6.5).

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
  build ends.
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

Once, point `doszer` at standalone DOSBox Pure, in
`~/.config/doszer/config.toml`:

```toml
dosbox_pure = "/path/to/DOSBoxPure.exe"
```

1. **Dump the disc** to a BIN/CUE (or ISO) with any CD ripping tool, into
   a new directory, e.g. `Example Game/`. The file names don't matter:
   `build` renames the disc after the game.

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

   Start the game once if you like, then close DOSBox Pure. `doszer`
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
system_dir  = "/path/to/system"      # resolves and checks os/shell targets
save_dir    = "/path/to/saves"       # where DOSBox Pure writes .pure.zip files
dosbox_pure = "/path/to/executable"  # standalone DOSBox Pure, used by install
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

`install` runs an installer under standalone DOSBox Pure and extracts what
it wrote into the source. It packs the source (install media included,
`DOSZ.TOML` and `DOSBOX.BAT` left out) into a temporary archive with a
unique name, so the session's save files are fresh, starts DOSBox Pure on
it, and waits for DOSBox Pure to exit. Then it reads the save files:

| Boot model | Session starts | Install to | Changes read from |
|---|---|---|---|
| OS image | the OS image (`boot.os`) | D: | `<name>-<HASH>.sav` over the D: disk DOSBox Pure builds from the archive |
| Shell | the shell (`boot.shell`) | C: | `<name>.pure.zip` |
| Plain | DOSBox Pure's start menu | C: | `<name>.pure.zip` |

An OS image session writes a `FRONTEND.DBP` setting "Save Difference Per
Content" for itself only, so the OS image is never modified and C: changes
land in `<name>-CDRIVE.sav`; they're listed, not extracted, since every
archive shares C:. Nothing in your DOSBox Pure configuration changes.

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
change is under. Other changes are listed. A file already in the source
with different content stops the extraction unless `--force` is given.
Save files are removed after a successful extraction unless `--keep` is
given.

For a plain model install, `install` lists the `.BAT`, `.COM` and `.EXE`
files the installer added in the extracted folder or one folder below it,
and if the manifest has no `exec` and exactly one batch file is at the
shallowest level, sets `exec` to it (not under `--dry-run`, which only
reports it). Under the other models the shell or OS image starts the
game (`WINDOWS.BAT`), so there is no `exec` to set.

`install --from <file>` skips DOSBox Pure and extracts from an existing
save or disk image:

| `--from` | Also needs |
|---|---|
| `<content>-<HASH>.sav` (D:) | `--content`, the archive it was made over; the hash must match |
| `<content>-CDRIVE.sav` (C:) | the image it was made over: `--os`, `install_os` or `boot.os` |
| `.pure.zip` | (`--content` to tell modified files from added ones) |
| `.vhd`, `.img` | `--template` or a configured template to compare against, else `--dir` |

DOSBox Pure's system and save directories come from `DOSBoxPure.cfg` beside
the executable (`path_system`, `path_saves`, else `system/` and `saves/`),
unless `system_dir`/`save_dir` are set. Under WSL, a Windows
`DOSBoxPure.exe` is run with paths converted by `wslpath`.

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
