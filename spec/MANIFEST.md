# DOSZ Manifest Specification

| | |
|---|---|
| Format version | 1 |
| Status | Draft |
| Reference implementation | `doszer` |

## 1. Introduction

This specification defines `DOSZ.TOML`, a manifest describing how to build a
`.dosz` archive for [DOSBox Pure](https://github.com/libretro/dosbox-pure)
that runs without per-game frontend configuration.

A manifest serves two purposes:

- **Build description**: it records how an archive was built from a source
  directory, so the archive can be rebuilt consistently.
- **Recipe**: combined with media the user supplies, it reproduces an archive
  without distributing the archive's content.

This specification defines the manifest format, the processing model, the
archive an implementation produces, and how equivalence between archives is
determined. It does not define any command-line interface or configuration
file of an implementation.

## 2. Conventions

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT", "SHOULD",
"SHOULD NOT", "RECOMMENDED", "NOT RECOMMENDED", "MAY", and "OPTIONAL" in this
document are to be interpreted as described in BCP 14 (RFC 2119, RFC 8174)
when, and only when, they appear in all capitals.

Paths in the manifest use `/` as separator and are relative unless stated
otherwise. Path matching is case-insensitive, because the guest file systems
are.

## 3. Terminology

| Term | Meaning |
|---|---|
| Archive | A ZIP file with the `.dosz` extension, loaded by DOSBox Pure as content. |
| Source | A directory containing a manifest and the files an archive is built from. |
| Media | A disc image identified in the manifest (§7). |
| System directory | The frontend's BIOS/system directory as seen by DOSBox Pure. |
| OS image | A bootable hard disk image (`.vhd`, `.img`) with an installed operating system. |
| System shell | An archive containing an operating system that runs on DOSBox's built-in DOS. |
| Parent | An archive layered beneath another archive. |
| Implementation | Software that processes manifests according to this specification. |

## 4. Boot models

An archive uses exactly one boot model.

| Model | OS location | C: drive | Archive contents appear on | Typical use |
|---|---|---|---|---|
| Plain | None (built-in DOS) | The archive | C: | DOS programs |
| Shell | System directory, `.dosz` | Shell, layered under the archive | C: (merged with the shell) | Programs for an environment that runs on DOS, e.g. Windows 3.x |
| OS image | System directory, `.vhd`/`.img` | The OS image | D: (separate drive) | Programs for an operating system that boots itself, e.g. Windows 95 or 98, or a DOS other than the built-in one |

Under the OS image model, CD-ROM images in the archive appear as E:.

> A shell is files run by DOSBox Pure's built-in DOS, so it can hold any
> environment started from DOS. Windows 95 and 98 cannot be shells: they
> start from their own DOS, which has to boot from a disk.

A shell is merged with the archive file by file; archive files replace shell
files of the same name. An OS image is read by the guest OS directly; archive
files cannot replace anything on it.

## 5. Manifest

### 5.1 Encoding and location

A manifest MUST be a [TOML 1.0](https://toml.io/en/v1.0.0) document encoded as
UTF-8, named `DOSZ.TOML`, at the root of the source.

### 5.2 Top-level keys

| Key | Type | Required | Meaning |
|---|---|---|---|
| `format` | integer | yes | Specification version. MUST be `1`. |
| `name` | string | no | Canonical name. If absent, the source directory name is used. |
| `notes` | string | no | Free-form text. |

### 5.3 `[boot]`

| Key | Type | Meaning |
|---|---|---|
| `exec` | string | Plain model: DOS path of the program or batch file to run, e.g. `C:\GAME\GAME.EXE`. MAY include arguments after a space. |
| `shell` | string | Shell model: system shell path relative to the system directory, without extension. |
| `os` | string | OS image model: OS image path relative to the system directory, without extension. |
| `parent` | string | File name, with extension, of an archive layered beneath this one. Resolved in the archive's directory. |
| `disc` | string | Label of the disc image to mount at startup. |
| `skip` | integer | Number of frames to skip at startup. |

At most one of `exec`, `shell` and `os` MUST be set. `parent` MAY be combined
with any of them. `skip` requires one of `exec`, `shell` or `os`; DOSBox Pure
ignores it otherwise. `disc` MAY be used alone.

`skip` depends on emulation timing. A manifest that sets `skip` SHOULD record
the frontend options it was measured with in `notes`.

### 5.4 `[layout]`

| Key | Type | Meaning |
|---|---|---|
| `trim` | array of strings | Glob patterns of files to remove. `**` matches any number of directories. |
| `flatten` | boolean | Collapse directories that contain only a single subdirectory (§6.3). Default `false`. |

`[[layout.rewrite]]`, zero or more:

| Key | Type | Meaning |
|---|---|---|
| `from` | string | Literal text to replace. Matched case-insensitively. |
| `to` | string | Replacement text. |
| `files` | array of strings | Glob patterns of files to apply the rewrite to. |

### 5.5 `[pack]`

| Key | Type | Meaning |
|---|---|---|
| `store` | array of strings | File extensions, without a leading dot, stored uncompressed. |
| `audio` | string | Encoding of CD audio tracks: `"ogg"` (default) or `"wav"`. |

### 5.6 `[[source.media]]`

Zero or more entries identifying disc images.

| Key | Type | Required | Meaning |
|---|---|---|---|
| `id` | string | yes | Identifier referenced by `[[extract]]`. Unique within the manifest. |
| `label` | string | no | Human-readable name, e.g. the original file name. |
| `redump` | string | no | Reference to the disc's [Redump](http://redump.org) entry, e.g. a disc page URL. Informational only; Redump hashes are not normalized track hashes (§7.2). |
| `tracks` | array of tables | yes | One entry per track, in track order. |

Each `tracks` entry:

| Key | Type | Meaning |
|---|---|---|
| `type` | string | `"data"` or `"audio"`. |
| `sha1` | string | Normalized track hash (§7.2), 40 lowercase hexadecimal characters. |

### 5.7 `[[extract]]`

Copies content from identified media into the build.

| Key | Type | Meaning |
|---|---|---|
| `media` | string | `id` of a `[[source.media]]` entry. |
| `from` | string | Path within the media's data track file system. A directory copies its contents recursively. |
| `to` | string | Destination path within the build. |

If `[[extract]]` is absent, media files present in the source are included
whole (§6.4).

### 5.8 `[[file]]`

Files whose content is given in the manifest.

| Key | Type | Meaning |
|---|---|---|
| `path` | string | Destination path within the build. |
| `text` | string | File content. Line endings are normalized per §6.6. |

### 5.9 `[[input.pad]]`

Gamepad mappings, zero to eight entries. The first entry maps controller
port 1, the second port 2, and so on. An entry with no keys leaves its port
unmapped.

Each key names a gamepad control and is OPTIONAL. Controls use the libretro
RetroPad layout, in which `b`, `a`, `y` and `x` are the bottom, right, left
and top face buttons:

| Key | Control |
|---|---|
| `b`, `a`, `y`, `x` | Face buttons |
| `up`, `down`, `left`, `right` | D-pad |
| `select`, `start` | Select and Start |
| `l`, `r`, `l2`, `r2` | Shoulder buttons and triggers |
| `l3`, `r3` | Stick buttons |
| `lstick_left`, `lstick_right`, `lstick_up`, `lstick_down` | Left analog stick directions |
| `rstick_left`, `rstick_right`, `rstick_up`, `rstick_down` | Right analog stick directions |
| `wheel` | Array of strings: the action wheel's items, at most 100 |

Every value other than `wheel`, and every `wheel` item, is a binding string:
one to four action names joined by `+`, optionally followed by a space and a
label, e.g. `"space Fire"` or `"leftctrl+x Use Item"`. Pressing the control
performs every action at once. A label MUST NOT contain a control character
or the sequence ` #`, and leading and trailing spaces are not part of it.

Action names are matched case-insensitively:

| Action | Names |
|---|---|
| Keyboard key | `1`-`0`, `a`-`z`, `f1`-`f12`, `esc`, `tab`, `backspace`, `enter`, `space`, `leftalt`, `rightalt`, `leftctrl`, `rightctrl`, `leftshift`, `rightshift`, `capslock`, `scrolllock`, `numlock`, `grave`, `minus`, `equals`, `backslash`, `leftbracket`, `rightbracket`, `semicolon`, `quote`, `period`, `comma`, `slash`, `extra_lt_gt`, `printscreen`, `pause`, `insert`, `home`, `pageup`, `delete`, `end`, `pagedown`, `left`, `up`, `down`, `right`, `kp1`-`kp9`, `kp0`, `kpdivide`, `kpmultiply`, `kpminus`, `kpplus`, `kpenter`, `kpperiod` |
| Mouse | `mouse_move_up`, `mouse_move_down`, `mouse_move_left`, `mouse_move_right`, `mouse_left_click`, `mouse_right_click`, `mouse_middle_click`, `mouse_speed_up`, `mouse_speed_down` |
| Joystick | `joy_up`, `joy_down`, `joy_left`, `joy_right`, `joy_button1`-`joy_button4`, `joy_hat_up`, `joy_hat_down`, `joy_hat_left`, `joy_hat_right`, `joy_2_up`, `joy_2_down`, `joy_2_left`, `joy_2_right` |
| Frontend | `menu` (DOSBox Pure's menu and on-screen keyboard), `wheel` (opens the action wheel), `hold_port1`-`hold_port4` (the controller acts as that port's mapping while held) |

A stick direction bound to a mouse or joystick movement moves it in
proportion to the stick, e.g. `lstick_left = "joy_left"` with
`lstick_right = "joy_right"`.

### 5.10 Extensions

Keys beginning with `x-` are reserved for implementation-specific extensions.
Implementations MUST ignore `x-` keys they do not recognize. Any other
unrecognized key MUST be treated as an error.

## 6. Processing model

An implementation MUST produce output equivalent (§9) to performing the
following steps in order. Paths in each step refer to the build as left by the
previous step.

### 6.1 Collect

The build starts with the source directory's files, excluding `DOSZ.TOML`,
followed by the result of every `[[extract]]` entry in manifest order, then
every `[[file]]` entry. A later entry replaces an earlier file at the same path.

### 6.2 Trim

Every file matching a `trim` pattern is removed. Directories left empty are
removed.

### 6.3 Flatten

If `flatten` is `true`: any directory whose only entry is a single
subdirectory is replaced by that subdirectory, repeatedly, until no such
directory remains. The build root is never removed, and no file is moved to
the build root by flattening.

### 6.4 Media

A disc image is a CUE sheet in the build together with the files it
references, resolved relative to the CUE sheet. Every disc image whose
referenced files are all present in the build is converted track by track:

| Input track | Output file | CUE `FILE` type | CUE `TRACK` mode |
|---|---|---|---|
| Data track | The track's 2048-byte user data (§7.2) from INDEX 01, in order: an ISO 9660 image | `BINARY` | `MODE1/2048` |
| Audio track in a `BINARY` file | The track's samples from INDEX 01 to the end of the track, encoded per `[pack].audio` as Ogg Vorbis or 16-bit PCM WAV, 44.1 kHz, 2 channels | `OGG` or `WAVE` | `AUDIO` |
| Audio track in any other file | The file, unchanged | unchanged | `AUDIO` |

A converted track's pregap (its INDEX 00 to INDEX 01 span plus any `PREGAP`)
is not included in its output file. The converted CUE sheet instead gives
it as a `PREGAP` command, with the track's `INDEX 01` at `00:00:00`, so
every track keeps its position on the disc. An unchanged file keeps its
tracks' `PREGAP` and `INDEX` commands as they were.

A CUE sheet referencing the output files replaces the original in the same
directory. If its tracks match a `[[source.media]]` entry, it is named
`<name>.cue` (`<name> (<id>).cue` if the manifest has more than one
`[[source.media]]` entry), where `<name>` is the manifest's effective name
(§8.3) and `<id>` is the entry's `id`. Otherwise it keeps its name. With
`<base>` the CUE sheet's name without its extension, output files are named:

| Output file | Name |
|---|---|
| First data track | `<base>.iso` |
| Any other track | `<base> (Track NN).<ext>`, where `NN` is the two-digit track number and `<ext>` is `iso`, `ogg`, `wav`, or an unchanged file's own extension |

An unchanged file referenced by more than one track keeps its name. Files
the original CUE sheet referenced are removed from the build.

> DOSBox Pure lists every ISO and CUE file in the content as a separate disc
> image, except one sharing a CUE sheet's name without its extension; naming
> the data track `<base>.iso` keeps it from being listed twice.

### 6.5 Rewrite

Each `[[layout.rewrite]]` rule, in manifest order, replaces every
case-insensitive occurrence of `from` with `to` in each file matching its
`files` patterns that contains no NUL byte. A matching file need not have a
text file name (§6.6): a rule's patterns name its files explicitly, e.g. a
game's INI-format `RESOURCE.CFG`. Rewrites MUST NOT be applied to a file
containing a NUL byte.

### 6.6 Text normalization

A file is a text file if its name matches `*.BAT`, `*.INI`, `*.CONF` or
`CONFIG.SYS` and it contains no NUL byte. Text files MUST use CRLF line
endings and end with a line ending.

### 6.7 Exclusion

Files matching `desktop.ini`, `Thumbs.db`, `ehthumbs.db`, `.DS_Store`, `._*`
or `*:Zone.Identifier` MUST NOT be included.

### 6.8 Runtime files

The following files are generated at the archive root:

| File | Condition | Content |
|---|---|---|
| `AUTOBOOT.DBP` | Any of `exec`, `shell`, `os`, `disc`, `skip` set | §6.8.1 |
| `<parent>.parent` | `parent` set | Empty |
| `PADMAP.DBP` | `[[input.pad]]` present | §6.8.2 |
| `DOS.YML` | The first `[[input.pad]]` entry has a binding §6.8.3 can express | §6.8.3 |
| `DOSZ.TOML` | Always | The manifest (§8.3) |

A generated file replaces any file of the same name in the build, except
`DOS.YML` (§6.8.3).

> DOSBox Pure applies `PADMAP.DBP` as the controller mapping, ahead of any
> other. The mapping in `DOS.YML` supplies the labels shown for bindings
> with the same actions, and becomes port 1's mapping if the player resets
> theirs. A player's own changes are saved over both.

#### 6.8.1 `AUTOBOOT.DBP`

Lines separated by CRLF, with no trailing line ending:

| Line | Content |
|---|---|
| 1 | `exec` value, `S*<shell>`, `O*<os>`, or empty |
| 2 | `skip` value, or empty |
| 3 | `disc` value, or empty |

Trailing empty lines MUST be omitted.

#### 6.8.2 `PADMAP.DBP`

A byte `0`, then one block per `[[input.pad]]` entry, in order. A block is
a byte giving its number of bindings, then the bindings: each bound control
in control ID order, then each `wheel` item in order.

| Control | ID |
|---|---|
| `b`, `y`, `select`, `start`, `up`, `down`, `left`, `right`, `a`, `x`, `l`, `r`, `l2`, `r2`, `l3`, `r3` | 0-15, in that order |
| `lstick_left` and `lstick_right` | 16 |
| `lstick_up` and `lstick_down` | 17 |
| `rstick_left` and `rstick_right` | 18 |
| `rstick_up` and `rstick_down` | 19 |
| A `wheel` item | 20 |

A binding is a byte `ID + 64 × (n − 1)`, where `n` is its number of
actions, followed by its actions. For IDs 0-15 and 20, each action is one
byte, its code. For IDs 16-19, which combine two opposite directions, `n`
is the larger of the two directions' action counts, and each action is two
bytes: the code of the left or up direction's action at that position,
then the right or down direction's, or `0` where a direction has fewer.

| Action | Code |
|---|---|
| Keyboard keys, in the order listed in §5.9 | 1-102 |
| Mouse, then joystick actions, in the order listed in §5.9 | 200-224 |
| `menu` | 225 |
| `wheel` | 226 |
| `hold_port1`-`hold_port4` | 227-230 |

#### 6.8.3 `DOS.YML`

One line per binding of the first `[[input.pad]]` entry whose actions are
all keyboard, mouse, joystick or `wheel` actions, in the order of §6.8.2's
control IDs (`lstick_left` before `lstick_right`, `lstick_up` before
`lstick_down`), then one line per such `wheel` item. A line is
`input_pad_<control>: ` or, for the `n`th wheel item line counting from 1,
`input_wheel_<n>: `, followed by the binding string with its action names
in lowercase, joined by `+`, and its label, if any, after one space. Lines
end with CRLF.

If the build already contains a `DOS.YML` at its root, its lines (separated
by CRLF, LF or CR) whose key begins with `input_pad_` or `input_wheel_` are
removed. Its other lines are kept in order, each ending with CRLF, followed
by the generated lines.

## 7. Media identification

### 7.1 Purpose

Media are identified by the content of their tracks, not by file format, so
that different dumps of the same disc (e.g. BIN/CUE and ISO) are recognized as
the same media.

### 7.2 Normalized track hash

The normalized hash of a track is the SHA-1 of:

| Track type | Data hashed |
|---|---|
| Data | The 2048-byte user data of each sector from the track's INDEX 01, in order. For Mode 1 sectors, bytes 16–2063; for Mode 2 Form 1 sectors, bytes 24–2071; for sectors stored as 2048 bytes, the whole sector. |
| Audio | The 16-bit little-endian stereo PCM samples from the track's INDEX 01 to the end of the track. |

Pregap data (INDEX 00) is excluded.

### 7.3 Verification

When a manifest contains `[[source.media]]`, an implementation MUST look for
each media among the disc images collected for the build (§6.1): CUE sheets,
and ISO 9660 images as a single data track.

A disc image matches a media when it has the same number of tracks, each of
the same type, and every verifiable track's normalized hash equals the
declared `sha1`. A track is verifiable when its file stores the track's
content exactly: raw sectors (`BINARY`) or PCM samples (`WAVE`). A track in a
lossy audio file (e.g. Ogg Vorbis) cannot reproduce its hash; it MUST NOT be
compared, and an implementation SHOULD warn that it was not verified.

A media that no disc image matches is not found. An implementation MUST NOT
produce an archive if `[[extract]]` references a media that is not found, and
SHOULD warn about any other media that is not found.

## 8. Archive

### 8.1 Container

An archive MUST be a ZIP file. Entries MUST use the Store or Deflate
compression methods only. Files whose extension is listed in `store`, and
`.ogg` files, MUST be stored.

### 8.2 Deterministic structure

For reproducible output, an implementation MUST, for every entry (the
archive comment is exempt, §8.4):

- Order entries by path, comparing UTF-8 bytes.
- Set every entry's modification time to its source's modification time: a
  collected file's own timestamp, or, for a file extracted from media
  (§5.7), the timestamp recorded in the media's file system. An entry with
  no such source - a file generated by §6.8, or given directly by a
  `[[file]]` entry - MUST use 1980-01-01 00:00:00. Modification times are
  recorded in UTC.
- Omit extra fields and set external attributes to zero.
- Write no directory entries: a directory exists only as part of the paths
  of the files in it.

A rewrite (§6.5) or text normalization (§6.6) changes an entry's content
without changing its source; its modification time is unaffected.

### 8.3 Embedded manifest

The archive MUST contain the manifest used for the build as `DOSZ.TOML`, with
`name` set to the effective name.

### 8.4 Archive comment

The ZIP archive comment is not part of the archive's content: §8.2 does not
apply to it, it plays no part in equivalence (§9), and a reader MUST NOT
depend on it. An implementation MAY record build information there, for
example the effective name, the implementation and its version, and the
build date.

## 9. Equivalence

Two archives are equivalent if they contain the same set of entry paths and,
for every entry except `DOSZ.TOML` and entries produced by lossy audio
encoding, identical uncompressed content.

Lossy audio encoding is not required to be deterministic across encoder
versions. A recipe requiring full equivalence SHOULD set `audio = "wav"`.

## 10. Conformance

| Level | Requirements |
|---|---|
| Builder | §5, §6 except `[[extract]]` and §7, §8, §9 |
| Recipe | Builder, plus `[[extract]]`, `[[file]]` and §7 |

An implementation MUST state the conformance level it supports.

## 11. Validation

An implementation MUST reject a manifest that violates any of the following:

| Rule |
|---|
| `format` is `1`. |
| At most one of `exec`, `shell`, `os` is set. |
| `skip` is set only if one of `exec`, `shell`, `os` is set, and is not negative. |
| `shell` and `os` contain no extension, no leading `/` and no `\`. |
| `parent` contains an extension and no `/`. |
| Paths contain no `..` component and are not absolute. |
| `[[source.media]]` `id` values are unique. |
| `[[extract]]` `media` refers to an existing `id`. |
| `sha1` values are 40 lowercase hexadecimal characters. |
| `audio` is `"ogg"` or `"wav"`. |
| `[[input.pad]]` has at most eight entries, and `wheel` at most 100 items. |
| Every `[[input.pad]]` binding string has one to four action names defined in §5.9, and a label as §5.9 allows. |
| A `wheel` item does not contain the action `wheel`. |

An implementation MUST reject an archive build that would contain, under the
shell model, `WINDOWS/WIN.INI`, `WINDOWS/SYSTEM.INI`, `WINDOWS/PROGMAN.INI` or
`WINDOWS/*.GRP`, or, under the shell or OS image model, `DOSBOX.BAT`.

A `trim` pattern or `rewrite` rule that matches nothing SHOULD produce a
warning, as SHOULD a media that is not found or a track that is not verified
(§7.3).

## 12. Frontend requirements

Archives built under this specification assume the following DOSBox Pure core
options:

| Option | Value | Reason |
|---|---|---|
| `dosbox_pure_bootos_ramdisk` | `diff` | OS images are shared and MUST NOT be modified. |
| `dosbox_pure_menu_time` | `0` | Exit when an auto-started program ends. |

An OS image used with the OS image model MUST, at startup:

1. if `D:\WINDOWS.BAT` exists, run it, wait for it to finish, then shut the
   guest OS down;
2. otherwise, start a shell from which the user can run programs.

Such an OS image MUST NOT automatically run programs from a disc image when
it is mounted or the guest OS starts (e.g. Windows AutoRun).

`WINDOWS.BAT` SHOULD NOT shut the guest OS down itself, and SHOULD finish
only when the program it starts has ended.
