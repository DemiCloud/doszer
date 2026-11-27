package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/DemiCloud/doszer/internal/config"
	"github.com/DemiCloud/doszer/internal/dump"
	"github.com/DemiCloud/doszer/internal/flow"
	"github.com/DemiCloud/doszer/internal/install"
	"github.com/DemiCloud/doszer/internal/manifest"
	"github.com/DemiCloud/doszer/internal/runlog"
	"github.com/DemiCloud/doszer/internal/start"
)

// runMake implements `doszer make`: the window's Import a disc and
// Build a folder, from a terminal. Each step is a doszer command of its
// own, run as the window runs it - the commands internal/flow plans -
// all one session in the logs.
func runMake(args []string) error {
	fs := newFlags("make")
	drive := fs.String("drive", "", "")
	discs := fs.Int("discs", 1, "")
	from := fs.StringArray("from", nil, "")
	audio := fs.String("audio", "", "")
	boot := fs.String("boot", "", "")
	flatten := fs.Bool("flatten", false, "")
	inst := fs.Bool("install", false, "")
	program := fs.String("start", "", "")
	display := fs.String("display", "", "")
	dir := fs.String("dir", "", "")
	romDir := fs.String("rom-dir", "", "")
	systemDir := fs.String("system-dir", "", "")
	dryRun := fs.BoolP("dry-run", "n", false, "")
	const help = `doszer make - make an archive from a CD, a disc image, a game's archive or folder

Usage: doszer make [options] --drive DRIVE | --from FILE... NAME [OUTPUT.dosz]
       doszer make [options] DIR [OUTPUT.dosz]

Options:
      --audio FMT         CD audio encoding: ogg or wav
      --boot MODEL        boot model: dos, shell or os (default: from the programs)
      --dir PATH          install: the folder to extract when several changed
      --discs N           --drive: discs to dump, asking for each (default: 1)
      --display MODE      Windows 9x: switch to WxHxBPP first (640x480x8: 256 colours)
      --drive DRIVE       dump the CD in DRIVE
  -n, --dry-run           print the first commands without running them
      --flatten           collapse single-folder chains, recording it in the manifest
      --from FILE         import a disc or floppy image or a game's archive (repeatable, one per disc)
      --install           run the game's setup in DOSBox Pure first
      --start PROGRAM     program the game starts with (default: asked, else the likely one)
` + flagROMDir + flagSystemDir + flagFormatText + flagHelp
	if ok, err := parse(fs, args, help, 1, 2); !ok {
		return err
	}
	if *drive != "" && len(*from) > 0 {
		return errors.New("--drive and --from exclude each other")
	}
	cfg, err := config.Load("")
	if err != nil {
		return err
	}
	m := &maker{session: runlog.NewSession(time.Now()), format: fsString(fs, "format"), dryRun: *dryRun}
	o := flow.Options{Drive: *drive, Discs: *discs, Files: *from, Boot: *boot, Audio: *audio, SystemDir: stringOr(*systemDir, cfg.Paths.System)}

	// Where it comes from: a drive or files (dumped or imported into a
	// source folder), or a folder built where it is.
	target := fs.Arg(0)
	temp := false
	switch {
	case *drive != "" || len(o.Files) > 0:
		o.Name = target
		name := flow.SafeName(target)
		if name == "" {
			return fmt.Errorf("%q: no name for a folder", target)
		}
		if len(o.Files) == 1 && dump.IsArchive(o.Files[0]) {
			if k, err := dump.ArchiveKind(o.Files[0]); err == nil && k == dump.ArchiveDoszer {
				// Already a doszer archive: rebuilt as it is.
				out, err := makeOutput(fs, cfg, *romDir, name)
				if err != nil {
					return err
				}
				return m.run(flow.Repack(o.Files[0], out))
			}
		}
		srcs := config.SourcesDirOf(cfg)
		if srcs.Path == "" {
			d, err := os.MkdirTemp("", "doszer-source-")
			if err != nil {
				return err
			}
			defer func() {
				if temp {
					os.RemoveAll(d)
				}
			}()
			srcs.Path, temp = d, true
		}
		o.Source = filepath.Join(srcs.Path, name)
		o.Init = true
	default:
		fi, err := os.Stat(target)
		if err != nil {
			return err
		}
		if !fi.IsDir() {
			return fmt.Errorf("%s: not a folder (--from takes a disc image or archive)", target)
		}
		o.Source, o.Folder = target, true
		o.Name = filepath.Base(filepath.Clean(target))
		if mf, err := manifest.Load(target); err == nil {
			o.Name = mf.Name
		} else {
			o.Init = true
		}
	}
	out, err := makeOutput(fs, cfg, *romDir, flow.SafeName(o.Name))
	if err != nil {
		return err
	}

	if m.dryRun {
		steps := flow.Prepare(o)
		if *inst {
			steps = append(steps, flow.Install(o.Source, o.SystemDir, *dir, "")...)
		}
		if *program != "" {
			steps = append(steps, flow.Start(o.Source, *program, *display))
		}
		steps = append(steps, flow.Build(o.Source, out, *flatten))
		for _, st := range steps {
			if st.Prompt != "" {
				fmt.Fprintf(stdout, "# %s\n", st.Prompt)
				continue
			}
			fmt.Fprintln(stdout, runlog.CommandLine(st.Args))
		}
		if *program == "" && (*inst || o.Folder) {
			fmt.Fprintln(stdout, "# start: chosen once the game is installed")
		}
		return nil
	}

	for _, st := range flow.Prepare(o) {
		if err := m.run(st); err != nil {
			return err
		}
	}
	if *inst {
		if err := m.install(o, *dir); err != nil {
			return err
		}
	}
	if *program != "" || *inst || o.Folder && flow.HasInstalledPrograms(o.Source) {
		if err := m.chooseStart(o.Source, *program, *display); err != nil {
			return err
		}
	}
	if err := m.run(flow.Build(o.Source, out, *flatten)); err != nil {
		return err
	}
	if temp {
		fmt.Fprintf(stderr, "Removed the temporary source %s (set paths.sources to keep sources)\n", o.Source)
	}
	return nil
}

