package manifest

import (
	"fmt"
	"strings"
)

// TemplateOptions pre-fills a generated manifest.
type TemplateOptions struct {
	Name  string
	Exec  string
	Shell string
	OS    string
	Audio string  // [pack] audio, "" to leave it commented out
	Media []Media // [[source.media]] entries to write, from build.IdentifyMedia;
	// a single disc is labelled with Name rather than its file name
	Icon     *Icon  // [icon], nil to leave it commented out
	IconFrom string // where init found Icon, for a comment
}

// Template returns a commented starting manifest for `init`.
func Template(o TemplateOptions) string {
	var b strings.Builder

	fmt.Fprintf(&b, "format = %d\n", FormatVersion)
	fmt.Fprintf(&b, "name   = %s\n", quote(o.Name))
	b.WriteString(`# notes = "Free-form text."

[boot]
# Set at most one of exec/shell/os.

# Plain model (neither shell nor os set): DOSBox Pure's built-in DOS.
# Program or batch file to run at startup; doszer install sets it when
# the installer wrote exactly one batch file to start the game.
`)
	b.WriteString(line("exec", o.Exec, `'C:\GAME\GAME.EXE'`))
	b.WriteString(`
# Shell model: an environment that runs on DOS (e.g. a Windows 3.x
# installation), as a .dosz layered under C: on DOSBox's built-in DOS.
# This archive (and any parent) is merged into the same C:.
# Relative to DOSBox Pure's system directory, without extension.
`)
	b.WriteString(line("shell", o.Shell, `"path/to/shell"`))
	b.WriteString(`
# OS image model: a disk image (.vhd/.img) with an installed operating
# system that boots itself (e.g. Windows 95 or 98), booted as C:.
# This archive (and any parent) appears as a separate drive, D:.
# Relative to DOSBox Pure's system directory, without extension.
`)
	b.WriteString(line("os", o.OS, `"path/to/image"`))
	b.WriteString(`
# Archive layered beneath this one, for files shared between games.
# File name with extension, looked up in the same directory as this .dosz.
# parent = "Shared.shell"

# Disc image label to mount at startup.
# disc = "Game (Disc 1)"

# Frames to skip at startup. Requires exec, shell or os.
# skip = 0

[layout]
# Removed before flattening. ** matches any number of directories.
# trim = ["**/*.HLP"]

# Collapse directories that contain only a single subdirectory.
# flatten = true

# Literal, case-insensitive replacement in text files, after flattening.
# [[layout.rewrite]]
# from  = 'C:\STUDIO\GAME'
# to    = 'D:\GAME'
# files = ["**/*.INI", "**/*.BAT"]

[pack]
# Extensions stored uncompressed. ogg is always stored. Rarely needed for
# a disc image: DOSBox Pure decompresses a large Deflate entry in full
# once per session and caches an index for random access after that, so
# compressing it only costs time on first launch, not every launch.
# store = ["bin"]

# CD audio encoding: "ogg" (default) or "wav". WAV is larger, but keeps
# every track's hash verifiable (Ogg Vorbis is lossy).
`)
	b.WriteString(line("audio", o.Audio, `"ogg"`))
	b.WriteString(`
# The archive's icon (DOSZ.ICO), for game lists: an ICO file or a Windows
# program's icon, in the build or, with media, on a disc (e.g. the one its
# AUTORUN.INF names). index picks one of a program's icons.
`)
	if ic := o.Icon; ic != nil {
		if o.IconFrom != "" {
			fmt.Fprintf(&b, "# Found: %s.\n", o.IconFrom)
		}
		b.WriteString("[icon]\n")
		b.WriteString(line("path", ic.Path, ""))
		if ic.Media != "" {
			b.WriteString(line("media", ic.Media, ""))
		}
		if ic.Index != 0 {
			fmt.Fprintf(&b, "index = %d\n", ic.Index)
		}
	} else {
		b.WriteString("# [icon]\n# path  = \"GAME/GAME.EXE\"\n# media = \"disc1\"\n# index = 0\n")
	}
	b.WriteString(`
# Gamepad mapping, one [[input.pad]] per controller port (spec §5.9).
# RetroPad layout: b/a/y/x are the bottom/right/left/top face buttons.
# Value: up to 4 actions joined by +, then an optional label.
# [[input.pad]]
# b            = "space Fire"
# a            = "leftctrl+enter Use"
# start        = "esc Menu"
# lstick_left  = "left"
# lstick_right = "right"
# rstick_left  = "mouse_move_left"
# rstick_right = "mouse_move_right"
# r            = "wheel"
# wheel        = ["f1 Help", "f10 Save"]
`)

	b.WriteString(`
# Disc images, identified by their tracks' content (spec §7), so a build
# can check it was given the right disc and name its CUE sheet after the
# game. Track hashes are SHA-1 over each track's user data or audio.
`)
	if len(o.Media) == 0 {
		b.WriteString(`# [[source.media]]
# id     = "disc1"
# label  = "Example Game (Disc 1)"
# tracks = [
#   { type = "data",  sha1 = "<40 hex digits>" },
#   { type = "audio", sha1 = "<40 hex digits>" },
# ]
`)
	}
	for i, m := range o.Media {
		if len(o.Media) == 1 && o.Name != "" {
			m.Label = o.Name
		}
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "[[source.media]]\nid     = %s\nlabel  = %s\n", quote(m.ID), quote(m.Label))
		if m.RedumpName != "" {
			fmt.Fprintf(&b, "# Matched Redump's datfile: redump_sha1 is each track as Redump records it.\nredump_name = %s\n", quote(m.RedumpName))
		}
		if m.Redump != "" {
			fmt.Fprintf(&b, "redump = %s\n", quote(m.Redump))
		}
		b.WriteString("tracks = [\n")
		for _, t := range m.Tracks {
			pad := ""
			if t.Type == TrackData {
				pad = " "
			}
			if t.RedumpSHA1 != "" {
				fmt.Fprintf(&b, "  { type = %q,%s sha1 = %q, redump_sha1 = %q },\n", t.Type, pad, t.SHA1, t.RedumpSHA1)
			} else {
				fmt.Fprintf(&b, "  { type = %q,%s sha1 = %q },\n", t.Type, pad, t.SHA1)
			}
		}
		b.WriteString("]\n")
	}

	return b.String()
}

func line(key, value, example string) string {
	if value != "" {
		return fmt.Sprintf("%s = %s\n", key, quote(value))
	}
	return fmt.Sprintf("# %s = %s\n", key, example)
}

// quote returns a TOML string literal for s, preferring literal strings so
// DOS paths keep their backslashes readable.
func quote(s string) string {
	if !strings.ContainsAny(s, "'\n\r") {
		return "'" + s + "'"
	}
	return fmt.Sprintf("%q", s)
}
