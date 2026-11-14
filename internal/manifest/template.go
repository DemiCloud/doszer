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
	Media []Media // [[source.media]] entries to write, from build.IdentifyMedia
}

// Template returns a commented starting manifest for `init`.
func Template(o TemplateOptions) string {
	var b strings.Builder

	fmt.Fprintf(&b, "format = %d\n", FormatVersion)
	fmt.Fprintf(&b, "name   = %s\n", quote(o.Name))
	b.WriteString(`# notes = "Free-form text."

[boot]
# Set at most one of exec/shell/os.

# Plain model: DOS program or batch file to run at startup.
`)
	b.WriteString(line("exec", o.Exec, `'C:\GAME\GAME.EXE'`))
	b.WriteString(`
# System shell archive (.dosz), layered under C: on DOSBox's built-in DOS.
# This archive (and any parent) is merged into the same C:.
# Relative to DOSBox Pure's system directory, without extension.
`)
	b.WriteString(line("shell", o.Shell, `"dosbox_pure/Win31"`))
	b.WriteString(`
# Installed OS disk image (.vhd/.img), booted as C:.
# This archive (and any parent) appears as a separate drive, D:.
# Relative to DOSBox Pure's system directory, without extension.
`)
	b.WriteString(line("os", o.OS, `"dosbox_pure/Win98"`))
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

# CD audio encoding: "ogg" (default) or "wav".
# audio = "ogg"
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
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "[[source.media]]\nid     = %s\nlabel  = %s\ntracks = [\n", quote(m.ID), quote(m.Label))
		for _, t := range m.Tracks {
			pad := ""
			if t.Type == TrackData {
				pad = " "
			}
			fmt.Fprintf(&b, "  { type = %q,%s sha1 = %q },\n", t.Type, pad, t.SHA1)
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