// fsString is a string flag's value.
func fsString(fs interface{ GetString(string) (string, error) }, name string) string {
	v, _ := fs.GetString(name)
	return v
}

// makeOutput is the archive make writes: OUTPUT, else NAME.dosz in the
// ROM folder (--rom-dir, else the first configured or found).
func makeOutput(fs interface {
	NArg() int
	Arg(int) string
}, cfg config.Config, romDir, name string) (string, error) {
	if fs.NArg() == 2 {
		return fs.Arg(1), nil
	}
	dirs := config.ROMDirsOf(cfg, romDir)
	if len(dirs) == 0 {
		return "", errors.New("no ROM folder: give OUTPUT.dosz, --rom-dir or paths.roms")
	}
	return filepath.Join(dirs[0].Path, name+".dosz"), nil
}

// maker runs make's steps.
type maker struct {
	session string
	format  string // --format, passed on
	dryRun  bool
	in      *bufio.Reader
}

// interactive reports whether make can ask: its input is a terminal.
func interactive() bool { return isTerminal(os.Stdin) }

func (m *maker) ask(q string) string {
	if m.in == nil {
		m.in = bufio.NewReader(os.Stdin)
	}
	fmt.Fprint(stderr, q)
	l, _ := m.in.ReadString('\n')
	return strings.TrimSpace(l)
}

