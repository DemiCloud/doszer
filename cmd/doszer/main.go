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

	"github.com/DemiCloud/doszer/internal/manifest"
)

// errUsage signals that usage was already printed.
var errUsage = errors.New("usage")

type command struct {
	name    string
	summary string
	run     func(args []string) error
}

var commands []command

func init() {
	commands = []command{
		{"init", "write a commented DOSZ.TOML into a source directory", runInit},
		{"build", "build an archive from a source directory", notImplemented("build")},
		{"install", "run an installer and extract the result into a source", notImplemented("install")},
		{"repack", "rebuild an archive from its embedded manifest", notImplemented("repack")},
		{"verify", "check an archive against its manifest and the specification", notImplemented("verify")},
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

	for _, c := range commands {
		if c.name == args[0] {
			return c.run(args[1:])
		}
	}

	usage(os.Stderr)
	return fmt.Errorf("unknown command %q", args[0])
}

func usage(w io.Writer) {
	fmt.Fprintf(w, "Usage: doszer <command> [arguments]\n\nCommands:\n")
	for _, c := range commands {
		fmt.Fprintf(w, "  %-8s %s\n", c.name, c.summary)
	}
	fmt.Fprintf(w, "\nRun 'doszer <command> -h' for command options.\n")
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
		fmt.Fprintf(fs.Output(), "Usage: doszer init [options] <source>\n\nOptions:\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(reorder(args)); err != nil {
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

// reorder moves flags ahead of positional arguments so options may follow
// the source path (flag stops parsing at the first non-flag argument).
func reorder(args []string) []string {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if len(a) > 1 && a[0] == '-' {
			flags = append(flags, a)
			if !hasValue(a) && i+1 < len(args) && !isBoolFlag(a) {
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

func isBoolFlag(flagArg string) bool {
	name := flagArg
	for len(name) > 0 && name[0] == '-' {
		name = name[1:]
	}
	return name == "force" || name == "h" || name == "help"
}
