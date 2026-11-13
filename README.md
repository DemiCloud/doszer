# doszer

Builds `.dosz` archives for [DOSBox Pure](https://github.com/libretro/dosbox-pure)
from `DOSZ.TOML` manifests, so games run without per-game frontend
configuration.

`doszer` is the reference implementation of the
[DOSZ Manifest Specification](spec/MANIFEST.md).

Conformance level: Builder, plus `[[extract]]` and §7 media identification
(Recipe, except §6.4's whole-disc-image conversion).

## Status

Early development. `init` and `build` are implemented, including
`[[extract]]` and media identification (§7); `build` cannot yet convert a
whole disc image already sitting in the build (§6.4 - ISO/Ogg/WAV output,
rewritten CUE). `install`, `repack` and `verify` are not yet implemented.

## Requirements

- Go 1.24 or later, to build
- `ffmpeg`, for Ogg Vorbis audio tracks

## Building

```bash
go build ./cmd/doszer
go test ./...
```

## Commands

```
doszer init    <source> [--os <path> | --shell <path> | --exec <path>] [--name <name>] [--force]
doszer build   <source> [<output.dosz>] [--name <name>] [--dry-run] [--strict]
doszer install <source> [--from <file>]
doszer repack  <input.dosz> [<output.dosz>]
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

`build --dry-run` prints the planned trim, flatten and rewrite operations
without writing anything. For every path changed by flattening, it searches
text files for references, suggests `[[layout.rewrite]]` rules, and reports
references in binary files that cannot be rewritten.

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
whole by the most specific layer that sets it.

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

Install sessions require `dosbox_pure_bootos_ramdisk = "false"` in the
standalone DOSBox Pure used for authoring, so installed files land in the
disposable image.

## Frontend setup

Archives assume these DOSBox Pure core options (see the specification, §12):

| Option | Value |
|---|---|
| `dosbox_pure_bootos_ramdisk` | `diff` |
| `dosbox_pure_menu_time` | `0` |

OS images and system shells go in a `dosbox_pure/` subdirectory of the
frontend's system directory, e.g. `dosbox_pure/Win98.vhd`.