// run runs a step: a doszer command of its own, in this session, its
// output the terminal's; or a prompt, waited on.
func (m *maker) run(st flow.Step) error {
	if st.Prompt != "" {
		if !interactive() {
			if st.Prompt == flow.InstallPrompt {
				return nil // the session's window says what to do
			}
			return fmt.Errorf("%s (needs a terminal to wait)", st.Prompt)
		}
		m.ask(strings.Replace(st.Prompt, "then Continue", "then press Enter", 1) + " ")
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	args := st.Args
	if m.format != "" {
		args = append([]string{args[0], "--format", m.format}, args[1:]...)
	}
	fmt.Fprintf(stderr, "==> %s\n", runlog.CommandLine(args))
	cmd := exec.Command(exe, args...)
	cmd.Env = append(append(os.Environ(), runlog.SessionEnv+"="+m.session), st.Env...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("doszer %s failed: %w", args[0], err)
	}
	return nil
}

// install runs the setup, then, if its changes spanned folders none of
// which stood out, extracts the one chosen: asked, or --dir.
func (m *maker) install(o flow.Options, dir string) error {
	f, err := os.CreateTemp("", "doszer-install-*.json")
	if err != nil {
		return err
	}
	f.Close()
	result := f.Name()
	defer os.Remove(result)
	steps := flow.Install(o.Source, o.SystemDir, dir, result)
	if err := m.run(steps[0]); err != nil {
		return err
	}
	err = m.run(steps[1])
	if err == nil {
		return nil
	}
	r := install.ReadRetry(result)
	if r == nil {
		return err
	}
	if !interactive() {
		return fmt.Errorf("%w: choose the folder with --dir", err)
	}
	fmt.Fprintf(stderr, "The setup changed files in %s:\n", plural(len(r.Folders), "folder"))
	for i, fo := range r.Folders {
		fmt.Fprintf(stderr, "  %d  %s (%s, %s)\n", i+1, fo.Name, plural(fo.Files, "file"), size(fo.Bytes))
	}
	n, aerr := strconv.Atoi(m.ask(fmt.Sprintf("The game's folder [1-%d]: ", len(r.Folders))))
	if aerr != nil || n < 1 || n > len(r.Folders) {
		return errors.New("no folder chosen")
	}
	if err := m.run(flow.Extract(o.Source, o.SystemDir, r, r.Folders[n-1].Name)); err != nil {
		return err
	}
	os.Remove(r.From)
	if r.Content != "" {
		os.RemoveAll(filepath.Dir(r.Content))
	}
	return nil
}

// chooseStart sets the program the game starts with: program, else the
// one chosen from the candidates (asked, the likely one the default),
// else the likely one; nothing when the current one is kept.
func (m *maker) chooseStart(source, program, display string) error {
	if program != "" {
		return m.run(flow.Start(source, program, display))
	}
	cs, _, err := start.Candidates(source)
	if err != nil {
		return err
	}
	if len(cs) == 0 {
		return nil
	}
	def := -1
	for i, c := range cs {
		if c.Current {
			def = i
			break
		}
		if c.Likely && def < 0 {
			def = i
		}
	}
	choice := def
	if interactive() {
		fmt.Fprintln(stderr, "Programs the game could start with:")
		for i, c := range cs {
			mark := " "
			if i == def {
				mark = "*"
			}
			line := fmt.Sprintf(" %s%2d  %s", mark, i+1, c.Path)
			if c.Runs != "" {
				line += " (runs " + c.Runs + ")"
			}
			fmt.Fprintln(stderr, line)
		}
		q := fmt.Sprintf("Start with [1-%d", len(cs))
		if def >= 0 {
			q += fmt.Sprintf(", Enter for %d", def+1)
		}
		if a := m.ask(q + "]: "); a != "" {
			n, err := strconv.Atoi(a)
			if err != nil || n < 1 || n > len(cs) {
				return fmt.Errorf("%q: not one of the programs", a)
			}
			choice = n - 1
		}
	}
	if choice < 0 {
		fmt.Fprintln(stderr, "No start program chosen: init's stays (--start sets one)")
		return nil
	}
	if cs[choice].Current && display == "" {
		return nil
	}
	return m.run(flow.Start(source, cs[choice].Path, display))
}
