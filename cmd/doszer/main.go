// Command doszer builds .dosz archives for DOSBox Pure from DOSZ.TOML
// manifests. It is the reference implementation of the DOSZ Manifest
// Specification.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DemiCloud/doszer/internal/build"
	"github.com/DemiCloud/doszer/internal/config"
	"github.com/DemiCloud/doszer/internal/install"
	"github.com/DemiCloud/doszer/internal/manifest"
	"github.com/DemiCloud/doszer/internal/repack"
	"github.com/DemiCloud/doszer/internal/verify"
)

// errUsage signals that usage was already printed.
var errUsage = errors.New("usage")

// version is set via -ldflags "-X main.version=..." (see Makefile).
var version = "dev"

type command struct {
	name    string
	summary string
	run     func(args []string) error
}

var commands []command

func init() {
	commands = []command{
		{"init", "write a commented DOSZ.TOML into a source directory", runInit},
		{"build", "build an archive from a source directory", runBuild},
		{"install", "run an installer and extract the result into a source", runInstall},
		{"repack", "rebuild an archive from its embedded manifest", runRepack},
		{"verify", "check an archive against its manifest and the specification", runVerify},
	}
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		if !errors.Is(err, errUsage) {
			logError(err)
		}
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		usage(os.Stderr)
		return errUsage
	}
	if args[0] == "-V" || args[0] == "--version" || args[0] == "version" {
		fmt.Println("doszer", version)
		return nil
	}

	for _, c := range commands {
		if c.name == args[0] {
			return c.run(args[1:])
		}
	}

	usage(os.Stderr)
	return fmt.Errorf("unknown command %q", args[0])
}

func usage(w io.Writer) {
	fmt.Fprint(w, "doszer - build .dosz archives for DOSBox Pure from DOSZ.TOML manifests\n\n")
	fmt.Fprint(w, "Usage: doszer <command> [arguments]\n\nCommands:\n")
	for _, c := range commands {
		fmt.Fprintf(w, "  %-8s %s\n", c.name, c.summary)
	}
	fmt.Fprint(w, "\nRun 'doszer <command> -h' for command options.\n")
	fmt.Fprint(w, "doszer --version/-V/version shows the version.\n")
}

// logError writes a timestamped error to stderr.
func logError(err error) {
	fmt.Fprintf(os.Stderr, "[%s] ERROR: %v\n", time.Now().Format("2006-01-02 15:04:05"), err)
}

// runInit implements `doszer init <source>`.
func runInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	var o manifest.TemplateOptions
	force := fs.Bool("force", false, "overwrite an existing manifest")
	fs.StringVar(&o.Name, "name", "", "canonical name (default: source directory name)")
	fs.StringVar(&o.Exec, "exec", "", "plain model: DOS program to run at startup")
	fs.StringVar(&o.Shell, "shell", "", "shell model: system shell path, without extension")
	fs.StringVar(&o.OS, "os", "", "OS image model: OS image path, without extension")
	noMedia := fs.Bool("no-media", false, "don't identify disc images in the source")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), `doszer init - write a commented DOSZ.TOML into a source directory

Usage: doszer init [options] <source>

Options:
  -exec STR   plain model: DOS program to run at startup
  -force      overwrite an existing manifest
  -name STR   canonical name (default: source directory name)
  -no-media   don't identify disc images in the source
  -os STR     OS image model: OS image path, without extension
  -shell STR  shell model: system shell path, without extension
`)
	}

	if err := fs.Parse(reorder(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return errUsage
		}
		return err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return errUsage
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
			return fmt.Errorf("%s already exists (use -force to overwrite)", path)
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
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	name := fs.String("name", "", "override the manifest's effective name")
	dryRun := fs.Bool("dry-run", false, "print the build plan without writing an archive")
	strict := fs.Bool("strict", false, "fail if any layout.trim or layout.rewrite rule matches nothing")
	oggQuality := fs.Int("ogg-quality", 0, "Ogg Vorbis quality, -1 to 10")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), `doszer build - build an archive from a source directory

Usage: doszer build [options] <source> [<output.dosz>]

Options:
  -dry-run         print the build plan without writing an archive
  -name STR        override the manifest's effective name
  -ogg-quality N   Ogg Vorbis quality, -1 to 10
  -strict          fail if a trim/rewrite rule matches nothing
`)
	}

	if err := fs.Parse(reorder(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return errUsage
		}
		return err
	}
	if fs.NArg() < 1 || fs.NArg() > 2 {
		fs.Usage()
		return errUsage
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

	res, err := build.Run(opts)
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
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), `doszer verify - check an archive against its manifest and the specification

Usage: doszer verify <file.dosz>
`)
	}

	if err := fs.Parse(reorder(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return errUsage
		}
		return err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return errUsage
	}

	if err := verify.Verify(fs.Arg(0)); err != nil {
		return err
	}

	fmt.Printf("%s conforms to the specification\n", fs.Arg(0))
	return nil
}

