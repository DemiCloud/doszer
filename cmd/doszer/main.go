// Command doszer builds .dosz archives for DOSBox Pure from DOSZ.TOML
// manifests. It is the reference implementation of the DOSZ Manifest
// Specification.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/pflag"

	"github.com/DemiCloud/doszer/internal/build"
	"github.com/DemiCloud/doszer/internal/builtin"
	"github.com/DemiCloud/doszer/internal/config"
	"github.com/DemiCloud/doszer/internal/dump"
	"github.com/DemiCloud/doszer/internal/env"
	"github.com/DemiCloud/doszer/internal/info"
	"github.com/DemiCloud/doszer/internal/install"
	"github.com/DemiCloud/doszer/internal/interrupt"
	"github.com/DemiCloud/doszer/internal/manifest"
	"github.com/DemiCloud/doszer/internal/output"
	"github.com/DemiCloud/doszer/internal/platform"
	"github.com/DemiCloud/doszer/internal/prepare"
	"github.com/DemiCloud/doszer/internal/redump"
	"github.com/DemiCloud/doszer/internal/repack"
	"github.com/DemiCloud/doszer/internal/status"
	"github.com/DemiCloud/doszer/internal/verify"
)

// errUsage signals that usage was already printed.
var errUsage = errors.New("usage")

// errQuiet signals that the errors were already reported: exit 1 with
// nothing more to say.
var errQuiet = errors.New("failed")

// version is set via -ldflags "-X main.version=..." (see Makefile).
var version = "dev"

// homepage is recorded in every archive's comment (spec §8.4).
const homepage = "https://github.com/demicloud/doszer"

// provenance is what build and repack record in the archive comment:
// this doszer.
func provenance() (*build.Provenance, error) {
	return &build.Provenance{Generator: "doszer " + version, Homepage: homepage}, nil
}

type command struct {
	name    string
	summary string
	run     func(args []string) error
}

var commands []command

func init() {
	commands = []command{
		{"dump", "dump a CD to BIN/CUE into a source directory", runDump},
		{"redump", "show the Redump datfile in use, or check tracks against it", runRedump},
		{"init", "write a commented DOSZ.TOML into a source directory", runInit},
		{"build", "build an archive from a source directory", runBuild},
		{"install", "run an installer and extract the result into a source", runInstall},
		{"run", "play an archive in DOSBox Pure", runRun},
		{"repack", "rebuild an archive from its embedded manifest", runRepack},
		{"set", "change archives' parent, OS image or shell in place", runSet},
		{"prepare", "make a game-ready copy of a Windows shell or OS image", runPrepare},
		{"info", "show what an archive or source directory holds", runInfo},
		{"verify", "check an archive against its manifest and the specification", runVerify},
		{"env", "show the directories, images and tools doszer finds here", runEnv},
		{"version", "show the versions of doszer and the programs it embeds", runVersion},
	}
}

func main() {
	redump.UserAgent = "doszer/" + version
	interrupt.Handle()
	status.Enable(os.Stderr)
	if err := run(windowsPaths(os.Args[1:])); err != nil {
		status.Stop()
		if !errors.Is(err, errUsage) && !errors.Is(err, errQuiet) {
			logError(err)
		}
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		usage(os.Stdout)
		return nil
	}
	if len(args) == 0 {
		usage(os.Stderr)
		return errUsage
	}
	if args[0] == "-V" || args[0] == "--version" {
		fmt.Println("doszer", version)
		return nil
	}

	for _, c := range commands {
		if c.name == args[0] {
			return c.run(args[1:])
		}
	}
	// A file instead of a command is run, so doszer can be the program
	// that opens .dosz files (doszer FILE).
	if fi, err := os.Stat(args[0]); err == nil && !fi.IsDir() {
		return runRun(args)
	}
	if strings.ContainsAny(args[0], `/\.`) {
		return fmt.Errorf("%s: no such file", args[0])
	}

	usage(os.Stderr)
	return fmt.Errorf("unknown command %q", args[0])
}

// windowsPaths converts WSL paths (/mnt/<drive>/...) in args, or in an
// option's =VALUE, to Windows ones when doszer runs on Windows: a
// Windows doszer started from a WSL shell gets the shell's paths, which
// mean nothing to it. Elsewhere args are returned unchanged.
func windowsPaths(args []string) []string {
	if runtime.GOOS != "windows" {
		return args
	}
	out := make([]string, len(args))
	for i, a := range args {
		prefix, p := "", a
		if strings.HasPrefix(a, "--") {
			if k, v, ok := strings.Cut(a, "="); ok {
				prefix, p = k+"=", v
			}
		}
		out[i] = prefix + wslToWindows(p)
	}
	return out
}

