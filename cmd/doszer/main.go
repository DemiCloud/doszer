// Command doszer builds .dosz archives for DOSBox Pure from DOSZ.TOML
// manifests. It is the reference implementation of the DOSZ Manifest
// Specification.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/pflag"

	"github.com/DemiCloud/doszer/internal/build"
	"github.com/DemiCloud/doszer/internal/builtin"
	"github.com/DemiCloud/doszer/internal/config"
	"github.com/DemiCloud/doszer/internal/dump"
	"github.com/DemiCloud/doszer/internal/install"
	"github.com/DemiCloud/doszer/internal/interrupt"
	"github.com/DemiCloud/doszer/internal/manifest"
	"github.com/DemiCloud/doszer/internal/prepare"
	"github.com/DemiCloud/doszer/internal/redump"
	"github.com/DemiCloud/doszer/internal/repack"
	"github.com/DemiCloud/doszer/internal/status"
	"github.com/DemiCloud/doszer/internal/verify"
)

// errUsage signals that usage was already printed.
var errUsage = errors.New("usage")

// version is set via -ldflags "-X main.version=..." (see Makefile).
var version = "dev"

// homepage is recorded in every archive's comment (spec §8.4).
const homepage = "https://github.com/demicloud/doszer"

// provenance is what build and repack record in the archive comment: this
// doszer and today's date in UTC, or SOURCE_DATE_EPOCH's
// (reproducible-builds.org) if set.
func provenance() (*build.Provenance, error) {
	built := time.Now()
	if s := os.Getenv("SOURCE_DATE_EPOCH"); s != "" {
		secs, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("SOURCE_DATE_EPOCH: %q is not a number of seconds", s)
		}
		built = time.Unix(secs, 0)
	}
	return &build.Provenance{Generator: "doszer " + version, Homepage: homepage, Built: built}, nil
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
		{"prepare", "make a game-ready copy of a Windows 3.x shell", runPrepare},
		{"verify", "check an archive against its manifest and the specification", runVerify},
		{"version", "show the versions of doszer and the programs it embeds", runVersion},
	}
}

func main() {
	redump.UserAgent = "doszer/" + version
	interrupt.Handle()
	status.Enable(os.Stderr)
	if err := run(windowsPaths(os.Args[1:])); err != nil {
		status.Stop()
		if !errors.Is(err, errUsage) {
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
	return fs
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
	const help = `doszer init - write a commented DOSZ.TOML into a source directory

Usage: doszer init [options] SOURCE

Options:
      --exec PATH         plain model: DOS program to run at startup
  -f, --force             overwrite an existing manifest
      --name NAME         canonical name (default: source directory name)
      --no-media          don't identify disc images in the source
      --os PATH           OS image model: OS image path, without extension
      --shell PATH        shell model: system shell path, without extension
` + flagHelp
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
		media, skipped, err := build.IdentifyMedia(source)
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
		o.Media = media
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
` + flagOggQuality + flagStrict + flagHelp
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

	if opts.DryRun {
		fmt.Printf("Dry run: would write %s (%d entries)\n", res.OutputPath, len(res.Entries))
	} else {
		fmt.Printf("Built %s (%d entries)\n", res.OutputPath, len(res.Entries))
	}
	return nil
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
	const help = `doszer verify - check an archive against its manifest and the specification

Usage: doszer verify [options] FILE.dosz

Options:
` + flagHelp
	if ok, err := parse(fs, args, help, 1, 1); !ok {
		return err
	}

	warnings, err := verify.Check(fs.Arg(0))
	endSteps(err)
	for _, w := range warnings {
		fmt.Fprintf(os.Stderr, "[WARN] %s\n", w)
	}
	if err != nil {
		return err
	}

	fmt.Printf("%s conforms to the specification\n", fs.Arg(0))
	return nil
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
` + flagHelp
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

Usage: doszer redump [options] [SHA1|FILE|IMAGE...]

Options:
      --dat FILE          datfile to use (default: redump_dat, else Redump's)
      --offset SAMPLES    drive read offset a CUE image's audio wasn't corrected for
      --update            fetch Redump's current datfile now
` + flagHelp
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

	var shas, files, cues []string
	for _, a := range fs.Args() {
		switch {
		case len(a) == 40 && isHex(a):
			shas = append(shas, a)
		case strings.EqualFold(filepath.Ext(a), ".cue"), strings.EqualFold(filepath.Ext(a), ".zip"):
			cues = append(cues, a)
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
` + flagHelp
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
` + flagHelp
	if ok, err := parse(fs, args, help, 0, 0); !ok {
		return err
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
const windowHelp = "Click the window to capture the mouse, Ctrl+F10 releases it. Alt+Enter: fullscreen, hold Alt+F12: fast-forward, Ctrl+F5: screenshot."

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
` + flagSystemDir + flagHelp
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
` + flagOggQuality + flagStrict + flagHelp
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
	const help = `doszer prepare - make a game-ready copy of a Windows 3.x shell

Usage: doszer prepare shell [options] SHELL OUTPUT

Options:
      --add DIR           lay DIR's files over the result (repeatable)
  -n, --dry-run           print the changes without writing OUTPUT
  -f, --force             overwrite OUTPUT
` + flagHelp
	if ok, err := parse(fs, args, help, 3, 3); !ok {
		return err
	}
	if fs.Arg(0) != "shell" {
		fmt.Fprint(os.Stderr, help)
		return fmt.Errorf("unknown prepare target %q", fs.Arg(0))
	}

	rep, err := prepare.Shell(prepare.ShellOptions{
		Input:  fs.Arg(1),
		Output: fs.Arg(2),
		Add:    *add,
		DryRun: *dryRun,
		Force:  *force,
	})
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