// runInstall implements `doszer install <source>`.
func runInstall(args []string) error {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	dir := fs.String("dir", "", "folder to extract, relative to the drive root")
	from := fs.String("from", "", "extract from this .sav, .pure.zip, .vhd or .img instead")
	content := fs.String("content", "", "archive a -from save was made over")
	template := fs.String("template", "", "pristine image to compare a -from image against")
	force := fs.Bool("force", false, "overwrite differing files in the source")
	dryRun := fs.Bool("dry-run", false, "report without writing")
	keep := fs.Bool("keep", false, "keep the session's save files")
	exe := fs.String("dosbox-pure", "", "DOSBox Pure executable")
	systemDir := fs.String("system-dir", "", "DOSBox Pure system directory")
	saveDir := fs.String("save-dir", "", "DOSBox Pure save directory")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), `doszer install - run an installer and extract the result into a source

Usage: doszer install [options] <source>

Options:
  -content FILE      archive a -from save was made over
  -dir PATH          folder to extract, relative to the drive root
  -dosbox-pure FILE  DOSBox Pure executable
  -dry-run           report without writing
  -force             overwrite differing files in the source
  -from FILE         extract from this .sav, .pure.zip, .vhd or .img instead
  -keep              keep the session's save files
  -save-dir DIR      DOSBox Pure save directory
  -system-dir DIR    DOSBox Pure system directory
  -template FILE     pristine image to compare a -from image against
`)
	}

	if err := fs.Parse(reorder(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return errUsage
		}
		return err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return errUsage
	}

	cfg, err := config.Load(fs.Arg(0))
	if err != nil {
		return err
	}
	opts := install.Options{
		Source:    fs.Arg(0),
		Exe:       stringOr(*exe, cfg.DosboxPure),
		SystemDir: stringOr(*systemDir, cfg.SystemDir),
		SaveDir:   stringOr(*saveDir, cfg.SaveDir),
		Dir:       *dir,
		Force:     *force,
		DryRun:    *dryRun,
		Keep:      *keep,
		From:      *from,
		Content:   *content,
		Template:  *template,
		Templates: cfg.Templates,
	}

	rep, err := install.Run(opts)
	if rep != nil {
		printInstallReport(rep, opts.DryRun)
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
	fs := flag.NewFlagSet("repack", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "print the rebuild plan without writing an archive")
	strict := fs.Bool("strict", false, "fail if any layout.trim or layout.rewrite rule matches nothing")
	manifestPath := fs.String("manifest", "", "use this manifest instead of the archive's embedded one")
	oggQuality := fs.Int("ogg-quality", 0, "Ogg Vorbis quality, -1 to 10")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), `doszer repack - rebuild an archive from its embedded manifest

Usage: doszer repack [options] <input.dosz> [<output.dosz>]

Options:
  -dry-run         print the rebuild plan without writing an archive
  -manifest STR    use this manifest instead of the archive's embedded one
  -ogg-quality N   Ogg Vorbis quality, -1 to 10
  -strict          fail if a trim/rewrite rule matches nothing
`)
	}

	if err := fs.Parse(reorder(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return errUsage
		}
		return err
	}
	if fs.NArg() < 1 || fs.NArg() > 2 {
		fs.Usage()
		return errUsage
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

	res, err := repack.Run(opts)
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
func flagOr(fs *flag.FlagSet, name string, flagValue, configured int) int {
	set := false
	fs.Visit(func(f *flag.Flag) { set = set || f.Name == name })
	if set {
		return flagValue
	}
	return configured
}

// reorder moves flags ahead of positional args so options may follow the
// source path; it consults fs to tell bool flags from value-taking ones.
func reorder(fs *flag.FlagSet, args []string) []string {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if len(a) > 1 && a[0] == '-' {
			flags = append(flags, a)
			if !hasValue(a) && i+1 < len(args) && !isBoolFlag(fs, a) {
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		positional = append(positional, a)
	}
	return append(flags, positional...)
}

func hasValue(flagArg string) bool {
	for _, r := range flagArg {
		if r == '=' {
			return true
		}
	}
	return false
}

// isBoolFlag reports whether flagArg is a bool flag on fs, or -h/-help.
func isBoolFlag(fs *flag.FlagSet, flagArg string) bool {
	name := flagArg
	for len(name) > 0 && name[0] == '-' {
		name = name[1:]
	}
	if name == "h" || name == "help" {
		return true
	}

	f := fs.Lookup(name)
	if f == nil {
		return false
	}
	b, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && b.IsBoolFlag()
}