// wslToWindows converts /mnt/d/x/y to D:\x\y; any other path is returned
// unchanged.
func wslToWindows(p string) string {
	if len(p) < 6 || !strings.HasPrefix(p, "/mnt/") {
		return p
	}
	d := p[5]
	if !(d >= 'a' && d <= 'z' || d >= 'A' && d <= 'Z') || len(p) > 6 && p[6] != '/' {
		return p
	}
	return strings.ToUpper(string(d)) + `:\` + strings.ReplaceAll(strings.TrimPrefix(p[6:], "/"), "/", `\`)
}

func usage(w io.Writer) {
	fmt.Fprint(w, "doszer - build .dosz archives for DOSBox Pure from DOSZ.TOML manifests\n\n")
	fmt.Fprint(w, "Usage: doszer COMMAND [options] [ARG...]\n       doszer FILE.dosz\n\nCommands:\n")
	for _, c := range commands {
		fmt.Fprintf(w, "  %-8s %s\n", c.name, c.summary)
	}
	fmt.Fprint(w, "\nOptions:\n"+flagHelp+flagVersion)
}

// Option lines shared between commands' help.
const (
	flagHelp       = "  -h, --help              show this help\n"
	flagFormatText = "      --format FMT        output: fancy, plain (default: fancy on a terminal)\n"
	flagFormatData = "      --format FMT        output: fancy, plain, json, yaml, toml, csv (default: fancy on a terminal)\n"
	flagVersion    = "  -V, --version           show version\n"
	flagOggQuality = "      --ogg-quality N     Ogg Vorbis quality, -1 to 10 (default: ogg_quality, else 6)\n"
	flagStrict     = "      --strict            fail on any warning\n"
	flagSaveDir    = "      --save-dir DIR      DOSBox Pure save directory (default: save_dir, else doszer's)\n"
	flagSystemDir  = "      --system-dir DIR    DOSBox Pure system directory (default: system_dir, else ./system, else RetroDECK's)\n"
)

// newFlags returns a command's flag set: GNU-style (pflag), options
// accepted before or after arguments, "--" ending them. pflag's own
// messages are discarded; parse prints the command's help instead.
func newFlags(name string) *pflag.FlagSet {
	fs := pflag.NewFlagSet(name, pflag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.String("format", "", "")
	return fs
}

// dataCommands are the commands with a result --format can encode.
var dataCommands = map[string]bool{"info": true, "verify": true, "set": true, "version": true, "env": true}

// format is the resolved --format of the command running.
var format = output.Plain

// setFormat resolves fs's --format: the spinner only under fancy, and a
// structured format only for a command with a result to encode.
func setFormat(fs *pflag.FlagSet) error {
	v, _ := fs.GetString("format")
	f, err := output.Parse(v)
	if err != nil {
		return err
	}
	if f.Structured() && !dataCommands[fs.Name()] {
		return fmt.Errorf("%s: --format %s: no result to encode (fancy or plain only)", fs.Name(), f)
	}
	format = f.Resolve(os.Stdout)
	switch {
	case format == output.Fancy && f == output.Fancy:
		status.Force(os.Stderr)
	case format != output.Fancy:
		status.Disable()
	}
	return nil
}

// parse parses args into fs, printing help on -h/--help (ok false, no
// error) or when the argument count is outside [min, max].
func parse(fs *pflag.FlagSet, args []string, help string, min, max int) (ok bool, err error) {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, pflag.ErrHelp) {
			fmt.Print(help)
			return false, nil
		}
		return false, err
	}
	if err := setFormat(fs); err != nil {
		return false, err
	}
	if fs.NArg() < min || fs.NArg() > max {
		fmt.Fprint(os.Stderr, help)
		return false, errUsage
	}
	return true, nil
}

// endSteps ends the step a command was showing (status), as completed
// unless err, before the command prints its results.
func endSteps(err error) {
	if err != nil {
		status.Stop()
	} else {
		status.Done()
	}
}

// logError writes a timestamped error to stderr.
func logError(err error) {
	fmt.Fprintf(os.Stderr, "[%s] ERROR: %v\n", time.Now().Format("2006-01-02 15:04:05"), err)
}

// runInit implements `doszer init <source>`.
func runInit(args []string) error {
	fs := newFlags("init")
	var o manifest.TemplateOptions
	fs.StringVar(&o.Exec, "exec", "", "")
	force := fs.BoolP("force", "f", false, "")
	fs.StringVar(&o.Name, "name", "", "")
	noMedia := fs.Bool("no-media", false, "")
	fs.StringVar(&o.OS, "os", "", "")
	fs.StringVar(&o.Shell, "shell", "", "")
	systemDir := fs.String("system-dir", "", "")
	boot := fs.String("boot", "", "")
	fs.StringVar(&o.Audio, "audio", "", "")
	const help = `doszer init - write a commented DOSZ.TOML into a source directory

Usage: doszer init [options] SOURCE

Options:
      --audio FMT         CD audio encoding: ogg or wav (keeps every track verifiable)
      --boot MODEL        boot model: dos, shell or os (default: from the programs)
      --exec PATH         plain model: DOS program to run at startup
  -f, --force             overwrite an existing manifest
      --name NAME         canonical name (default: source directory name)
      --no-media          don't identify disc images in the source
      --os PATH           OS image model: OS image path, without extension
      --shell PATH        shell model: system shell path, without extension
` + flagSystemDir + flagFormatText + flagHelp
	if ok, err := parse(fs, args, help, 1, 1); !ok {
		return err
	}

	source := fs.Arg(0)
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s: not a directory", source)
	}

	if o.Name == "" {
		abs, err := filepath.Abs(source)
		if err != nil {
			return err
		}
		o.Name = filepath.Base(abs)
	}

	if !*noMedia {
		cfg, err := config.Load(source)
		if err != nil {
			return err
		}
		// Redump's datfile, to record which Redump dump each disc is; none
		// (not embedded or cached) just leaves those fields out.
		var db *redump.DB
		if sel, err := redump.Current(cfg.RedumpDAT); err == nil && sel != nil {
			db = sel.DB
		}
		media, skipped, err := build.IdentifyMedia(source, db)
		endSteps(err)
		if err != nil {
			return err
		}
		for _, s := range skipped {
			fmt.Fprintf(os.Stderr, "[WARN] disc image not identified: %s\n", s)
		}
		if len(media) > 0 {
			fmt.Printf("Identified %d disc image(s)\n", len(media))
		}
		for _, m := range media {
			if m.RedumpName != "" {
				fmt.Printf("  %s: matches Redump's %q\n", m.Label, m.RedumpName)
			} else if db != nil {
				fmt.Printf("  %s: no Redump match\n", m.Label)
			}
		}
		o.Media = media
		audio := 0
		for _, m := range media {
			for _, t := range m.Tracks {
				if t.Type == "audio" {
					audio++
				}
			}
		}
		if audio > 0 && o.Audio != "wav" {
			fmt.Printf("%s will be Ogg Vorbis, lossy: verify can't check their hashes (--audio wav keeps them checkable)\n", plural(audio, "audio track"))
		}
	}

	if o.Audio != "" && o.Audio != "ogg" && o.Audio != "wav" {
		return fmt.Errorf("--audio %q: want ogg or wav", o.Audio)
	}
	switch *boot {
	case "", "dos", "shell", "os":
	default:
		return fmt.Errorf("--boot %q: want dos, shell or os", *boot)
	}
	if o.Exec == "" && o.Shell == "" && o.OS == "" {
		cfg, err := config.Load(source)
		if err != nil {
			return err
		}
		if err := chooseBoot(&o, source, stringOr(*systemDir, cfg.SystemDir), *boot, cfg); err != nil {
			return err
		}
	} else if *boot != "" {
		return errors.New("--boot chooses a model when --exec, --shell and --os don't")
	}

	content := manifest.Template(o)

	// The generated manifest must itself be valid.
	if _, err := manifest.Parse([]byte(content), o.Name); err != nil {
		return err
	}

	path := filepath.Join(source, manifest.FileName)
	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if *force {
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}

	f, err := os.OpenFile(path, flags, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%s already exists (use --force to overwrite)", path)
		}
		return err
	}

	if _, err := f.WriteString(content); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	fmt.Printf("Created %s\n", path)
	return nil
}

// runBuild implements `doszer build <source> [<output.dosz>]`.
func runBuild(args []string) error {
	fs := newFlags("build")
	dryRun := fs.BoolP("dry-run", "n", false, "")
	name := fs.String("name", "", "")
	oggQuality := fs.Int("ogg-quality", 0, "")
	strict := fs.Bool("strict", false, "")
	const help = `doszer build - build an archive from a source directory

Usage: doszer build [options] SOURCE [OUTPUT.dosz]

Options:
  -n, --dry-run           print the build plan without writing an archive
      --name NAME         override the manifest's effective name
` + flagOggQuality + flagStrict + flagFormatText + flagHelp
	if ok, err := parse(fs, args, help, 1, 2); !ok {
		return err
	}

	cfg, err := config.Load(fs.Arg(0))
	if err != nil {
		return err
	}
	opts := build.Options{
		SourceDir:  fs.Arg(0),
		Name:       *name,
		DryRun:     *dryRun,
		Strict:     *strict,
		OggQuality: flagOr(fs, "ogg-quality", *oggQuality, cfg.OggQuality),
	}
	if fs.NArg() == 2 {
		opts.OutputPath = fs.Arg(1)
	}
	if opts.Provenance, err = provenance(); err != nil {
		return err
	}

	res, err := build.Run(opts)
	endSteps(err)
	if res != nil {
		printBuildPlan(res, opts.DryRun)
	}
	if err != nil {
		return err
	}

	warnMissingImage(&res.Manifest.Boot, cfg.SystemDir)
	if opts.DryRun {
		fmt.Printf("Dry run: would write %s (%d entries)\n", res.OutputPath, len(res.Entries))
	} else {
		fmt.Printf("Built %s (%d entries)\n", res.OutputPath, len(res.Entries))
	}
	return nil
}

// warnMissingImage warns, on one line, when the shell or OS image boot
// names isn't in the system directory here (configured, else found). It
// never fails anything: archives may be played on another machine. With
// no system directory here, it says nothing.
func warnMissingImage(b *manifest.Boot, systemDir string) {
	name, shell := b.OS, false
	if b.Shell != "" {
		name, shell = b.Shell, true
	}
	if name == "" {
		return
	}
	if systemDir == "" {
		systemDir = install.FindSystemDir()
	}
	if systemDir == "" {
		return
	}
	what := "boot.os"
	if shell {
		what = "boot.shell"
	}
	near := ""
	for _, f := range install.SystemFiles(systemDir) {
		if f.Shell != shell {
			continue
		}
		if f.Name == name {
			return
		}
		if strings.EqualFold(f.Name, name) {
			near = f.Name
		}
	}
	if near != "" {
		status.Printf(os.Stderr, "[WARN] %s %q is %q in %s: DOSBox Pure matches names exactly\n", what, name, near, systemDir)
		return
	}
	status.Printf(os.Stderr, "[WARN] %s %q isn't in %s (fine if the archive is played elsewhere)\n", what, name, systemDir)
}

// printBuildPlan reports what a build did or would do. Trim/flatten/rewrite
// details are --dry-run only; warnings print either way.
func printBuildPlan(res *build.Result, dryRun bool) {
	if dryRun {
		if len(res.Trimmed) > 0 {
			fmt.Printf("Trim removes %d file(s):\n", len(res.Trimmed))
			for _, p := range res.Trimmed {
				fmt.Printf("  - %s\n", p)
			}
		}

		if len(res.Moved) > 0 {
			fmt.Printf("Flatten renames %d path(s):\n", len(res.Moved))
			for _, m := range res.Moved {
				fmt.Printf("  %s -> %s\n", m.From, m.To)
			}
		}

		for _, c := range res.Converted {
			fmt.Printf("Media converts %s to:\n", c.Cue)
			for _, p := range c.Files {
				fmt.Printf("  - %s\n", p)
			}
		}

		for i, rule := range res.Manifest.Layout.Rewrite {
			var files []string
			for _, h := range res.RewriteHits {
				if h.Rule == i {
					files = append(files, h.Path)
				}
			}
			if len(files) > 0 {
				fmt.Printf("Rewrite %q -> %q changes %d file(s):\n", rule.From, rule.To, len(files))
				for _, p := range files {
					fmt.Printf("  - %s\n", p)
				}
			}
		}

		if len(res.Suggestions) > 0 {
			fmt.Println("Suggested rewrite rules, for paths flatten changed:")
			for _, s := range res.Suggestions {
				fmt.Println(s)
			}
		}

		for _, w := range res.BinaryReferences {
			fmt.Fprintf(os.Stderr, "[WARN] %s\n", w)
		}
	}

	for _, w := range res.Warnings() {
		fmt.Fprintf(os.Stderr, "[WARN] %s\n", w)
	}
}

// runVerify implements `doszer verify <file.dosz>`.
func runVerify(args []string) error {
	fs := newFlags("verify")
	all := fs.Bool("all", false, "")
	const help = `doszer verify - check archives against their manifests and the specification

Usage: doszer verify [options] FILE.dosz...
       doszer verify --all [DIR...]

Options:
      --all               check every .dosz under DIR (default: the current directory)
` + flagFormatData + flagHelp
	if ok, err := parse(fs, args, help, 0, 1<<20); !ok {
		return err
	}
	if !*all && fs.NArg() == 0 {
		fmt.Fprint(os.Stderr, help)
		return errUsage
	}
	paths := fs.Args()
	if *all {
		dirs := paths
		if len(dirs) == 0 {
			dirs = []string{"."}
		}
		var err error
		if paths, err = findArchives(dirs); err != nil {
			return err
		}
		if len(paths) == 0 {
			return fmt.Errorf("no .dosz files under %s", strings.Join(dirs, ", "))
		}
	}

	type result struct {
		File     string   `json:"file" yaml:"file" toml:"file"`
		OK       bool     `json:"ok" yaml:"ok" toml:"ok"`
		Errors   []string `json:"errors,omitempty" yaml:"errors,omitempty" toml:"errors,omitempty"`
		Warnings []string `json:"warnings,omitempty" yaml:"warnings,omitempty" toml:"warnings,omitempty"`
	}
	var results []result
	failed := 0
	for _, p := range paths {
		warnings, err := verify.Check(p)
		endSteps(err)
		r := result{File: p, OK: err == nil, Warnings: warnings}
		if err != nil {
			failed++
			r.Errors = errorLines(err)
		}
		results = append(results, r)
		if format.Structured() {
			continue
		}
		prefix := ""
		if len(paths) > 1 {
			prefix = p + ": "
		}
		for _, w := range warnings {
			status.Printf(os.Stderr, "[WARN] %s%s\n", prefix, w)
		}
		if err != nil {
			if len(paths) == 1 {
				return err
			}
			logError(fmt.Errorf("%s: %w", p, err))
			continue
		}
		status.Printf(os.Stdout, "%s conforms to the specification\n", p)
	}

	switch format {
	case output.CSV:
		rows := make([][]string, len(results))
		for i, r := range results {
			rows[i] = []string{r.File, strconv.FormatBool(r.OK), strings.Join(r.Errors, "; "), strings.Join(r.Warnings, "; ")}
		}
		if err := output.EncodeCSV(os.Stdout, []string{"file", "ok", "errors", "warnings"}, rows); err != nil {
			return err
		}
	case output.JSON, output.YAML, output.TOML:
		if err := output.Encode(os.Stdout, format, struct {
			Archives []result `json:"archives" yaml:"archives" toml:"archives"`
		}{results}); err != nil {
			return err
		}
	default:
		if len(paths) > 1 {
			fmt.Printf("%d of %s conform\n", len(paths)-failed, plural(len(paths), "archive"))
		}
	}
	if failed > 0 {
		if format.Structured() || len(paths) > 1 {
			return errQuiet
		}
	}
	return nil
}

// findArchives returns every .dosz file under dirs, sorted.
func findArchives(dirs []string) ([]string, error) {
	var out []string
	for _, dir := range dirs {
		err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.EqualFold(filepath.Ext(p), ".dosz") {
				out = append(out, p)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(out)
	return out, nil
}

// errorLines splits an error joined with errors.Join into its errors.
func errorLines(err error) []string {
	var lines []string
	for _, l := range strings.Split(err.Error(), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

// plural is "1 thing" or "N things".
func plural(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return fmt.Sprintf("%d %ss", n, what)
}

// runDump implements `doszer dump <dir>`.
func runDump(args []string) error {
	fs := newFlags("dump")
	drive := fs.String("drive", "", "")
	dat := fs.String("dat", "", "")
	from := fs.String("from", "", "")
	offset := fs.Int("offset", 0, "")
	const help = `doszer dump - dump a CD to BIN/CUE into a source directory

Usage: doszer dump [options] DIR

Options:
      --dat FILE          datfile to verify against (default: redump_dat, else Redump's)
      --drive DRIVE       drive to read, e.g. E: or /dev/sg1 (default: first drive with a disc)
      --from FILE         import this BIN/CUE image (.cue, or a .zip of one) instead of reading a drive
      --offset SAMPLES    --from: drive read offset the image's audio wasn't corrected for
` + flagFormatText + flagHelp
	if ok, err := parse(fs, args, help, 1, 1); !ok {
		return err
	}
	cfg, err := config.Load("")
	if err != nil {
		return err
	}
	dat2 := stringOr(*dat, cfg.RedumpDAT)
	if *from != "" {
		if *drive != "" {
			return errors.New("--from and --drive exclude each other")
		}
		status.Step("Copying the image's tracks")
		res, err := dump.Import(*from, fs.Arg(0), *offset)
		endSteps(err)
		if err != nil {
			return err
		}
		fmt.Printf("Imported %s (%d track file(s))\n", res.Cue, len(res.Tracks))
		return verifyImage(res.Cue, 0, dat2, false)
	}
	if *offset != 0 {
		return errors.New("--offset needs --from")
	}
	res, err := dump.Run(dump.Options{Dir: fs.Arg(0), Drive: *drive})
	if res != nil && res.Log != "" {
		fmt.Fprintf(os.Stderr, "redumper log: %s\n", res.Log)
	}
	if err != nil {
		return err
	}
	fmt.Printf("Dumped %s (%d track file(s))\n", res.Cue, len(res.Tracks))
	for _, e := range res.Errors {
		fmt.Fprintf(os.Stderr, "[WARN] redumper: %s (left as read: zeros)\n", e)
	}
	return verifyImage(res.Cue, 0, dat2, len(res.Errors) > 0)
}

// verifyImage reports whether a disc image's tracks, in Redump's layout
// (redump.OpenImage, correcting offset), match a Redump disc.
func verifyImage(cuePath string, offset int, dat string, readErrors bool) error {
	return verifyTracks(func() ([]redump.Track, error) {
		img, err := redump.OpenImage(cuePath, offset)
		if err != nil {
			return nil, err
		}
		defer img.Close()
		return img.Hash()
	}, dat, readErrors)
}

// verifyArchive checks the disc images in an archive against Redump: data
// tracks rebuilt as raw Mode 1 sectors, WAV audio as it is, a PREGAP's
// silence as zeros (redump.ArchiveImages).
func verifyArchive(p, dat string) error {
	images, skipped, closeAll, err := redump.ArchiveImages(p)
	if err != nil {
		return err
	}
	defer closeAll()
	for _, s := range skipped {
		fmt.Fprintf(os.Stderr, "[WARN] %s: %s\n", filepath.Base(p), s)
	}
	if len(images) == 0 && len(skipped) == 0 {
		fmt.Printf("%s: no disc images\n", filepath.Base(p))
	}
	for _, img := range images {
		fmt.Printf("%s: %s\n", filepath.Base(p), img.Cue)
		if err := verifyTracks(img.Hash, dat, false); err != nil {
			return err
		}
	}
	return nil
}

// verifyTracks reports whether a dump's tracks match a Redump disc. A
// dump that doesn't is reported, not an error: it may be a disc nobody
// has submitted yet.
func verifyTracks(hash func() ([]redump.Track, error), dat string, readErrors bool) error {
	status.Step("Checking the dump against Redump")
	sel, err := redump.Select(dat)
	if err != nil {
		endSteps(err)
		return err
	}
	tracks, err := hash()
	endSteps(err)
	if err != nil {
		return err
	}
	if sel.Note != "" {
		fmt.Fprintf(os.Stderr, "[WARN] %s\n", sel.Note)
	}
	src := fmt.Sprintf("%s datfile %s", sel.Source, sel.DB.Version)
	m, err := sel.DB.Match(tracks)
	switch {
	case errors.Is(err, redump.ErrNoMatch):
		fmt.Printf("Not verified: no track matches a Redump disc (%s): a bad dump, or a disc not in Redump yet\n", src)
	case err != nil:
		return err
	case m.Verified:
		fmt.Printf("Verified: every track matches Redump's %q (%s)\n  %s\n", m.Disc.Name, src, m.Link)
	default:
		var differ []string
		for _, i := range m.Differ {
			differ = append(differ, filepath.Base(tracks[i].Path))
		}
		fmt.Printf("Not verified: closest Redump disc %q matches %d of its %d track(s) (%s)\n  %s\n", m.Disc.Name, m.Matching, len(m.Disc.Tracks), src, m.Link)
		if len(differ) > 0 {
			fmt.Printf("  differing: %s\n", strings.Join(differ, ", "))
		}
		if readErrors {
			fmt.Println("  the drive couldn't read some samples (see the warnings above), which likely explains it")
		}
	}
	return nil
}

// runRedump implements `doszer redump [SHA1|FILE...]`.
func runRedump(args []string) error {
	fs := newFlags("redump")
	dat := fs.String("dat", "", "")
	update := fs.Bool("update", false, "")
	offset := fs.Int("offset", 0, "")
	const help = `doszer redump - show the Redump datfile in use, or check tracks against it

Usage: doszer redump [options] [SHA1|FILE|IMAGE|FILE.dosz...]

Options:
      --dat FILE          datfile to use (default: redump_dat, else Redump's)
      --offset SAMPLES    drive read offset a CUE image's audio wasn't corrected for
      --update            fetch Redump's current datfile now
` + flagFormatText + flagHelp
	if ok, err := parse(fs, args, help, 0, 1<<20); !ok {
		return err
	}
	cfg, err := config.Load("")
	if err != nil {
		return err
	}
	external := stringOr(*dat, cfg.RedumpDAT)
	if *update {
		status.Step("Fetching Redump's datfile")
		db, err := redump.Update()
		endSteps(err)
		if err != nil {
			return err
		}
		fmt.Printf("Cached Redump datfile %s (%d discs)\n", db.Version, len(db.Discs))
	}
	if fs.NArg() == 0 {
		return redumpStatus(external)
	}

	var shas, files, cues, archives []string
	for _, a := range fs.Args() {
		switch {
		case len(a) == 40 && isHex(a):
			shas = append(shas, a)
		case strings.EqualFold(filepath.Ext(a), ".cue"), strings.EqualFold(filepath.Ext(a), ".zip"):
			cues = append(cues, a)
		case strings.EqualFold(filepath.Ext(a), ".dosz"):
			archives = append(archives, a)
		default:
			files = append(files, a)
		}
	}
	if len(shas) > 0 {
		sel, err := redump.Select(external)
		if err != nil {
			return err
		}
		for _, s := range shas {
			hits := sel.DB.Lookup(s)
			if len(hits) == 0 {
				fmt.Printf("%s: not in Redump\n", s)
			}
			for _, h := range hits {
				fmt.Printf("%s: %s, track %d (%d bytes)\n", s, h.Disc, h.Track, h.Size)
			}
		}
	}
	for _, c := range cues {
		if err := verifyImage(c, *offset, external, false); err != nil {
			return err
		}
	}
	for _, a := range archives {
		if err := verifyArchive(a, external); err != nil {
			return err
		}
	}
	if len(files) > 0 {
		return verifyTracks(func() ([]redump.Track, error) { return redump.HashTracks(files) }, external, false)
	}
	return nil
}

func redumpStatus(external string) error {
	emb, err := redump.Embedded()
	if err != nil {
		return err
	}
	if emb != nil {
		fmt.Printf("Embedded: Redump datfile %s (%d discs)\n", emb.Version, len(emb.Discs))
	} else {
		fmt.Println("Embedded: none (built without one: make dat)")
	}
	cached, checked, err := redump.Cached()
	switch {
	case err != nil:
		fmt.Printf("Cached:   unreadable (%v)\n", err)
	case cached != nil:
		fmt.Printf("Cached:   Redump datfile %s (%d discs), last fetched or checked %s\n", cached.Version, len(cached.Discs), checked.Format("2006-01-02 15:04"))
	default:
		fmt.Println("Cached:   none yet (fetched on the first dump, or with --update)")
	}
	if external != "" {
		fmt.Printf("External: %s\n", external)
	}
	cur, err := redump.Current(external)
	if err != nil {
		return err
	}
	if cur == nil {
		fmt.Println("In use:   none")
	} else {
		fmt.Printf("In use:   %s (Redump datfile %s)\n", cur.Source, cur.DB.Version)
	}
	return nil
}

func isHex(s string) bool {
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return false
		}
	}
	return true
}

// runInstall implements `doszer install <source>`.
func runInstall(args []string) error {
	fs := newFlags("install")
	content := fs.String("content", "", "")
	dir := fs.String("dir", "", "")
	drive := fs.String("drive", "", "")
	dryRun := fs.BoolP("dry-run", "n", false, "")
	force := fs.BoolP("force", "f", false, "")
	from := fs.String("from", "", "")
	keep := fs.Bool("keep", false, "")
	osImage := fs.String("os", "", "")
	saveDir := fs.String("save-dir", "", "")
	systemDir := fs.String("system-dir", "", "")
	template := fs.String("template", "", "")
	const help = `doszer install - run an installer and extract the result into a source

Usage: doszer install [options] SOURCE

Options:
      --content FILE      archive a --from save was made over
      --dir PATH          folder to extract, relative to the drive root
      --drive LETTER      OS image session: drive to extract from, c or d (default: d)
  -n, --dry-run           report without writing
  -f, --force             overwrite differing files in the source
      --from FILE         extract from this .sav, .pure.zip, .vhd or .img instead
      --keep              keep the session's save files
      --os PATH           OS image to boot instead of boot.os (default: install_os, else boot.os)
` + flagSaveDir + flagSystemDir + `      --template FILE     pristine image to compare a --from image against
` + flagFormatText + flagHelp
	if ok, err := parse(fs, args, help, 1, 1); !ok {
		return err
	}

	cfg, err := config.Load(fs.Arg(0))
	if err != nil {
		return err
	}
	opts := install.Options{
		Source:    fs.Arg(0),
		SystemDir: stringOr(*systemDir, cfg.SystemDir),
		SaveDir:   stringOr(*saveDir, cfg.SaveDir),
		Dir:       *dir,
		Drive:     *drive,
		OS:        *osImage,
		InstallOS: cfg.InstallOS,
		Force:     *force,
		DryRun:    *dryRun,
		Keep:      *keep,
		From:      *from,
		Content:   *content,
		Template:  *template,
		Templates: cfg.Templates,
	}

	if opts.From == "" {
		fmt.Fprintln(os.Stderr, windowHelp)
	}
	rep, err := install.Run(opts)
	endSteps(err)
	if rep != nil {
		printInstallReport(rep, opts.DryRun)
	}
	return err
}

// runVersion implements `doszer version`.
func runVersion(args []string) error {
	fs := newFlags("version")
	const help = `doszer version - show the versions of doszer and the programs it embeds

Usage: doszer version

Options:
` + flagFormatData + flagHelp
	if ok, err := parse(fs, args, help, 0, 0); !ok {
		return err
	}
	if format.Structured() {
		type program struct {
			Name     string `json:"name" yaml:"name" toml:"name"`
			Version  string `json:"version,omitempty" yaml:"version,omitempty" toml:"version,omitempty"`
			URL      string `json:"url" yaml:"url" toml:"url"`
			Embedded bool   `json:"embedded" yaml:"embedded" toml:"embedded"`
		}
		progs := []program{{"doszer", version, homepage, true}}
		for _, p := range builtin.Programs() {
			progs = append(progs, program{p.Name, p.Version, p.URL, p.Embedded})
		}
		if format == output.CSV {
			rows := make([][]string, len(progs))
			for i, p := range progs {
				rows[i] = []string{p.Name, p.Version, p.URL, strconv.FormatBool(p.Embedded)}
			}
			return output.EncodeCSV(os.Stdout, []string{"name", "version", "url", "embedded"}, rows)
		}
		return output.Encode(os.Stdout, format, struct {
			Programs []program `json:"programs" yaml:"programs" toml:"programs"`
		}{progs})
	}
	fmt.Println("doszer", version)
	for _, p := range builtin.Programs() {
		v := p.Version
		if !p.Embedded {
			v = "not embedded"
		} else if v == "" {
			v = "unknown version"
		}
		fmt.Printf("  %-12s %-16s %s\n", p.Name, v, p.URL)
	}
	return nil
}

// windowHelp is printed when DOSBox Pure's window opens.
const windowHelp = "Click the window to capture the mouse, Ctrl+F10 releases it. Alt+Pause: pause, Alt+Enter: fullscreen, hold Alt+F12: fast-forward, Ctrl+F5: screenshot."

// runRun implements `doszer run <archive.dosz>`.
func runRun(args []string) error {
	fs := newFlags("run")
	options := fs.StringArray("option", nil, "")
	saveDir := fs.String("save-dir", "", "")
	systemDir := fs.String("system-dir", "", "")
	const help = `doszer run - play an archive in DOSBox Pure

Usage: doszer run [options] ARCHIVE.dosz

Options:
      --option KEY=VALUE  DOSBox Pure core option for this session (repeatable)
      --save-dir DIR      DOSBox Pure save directory (default: save_dir, else ./saves, else RetroDECK's)
` + flagSystemDir + flagFormatText + flagHelp
	if ok, err := parse(fs, args, help, 1, 1); !ok {
		return err
	}
	opts := map[string]string{}
	for _, o := range *options {
		k, v, ok := strings.Cut(o, "=")
		if !ok || k == "" {
			return fmt.Errorf("--option %s: want KEY=VALUE", o)
		}
		opts[k] = v
	}
	cfg, err := config.Load("")
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, windowHelp)
	status.Step("Playing %s", filepath.Base(fs.Arg(0)))
	env, err := install.Play(fs.Arg(0), stringOr(*systemDir, cfg.SystemDir), stringOr(*saveDir, cfg.SaveDir), opts)
	endSteps(err)
	if err == nil {
		fmt.Printf("Saves: %s\n", env.SaveDir)
	}
	return err
}

func stringOr(flagValue, configured string) string {
	if flagValue != "" {
		return flagValue
	}
	return configured
}

// printInstallReport summarizes what install found and extracted.
func printInstallReport(rep *install.Report, dryRun bool) {
	for _, p := range rep.Skipped {
		fmt.Fprintf(os.Stderr, "[WARN] %s: left out of the install session\n", p)
	}
	if len(rep.Changes) > 0 {
		fmt.Printf("%s %d file(s) changed\n", rep.Drive, len(rep.Changes))
	} else if rep.Drive != "" {
		fmt.Printf("No files changed on %s (a file saved with the same content doesn't count): nothing to extract.\n", rep.Drive)
		fmt.Println("A game that comes installed needs no install: build it as it is.")
	}
	if x := rep.Extracted; x != nil {
		verb := "Extracted"
		if dryRun {
			verb = "Would extract"
		}
		fmt.Printf("%s %d file(s) from %s\\%s", verb, len(x.Written), rep.Drive, strings.ReplaceAll(rep.Dir, "/", "\\"))
		if len(x.Unchanged) > 0 {
			fmt.Printf(" (%d already identical)", len(x.Unchanged))
		}
		fmt.Println()
		if dryRun {
			for _, p := range x.Written {
				fmt.Printf("  + %s\n", p)
			}
		}
	}
	var outside []install.Change
	for _, c := range rep.Changes {
		if rep.Extracted != nil && (c.Kind == install.Deleted || !inDir(c.Path, rep.Dir)) {
			outside = append(outside, c)
		}
	}
	if len(outside) > 0 {
		fmt.Printf("Other changes on %s (not extracted):\n", rep.Drive)
		for _, c := range outside {
			fmt.Printf("  %-8s %s\n", c.Kind, c.Path)
		}
	}
	if len(rep.Other) > 0 {
		fmt.Printf("C: (OS image, not extracted): %d file(s) changed\n", len(rep.Other))
		for _, c := range rep.Other {
			fmt.Printf("  %-8s %s\n", c.Kind, c.Path)
		}
	}
	if len(rep.Programs) > 0 {
		fmt.Println("Programs installed:")
		for _, p := range rep.Programs {
			fmt.Printf("  %s\n", p)
		}
		switch {
		case rep.Exec != "" && dryRun:
			fmt.Printf("Would set boot.exec = '%s'\n", rep.Exec)
		case rep.Exec != "":
			fmt.Printf("Set boot.exec = '%s' in %s\n", rep.Exec, manifest.FileName)
		}
	}
	if len(rep.DLLs) > 0 {
		verb := "Copied"
		if dryRun {
			verb = "Would copy"
		}
		fmt.Printf("%s %d DLL(s) the game imports from Windows' folders beside it:\n", verb, len(rep.DLLs))
		for _, d := range rep.DLLs {
			fmt.Printf("  %s (from %s)\n", d.To, install.DOSPath("C:", d.From))
		}
	}
	if rep.Registry != "" {
		verb := "Wrote"
		if dryRun {
			verb = "Would write"
		}
		fmt.Printf("%s %d registry value(s) the installer added to %s\n", verb, rep.RegistryValues, rep.Registry)
		fmt.Printf("To restore them at startup, WINDOWS.BAT needs: REGEDIT /S %s\n", rep.Registry)
	}
	for _, p := range rep.Kept {
		fmt.Printf("Save file kept: %s\n", p)
	}
}

func inDir(p, dir string) bool {
	p, dir = strings.ToLower(p), strings.ToLower(dir)
	return dir == "" || p == dir || strings.HasPrefix(p, dir+"/")
}

// runRepack implements `doszer repack <input.dosz> [<output.dosz>]`.
func runRepack(args []string) error {
	fs := newFlags("repack")
	dryRun := fs.BoolP("dry-run", "n", false, "")
	manifestPath := fs.String("manifest", "", "")
	oggQuality := fs.Int("ogg-quality", 0, "")
	strict := fs.Bool("strict", false, "")
	const help = `doszer repack - rebuild an archive from its embedded manifest

Usage: doszer repack [options] INPUT.dosz [OUTPUT.dosz]

Options:
  -n, --dry-run           print the rebuild plan without writing an archive
      --manifest FILE     use this manifest instead of the archive's embedded one
` + flagOggQuality + flagStrict + flagFormatText + flagHelp
	if ok, err := parse(fs, args, help, 1, 2); !ok {
		return err
	}

	cfg, err := config.Load("")
	if err != nil {
		return err
	}
	opts := repack.Options{
		InputPath:    fs.Arg(0),
		DryRun:       *dryRun,
		Strict:       *strict,
		ManifestPath: *manifestPath,
		OggQuality:   flagOr(fs, "ogg-quality", *oggQuality, cfg.OggQuality),
	}
	if fs.NArg() == 2 {
		opts.OutputPath = fs.Arg(1)
	}
	if opts.Provenance, err = provenance(); err != nil {
		return err
	}

	res, err := repack.Run(opts)
	endSteps(err)
	if res != nil {
		printBuildPlan(res, opts.DryRun)
	}
	if err != nil {
		return err
	}

	warnMissingImage(&res.Manifest.Boot, cfg.SystemDir)
	if opts.DryRun {
		fmt.Printf("Dry run: would write %s (%d entries)\n", res.OutputPath, len(res.Entries))
	} else {
		fmt.Printf("Repacked %s (%d entries)\n", res.OutputPath, len(res.Entries))
	}
	return nil
}

// flagOr returns the flag's value if it was given on the command line,
// else the configured value: flags are configuration's highest layer.
func flagOr(fs *pflag.FlagSet, name string, flagValue, configured int) int {
	if fs.Changed(name) {
		return flagValue
	}
	return configured
}

func runPrepare(args []string) error {
	fs := newFlags("prepare")
	dryRun := fs.BoolP("dry-run", "n", false, "")
	force := fs.BoolP("force", "f", false, "")
	add := fs.StringArray("add", nil, "")
	const help = `doszer prepare - make a game-ready copy of a Windows shell or OS image

Usage: doszer prepare shell [options] SHELL OUTPUT
       doszer prepare os [options] IMAGE OUTPUT

Options:
      --add DIR           lay DIR's files over the result (repeatable)
  -n, --dry-run           print the changes without writing OUTPUT
  -f, --force             overwrite OUTPUT
` + flagFormatText + flagHelp
	if ok, err := parse(fs, args, help, 3, 3); !ok {
		return err
	}
	var rep *prepare.Report
	var err error
	switch fs.Arg(0) {
	case "shell":
		rep, err = prepare.Shell(prepare.ShellOptions{
			Input: fs.Arg(1), Output: fs.Arg(2), Add: *add, DryRun: *dryRun, Force: *force,
		})
	case "os":
		rep, err = prepare.OS(prepare.OSOptions{
			Input: fs.Arg(1), Output: fs.Arg(2), Add: *add, DryRun: *dryRun, Force: *force,
		})
	default:
		fmt.Fprint(os.Stderr, help)
		return fmt.Errorf("unknown prepare target %q", fs.Arg(0))
	}
	endSteps(err)
	if err != nil {
		return err
	}
	for _, c := range rep.Changes {
		fmt.Printf("  %-8s  %s (%s)\n", c.Action, strings.ReplaceAll(c.Path, "/", `\`), c.Why)
	}
	if *dryRun {
		fmt.Printf("Dry run: would write %s (%d entries)\n", rep.Output, rep.Entries)
	} else {
		fmt.Printf("Prepared %s (%d entries)\n", rep.Output, rep.Entries)
	}
	return nil
}

func runEnv(args []string) error {
	fs := newFlags("env")
	systemDir := fs.String("system-dir", "", "")
	saveDir := fs.String("save-dir", "", "")
	const help = `doszer env - show the directories, images and tools doszer finds here

Usage: doszer env [options]

Options:
` + flagSystemDir + flagSaveDir + flagFormatData + flagHelp
	if ok, err := parse(fs, args, help, 0, 0); !ok {
		return err
	}
	cfg, err := config.Load("")
	if err != nil {
		return err
	}
	r, err := env.Read(cfg, *systemDir, *saveDir)
	if err != nil {
		return err
	}
	switch format {
	case output.CSV:
		var rows [][]string
		add := func(k, v string) { rows = append(rows, []string{k, v}) }
		add("system_dir", r.SystemDir.Path)
		add("system_dir_from", r.SystemDir.From)
		add("save_dir", r.SaveDir.Path)
		add("save_dir_from", r.SaveDir.From)
		for _, i := range r.Shells {
			add("shell", i.Name)
		}
		for _, i := range r.Images {
			add("image", i.Name+" ("+i.Kind+")")
		}
		for _, s := range r.Settings {
			add(s.Key, s.Value)
		}
		add("ffmpeg", r.FFmpeg)
		add("cache", r.Cache)
		return output.EncodeCSV(os.Stdout, []string{"key", "value"}, rows)
	case output.JSON, output.YAML, output.TOML:
		return output.Encode(os.Stdout, format, r)
	}
	env.Print(os.Stdout, r)
	return nil
}

func runInfo(args []string) error {
	fs := newFlags("info")
	const help = `doszer info - show what an archive or source directory holds

Usage: doszer info FILE.dosz|SOURCE...

Options:
` + flagFormatData + flagHelp
	if ok, err := parse(fs, args, help, 1, 1<<20); !ok {
		return err
	}
	var data []info.Data
	for i, p := range fs.Args() {
		status.Step("Reading %s", filepath.Base(p))
		read := info.Read
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			read = info.ReadSource
		}
		r, err := read(p)
		endSteps(err)
		if err != nil {
			return err
		}
		if format.Structured() {
			data = append(data, r.Data())
			continue
		}
		if i > 0 {
			fmt.Println()
		}
		info.Print(os.Stdout, r)
	}
	switch format {
	case output.CSV:
		rows := make([][]string, len(data))
		for i, d := range data {
			rows[i] = d.CSVRow()
		}
		return output.EncodeCSV(os.Stdout, info.CSVHeader, rows)
	case output.JSON, output.YAML, output.TOML:
		return output.Encode(os.Stdout, format, struct {
			Archives []info.Data `json:"archives" yaml:"archives" toml:"archives"`
		}{data})
	}
	return nil
}

func runSet(args []string) error {
	fs := newFlags("set")
	dryRun := fs.BoolP("dry-run", "n", false, "")
	parent := fs.String("parent", "", "")
	noParent := fs.Bool("no-parent", false, "")
	osName := fs.String("os", "", "")
	shell := fs.String("shell", "", "")
	const help = `doszer set - change archives' parent, OS image or shell in place

Usage: doszer set [options] FILE.dosz...

Options:
  -n, --dry-run           print the changes without writing
      --parent [OLD:]NEW  set the parent archive, only where it is OLD
      --no-parent         remove the parent
      --os [OLD:]NEW      set the OS image, only where it is OLD
      --shell [OLD:]NEW   set the shell, only where it is OLD
` + flagFormatData + flagHelp
	if ok, err := parse(fs, args, help, 1, 1<<20); !ok {
		return err
	}
	opts := repack.SetOptions{DryRun: *dryRun}
	if fs.Changed("parent") && *noParent {
		return errors.New("--parent and --no-parent are exclusive")
	}
	if fs.Changed("parent") {
		opts.Parent = new(repack.Change)
		*opts.Parent = repack.ParseChange(*parent)
		if opts.Parent.To == "" {
			return errors.New("--parent: empty name (--no-parent removes it)")
		}
	}
	if *noParent {
		opts.Parent = &repack.Change{}
	}
	for _, f := range []struct {
		name string
		val  *string
		dst  **repack.Change
	}{{"os", osName, &opts.OS}, {"shell", shell, &opts.Shell}} {
		if !fs.Changed(f.name) {
			continue
		}
		c := repack.ParseChange(*f.val)
		if c.To == "" {
			return fmt.Errorf("--%s: empty name", f.name)
		}
		*f.dst = &c
	}
	show := opts.Parent == nil && opts.OS == nil && opts.Shell == nil
	var err error
	if opts.Provenance, err = provenance(); err != nil {
		return err
	}
	cfg, err := config.Load("")
	if err != nil {
		return err
	}
	warned := map[string]bool{}

	type result struct {
		File    string        `json:"file" yaml:"file" toml:"file"`
		Changed bool          `json:"changed" yaml:"changed" toml:"changed"`
		Before  manifest.Boot `json:"before" yaml:"before" toml:"before"`
		After   manifest.Boot `json:"after" yaml:"after" toml:"after"`
		Skipped []string      `json:"skipped,omitempty" yaml:"skipped,omitempty" toml:"skipped,omitempty"`
		Error   string        `json:"error,omitempty" yaml:"error,omitempty" toml:"error,omitempty"`
	}
	var results []result
	failed := 0
	for _, p := range fs.Args() {
		name := filepath.Base(p)
		o := opts
		o.Path = p
		if show {
			o.DryRun = true
		}
		res, err := repack.Set(o)
		if err == nil && res.Changed() && !show && !format.Structured() {
			k := res.After.Shell + "\x00" + res.After.OS
			if !warned[k] {
				warned[k] = true
				defer warnMissingImage(&res.After, cfg.SystemDir)
			}
		}
		if format.Structured() {
			r := result{File: p}
			if err != nil {
				failed++
				r.Error = err.Error()
			} else {
				r.Changed, r.Before, r.After, r.Skipped = res.Changed() && !show, res.Before, res.After, res.Skipped
			}
			results = append(results, r)
			continue
		}
		if err != nil {
			logError(err)
			failed++
			continue
		}
		switch {
		case show:
			fmt.Printf("%s: %s\n", name, describeBoot(res.Before))
		case res.Changed():
			verb := "Changed"
			if opts.DryRun {
				verb = "Would change"
			}
			fmt.Printf("%s %s: %s, was %s\n", verb, name, describeBoot(res.After), describeBoot(res.Before))
		default:
			why := "already set"
			if len(res.Skipped) > 0 {
				why = strings.Join(res.Skipped, "; ")
			}
			fmt.Printf("Left %s: %s\n", name, why)
		}
	}
	switch format {
	case output.CSV:
		rows := make([][]string, len(results))
		for i, r := range results {
			rows[i] = []string{r.File, strconv.FormatBool(r.Changed), r.After.Exec, r.After.Shell, r.After.OS, r.After.Parent, r.Before.Shell, r.Before.OS, r.Before.Parent, strings.Join(r.Skipped, "; "), r.Error}
		}
		if err := output.EncodeCSV(os.Stdout, []string{"file", "changed", "exec", "shell", "os", "parent", "was_shell", "was_os", "was_parent", "skipped", "error"}, rows); err != nil {
			return err
		}
	case output.JSON, output.YAML, output.TOML:
		if err := output.Encode(os.Stdout, format, struct {
			Archives []result `json:"archives" yaml:"archives" toml:"archives"`
		}{results}); err != nil {
			return err
		}
	}
	if failed > 0 {
		if format.Structured() {
			return errQuiet
		}
		return fmt.Errorf("%d of %d archives failed", failed, fs.NArg())
	}
	return nil
}

// describeBoot is a boot section in a few words, for set's output.
func describeBoot(b manifest.Boot) string {
	var parts []string
	switch {
	case b.OS != "":
		parts = append(parts, "OS image "+b.OS)
	case b.Shell != "":
		parts = append(parts, "shell "+b.Shell)
	case b.Exec != "":
		parts = append(parts, "starts "+b.Exec)
	default:
		parts = append(parts, "start menu")
	}
	if b.Parent != "" {
		parts = append(parts, "parent "+b.Parent)
	}
	return strings.Join(parts, ", ")
}

// chooseBoot sets init's boot model from what the source's programs need
// (platform.Needs), or the model boot forces ("dos", "shell", "os"): DOS
// for a DOS game or one with a DOS version, a shell for Windows 3.x, an
// OS image for Windows 9x. The shell or image is default_shell or
// default_os if configured, else the first well-known name found in the
// system directory here, which init says, since the archive may be played
// elsewhere. A DOS game with exactly one program to start gets exec. It
// prints one line saying what it chose and why.
func chooseBoot(o *manifest.TemplateOptions, source, systemDir, boot string, cfg config.Config) error {
	status.Step("Looking at the programs")
	progs, err := platform.Scan(source)
	endSteps(err)
	if err != nil {
		return err
	}
	need := platform.Needs(progs)
	game := need.Game
	if systemDir == "" {
		systemDir = install.FindSystemDir()
	}
	why := fmt.Sprintf("The game needs %s (%d programs found)", need, len(progs))
	switch {
	case boot != "":
		why = fmt.Sprintf("--boot %s (the game needs %s)", boot, need)
	case game.Platform == platform.NotProgram:
		why = "No programs found"
	case need.Setup.Platform != platform.NotProgram:
		why += fmt.Sprintf(" (setup: %s)", need.Setup)
	}
	win98 := platform.Target{Platform: platform.Win32, Major: 4, Minor: 10}
	pick := func(key, configured string, shell bool, names []string) (string, string) {
		if configured != "" {
			return configured, key + " = " + configured + " (default_" + key + ")"
		}
		if p := install.FindSystemFile(systemDir, shell, names); p != "" {
			return p, key + " = " + p + " (found here; default_" + key + " picks one everywhere)"
		}
		msg := "no " + key + " found here: set boot." + key + " in the manifest, --" + key + " or default_" + key
		if !shell && game.Platform == platform.Win32 && !game.Less(win98) {
			if p := install.FindSystemFile(systemDir, false, install.Win95Names); p != "" {
				msg += " (" + p + " is Windows 95, too old)"
			}
		}
		return "", msg
	}
	model := boot
	if model == "" {
		switch {
		case need.DOS, game.Platform == platform.DOS, game.Platform == platform.NotProgram:
			model = "dos"
		case game.Platform == platform.Win16:
			model = "shell"
		case game.Platform == platform.Win32:
			model = "os"
		case game.Platform == platform.OS2:
			fmt.Printf("%s: no boot model for OS/2\n", why)
			return nil
		}
	}
	var msg string
	switch model {
	case "dos":
		msg = "plain DOS model"
		if need.DOS && boot == "" {
			key := "--shell"
			if game.Platform == platform.Win32 {
				key = "--os"
			}
			msg += " (" + key + " for the Windows version)"
		}
		starters := platform.Starters(progs)
		start := ""
		switch {
		case len(starters) == 1:
			start = starters[0].Path
		case len(starters) > 1:
			if p := platform.Likely(starters, o.Name); p != nil {
				start = p.Path
			}
		}
		via := ""
		if start != "" {
			if bat := platform.Launcher(source, start); bat != "" {
				start, via = bat, path.Base(start)
			}
		}
		if p, ok := platform.DOSPath(start); ok && start != "" {
			o.Exec = p
			msg += ", exec = " + p
			var notes []string
			if via != "" {
				notes = append(notes, "runs "+strings.ToUpper(via))
			}
			if len(starters) > 1 {
				notes = append(notes, fmt.Sprintf("named like the game, of %d programs", len(starters)))
			}
			if len(notes) > 0 {
				msg += " (" + strings.Join(notes, "; ") + ")"
			}
			break
		}
		switch {
		case len(starters) > 1 && len(starters) <= 5:
			var names []string
			for _, p := range starters {
				names = append(names, p.Path)
			}
			msg += "; " + strings.Join(names, ", ") + " could start it (--exec)"
		}
	case "shell":
		o.Shell, msg = pick("shell", cfg.DefaultShell, true, install.ShellNames)
	case "os":
		names := install.Win95Names
		if game.Platform == platform.Win32 && !game.Less(win98) {
			names = install.Win98Names // a Windows 95 image won't do
		}
		o.OS, msg = pick("os", cfg.DefaultOS, false, names)
	}
	fmt.Printf("%s: %s\n", why, msg)
	if winME := (platform.Target{Platform: platform.Win32, Major: 4, Minor: 90}); model == "os" && game.Platform == platform.Win32 && winME.Less(game) {
		fmt.Fprintf(os.Stderr, "[WARN] %s programs may not run under Windows 9x\n", game)
	}
	return nil
}
