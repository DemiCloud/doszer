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
	"time"

	"github.com/DemiCloud/doszer/internal/build"
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
		{"install", "run an installer and extract the result into a source", notImplemented("install")},
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

func notImplemented(name string) func([]string) error {
	return func([]string) error {
		return fmt.Errorf("%s: not implemented yet", name)
	}
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
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), `doszer init - write a commented DOSZ.TOML into a source directory

Usage: doszer init [options] <source>

Options:
  -exec STR   plain model: DOS program to run at startup
  -force      overwrite an existing manifest
  -name STR   canonical name (default: source directory name)
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
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), `doszer build - build an archive from a source directory

Usage: doszer build [options] <source> [<output.dosz>]

Options:
  -dry-run    print the build plan without writing an archive
  -name STR   override the manifest's effective name
  -strict     fail if a trim/rewrite rule matches nothing
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

	opts := build.Options{
		SourceDir: fs.Arg(0),
		Name:      *name,
		DryRun:    *dryRun,
		Strict:    *strict,
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

// runRepack implements `doszer repack <input.dosz> [<output.dosz>]`.
func runRepack(args []string) error {
	fs := flag.NewFlagSet("repack", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "print the rebuild plan without writing an archive")
	strict := fs.Bool("strict", false, "fail if any layout.trim or layout.rewrite rule matches nothing")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), `doszer repack - rebuild an archive from its embedded manifest

Usage: doszer repack [options] <input.dosz> [<output.dosz>]

Options:
  -dry-run    print the rebuild plan without writing an archive
  -strict     fail if a trim/rewrite rule matches nothing
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

	opts := repack.Options{InputPath: fs.Arg(0), DryRun: *dryRun, Strict: *strict}
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
