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
| Plain | None (built-in DOS) | The archive | C: | DOS games |
| Shell | System directory, `.dosz` | Shell, layered under the archive | C: (merged with the shell) | Windows 3.x |
| OS image | System directory, `.vhd`/`.img` | The OS image | D: (separate drive) | Windows 9x |

Under the OS image model, CD-ROM images in the archive appear as E:.

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

### 5.9 Extensions

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

Disc images in the build are converted:

| Input | Output |
|---|---|
| Data track | ISO 9660 image of the track's 2048-byte user data |
| Audio track | Ogg Vorbis or PCM WAV per `[pack].audio`, 44.1 kHz, 2 channels |

A CUE sheet referencing the converted tracks replaces the original, named
`<name>.cue` (`<name> (<id>).cue` if more than one `[[source.media]]` entry
is converted), where `<name>` is the manifest's effective name (§8.3) and
`<id>` is the entry's `id`.

### 6.5 Rewrite

Each `[[layout.rewrite]]` rule, in manifest order, replaces every
case-insensitive occurrence of `from` with `to` in matching text files (§6.6).
Rewrites MUST NOT be applied to binary files.

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
| `DOSZ.TOML` | Always | The manifest (§8.3) |

A generated file replaces any file of the same name in the build.

#### 6.8.1 `AUTOBOOT.DBP`

Lines separated by CRLF, with no trailing line ending:

| Line | Content |
|---|---|
| 1 | `exec` value, `S*<shell>`, `O*<os>`, or empty |
| 2 | `skip` value, or empty |
| 3 | `disc` value, or empty |

Trailing empty lines MUST be omitted.

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

When a manifest contains `[[source.media]]`, an implementation MUST verify
every track hash of every media it uses and MUST NOT produce an archive if any
hash differs.

## 8. Archive

### 8.1 Container

An archive MUST be a ZIP file. Entries MUST use the Store or Deflate
compression methods only. Files whose extension is listed in `store`, and
`.ogg` files, MUST be stored.

### 8.2 Deterministic structure

For reproducible output, an implementation MUST:

- Order entries by path, comparing UTF-8 bytes.
- Set every entry's modification time to its source's modification time: a
  collected file's own timestamp, or, for a file extracted from media
  (§5.7), the timestamp recorded in the media's file system. An entry with
  no such source - a file generated by §6.8, or given directly by a
  `[[file]]` entry - MUST use 1980-01-01 00:00:00.
- Omit extra fields and set external attributes to zero.

A rewrite (§6.5) or text normalization (§6.6) changes an entry's content
without changing its source; its modification time is unaffected.

### 8.3 Embedded manifest

The archive MUST contain the manifest used for the build as `DOSZ.TOML`, with
`name` set to the effective name.

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

An implementation MUST reject an archive build that would contain, under the
shell model, `WINDOWS/WIN.INI`, `WINDOWS/SYSTEM.INI`, `WINDOWS/PROGMAN.INI` or
`WINDOWS/*.GRP`, or, under the shell or OS image model, `DOSBOX.BAT`.

A `trim` pattern or `rewrite` rule that matches nothing SHOULD produce a
warning.

## 12. Frontend requirements

Archives built under this specification assume the following DOSBox Pure core
options:

| Option | Value | Reason |
|---|---|---|
| `dosbox_pure_bootos_ramdisk` | `diff` | OS images are shared and MUST NOT be modified. |
| `dosbox_pure_menu_time` | `0` | Exit when an auto-started program ends. |

An OS image used with the OS image model MUST run `D:\WINDOWS.BAT`, if present,
at startup.
