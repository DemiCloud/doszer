# doszer

Builds `.dosz` archives for [DOSBox Pure](https://github.com/libretro/dosbox-pure)
from `DOSZ.TOML` manifests, so games run without per-game frontend
configuration.

`doszer` is the reference implementation of the
[DOSZ Manifest Specification](spec/MANIFEST.md).

Conformance level: Recipe.

## Status

Early development. `init`, `build`, `verify` and `repack` are implemented,
including `[[extract]]`, media identification (§7) and disc image
conversion (§6.4). `install` is not yet.

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
doszer init    <source> [--os <path> | --shell <path> | --exec <path>] [--name <name>] [--force]
doszer build   <source> [<output.dosz>] [--name <name>] [--dry-run] [--strict] [--ogg-quality <n>]
doszer install <source> [--from <file>]
doszer repack  <input.dosz> [<output.dosz>] [--manifest <file>] [--dry-run] [--strict] [--ogg-quality <n>]
doszer verify  <file.dosz>
```

| Command | Purpose |
|---|---|
| `init` | Write a commented `DOSZ.TOML` into the source. |
| `build` | Build an archive from a source. Writes only the output file. |
| `install` | Run an installer under DOSBox Pure and extract the chosen folder into the source. |
| `repack` | Rebuild an archive from its embedded manifest. |
| `verify` | Check an archive against its embedded manifest and the specification. |

The default output is `<name>.dosz` in the current directory. `--name`
overrides the manifest's `name`, which defaults to the source directory name.

`build --dry-run` prints the planned trim, flatten, media conversion and
rewrite operations without writing anything. For every path changed by flattening, it searches
text files for references, suggests `[[layout.rewrite]]` rules, and reports
references in binary files that cannot be rewritten.

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
  `.doszer-media-*` directory next to the output archive, removed when the
  build ends.
- Disc images inside an archive being repacked are streamed, not loaded
  into memory.
- A CUE sheet whose referenced files aren't all in the build, or that uses
  `MOTOROLA` (big-endian) files, is left unconverted with a warning
  (a failure under `--strict`).
- Audio already encoded (any CUE `FILE` type other than `BINARY`) is kept
  as it is, even if `[pack].audio` asks for the other encoding.
- `verify` reports a CUE sheet that §6.4 would still change.

## Example

```bash
mkdir "Example Game"
# copy the game's files and disc images into it
doszer init --os dosbox_pure/Win98 "Example Game"
# edit Example Game/DOSZ.TOML
doszer build --dry-run "Example Game"
doszer build "Example Game"
```

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
"dosbox_pure/Win98" = "/path/to/Win98.template"
```

A manifest overrides them the same way, under `[x-doszer]` (spec §5.9's
extension-key namespace, so the manifest schema doesn't reject it):

```toml
[x-doszer]
ogg_quality = 10
```

`templates` merges by key across layers; every other setting is replaced
whole by the most specific layer that sets it. `repack` has no source
directory, so it skips the `[x-doszer]` layer.

## Install

`install` captures the files an installer writes and nothing else:

| Boot model | Installer runs against | Changes land in | Compared against |
|---|---|---|---|
| OS image | A disposable copy of the template | The disposable image | The template |
| Shell | The shell (read-only) plus install media | `<content>.pure.zip` | — (changes only) |
| Plain | Built-in DOS plus install media | `<content>.pure.zip` | — (changes only) |

DOSBox Pure runs under a unique temporary content name, so the session writes
a fresh save archive. Afterwards the chosen folder is extracted into the
source; all other changed files are reported but not extracted.

`install --from <file>` skips the emulator and extracts from an existing
`.vhd`, `.img` or `.pure.zip`.

Install sessions require `dosbox_pure_bootos_ramdisk = "false"` ("Keep") in
the standalone DOSBox Pure used for authoring, so installed files land in
the disposable image. Test builds with "Save Difference Per Content"
(`"diff"`), the setting spec §12 requires for normal use.

## Frontend setup

Archives assume these DOSBox Pure core options (see the specification, §12):

| Option | Value |
|---|---|
| `dosbox_pure_bootos_ramdisk` | `diff` |
| `dosbox_pure_menu_time` | `0` |

OS images and system shells go in a `dosbox_pure/` subdirectory of the
frontend's system directory, e.g. `dosbox_pure/Win98.vhd`.
