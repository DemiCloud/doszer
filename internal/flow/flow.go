// Package flow plans making an archive from a disc, an image, a game's
// archive or folder: the doszer commands it takes, in order, as `doszer
// make` and the window's Import a disc and Build a folder run them. Both
// build their command lines here, so the same choices give the same
// commands - and the same session in the logs.
//
// The plan comes in parts, because what follows install depends on what
// it did: Prepare (dump or import, then init), Install (and Extract, when
// install's changes span folders), Start, Build.
package flow

import (
	"strconv"
	"strings"

	"github.com/DemiCloud/doszer/internal/install"
	"github.com/DemiCloud/doszer/internal/platform"
)

// Step is a doszer command (Args, without "doszer"), or, with Prompt
// set, a pause asking the user to do something first.
type Step struct {
	Args   []string
	Prompt string
	Env    []string // KEY=VALUE added to the command's environment
}

// Options are what the game is made from and how.
type Options struct {
	Name   string   // the game's name
	Source string   // the source folder: dumped or imported into, or the game's folder
	Drive  string   // dump the CD in Drive...
	Discs  int      // ...this many discs, asking for each after the first
	Files  []string // or import these: a disc or floppy image, or a game's archive, each
	Folder bool     // or build Source where it is
	Init   bool     // write a manifest (Source has none)

	Boot      string // init --boot: dos, shell, os; "" chooses
	Audio     string // init --audio: ogg, wav; "" Ogg Vorbis
	SystemDir string // --system-dir for init and install
}

// Prepare is everything before install: the discs dumped or the files
// imported, then the manifest written.
func Prepare(o Options) []Step {
	var steps []Step
	switch {
	case o.Folder:
		// Built where it is: nothing to dump.
	case len(o.Files) > 0:
		for _, f := range o.Files {
			steps = append(steps, Step{Args: []string{"dump", "--from", f, o.Source}})
		}
	case o.Drive != "":
		for i := 1; i <= max(1, o.Discs); i++ {
			if i > 1 {
				steps = append(steps, Step{Prompt: DiscPrompt(i, max(1, o.Discs), o.Drive)})
			}
			steps = append(steps, Step{Args: []string{"dump", "--drive", o.Drive, o.Source}})
		}
	}
	if o.Init {
		args := []string{"init", "--force", "--name", o.Name}
		if o.Boot != "" {
			args = append(args, "--boot", o.Boot)
		}
		if o.Audio != "" && o.Audio != "ogg" {
			args = append(args, "--audio", o.Audio)
		}
		if o.SystemDir != "" {
			args = append(args, "--system-dir", o.SystemDir)
		}
		steps = append(steps, Step{Args: append(args, o.Source)})
	}
	return steps
}

// DiscPrompt asks for disc n of all.
func DiscPrompt(n, all int, drive string) string {
	return "Put disc " + strconv.Itoa(n) + " of " + strconv.Itoa(all) + " in " + drive + ", then Continue."
}

// InstallPrompt is said before the install session opens.
const InstallPrompt = "Next, DOSBox Pure opens with the game's disc. Run its setup (SETUP.EXE or INSTALL.EXE: in Windows, from the CD drive), install the game, then close DOSBox Pure's window."

// Install runs the game's setup in DOSBox Pure and extracts what it
// installed; dir, if set, is the folder to extract. result, if set, is
// the file install writes its folders to choose from when its changes
// span several (install.ResultEnv).
func Install(source, systemDir, dir, result string) []Step {
	args := []string{"install"}
	if systemDir != "" {
		args = append(args, "--system-dir", systemDir)
	}
	if dir != "" {
		args = append(args, "--dir", dir)
	}
	st := Step{Args: append(args, source)}
	if result != "" {
		st.Env = []string{install.ResultEnv + "=" + result}
	}
	return []Step{{Prompt: InstallPrompt}, st}
}

// Extract extracts folder dir from an install session's kept save (the
// Retry install wrote), without running the session again.
func Extract(source, systemDir string, r *install.Retry, dir string) Step {
	args := []string{"install", "--from", r.From}
	if r.Content != "" {
		args = append(args, "--content", r.Content)
	}
	if systemDir != "" {
		args = append(args, "--system-dir", systemDir)
	}
	return Step{Args: append(args, "--dir", dir, source)}
}

// Start sets the program the game starts with; display, for a Windows
// 9x game, the mode to switch to first (640x480x8).
func Start(source, program, display string) Step {
	args := []string{"start", "--force"}
	if display != "" {
		args = append(args, "--display", display)
	}
	return Step{Args: append(args, source, program)}
}

// Build builds the archive.
func Build(source, output string, flatten bool) Step {
	args := []string{"build"}
	if flatten {
		args = append(args, "--flatten")
	}
	return Step{Args: append(args, source, output)}
}

// Repack rebuilds a doszer archive (one with a manifest) as output.
func Repack(archive, output string) Step {
	return Step{Args: []string{"repack", archive, output}}
}

// HasInstalledPrograms reports whether a folder holds programs outside
// disc images: an installed game, whose start program is to be chosen.
func HasInstalledPrograms(dir string) bool {
	progs, _ := platform.Scan(dir)
	for _, p := range progs {
		if !strings.Contains(p.Path, ":") {
			return true
		}
	}
	return false
}

// SafeName makes name a file name on every system.
func SafeName(name string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if strings.ContainsRune(`\/:*?"<>|`, r) || r < 32 {
			return -1
		}
		return r
	}, name))
}
