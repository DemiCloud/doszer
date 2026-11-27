package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DemiCloud/doszer/internal/config"
	"github.com/DemiCloud/doszer/internal/env"
	"github.com/DemiCloud/doszer/internal/output"
	"github.com/DemiCloud/doszer/internal/redact"
	"github.com/DemiCloud/doszer/internal/runlog"
)

// runLogs implements `doszer logs [RUN|SESSION|PATH...]`.
func runLogs(args []string) error {
	fs := newFlags("logs")
	export := fs.String("export", "", "")
	del := fs.Bool("delete", false, "")
	noRedact := fs.Bool("no-redact", false, "")
	transcript := fs.Bool("transcript", false, "")
	force := fs.BoolP("force", "f", false, "")
	const help = `doszer logs - list recorded runs, show one, pack them into a ZIP, or delete them

Usage: doszer logs [options] [RUN|SESSION|PATH...]

Options:
      --delete            delete the runs
      --export FILE.zip   pack the runs (default: the last run's history) with this machine's environment
  -f, --force             export: overwrite FILE
      --no-redact         export: keep home folders, user and host names
      --transcript        show the runs' output instead of their reports
` + flagFormatData + flagHelp
	if ok, err := parse(fs, args, help, 0, 1<<20); !ok {
		return err
	}
	dir, err := config.LogsDir()
	if err != nil {
		return err
	}
	all, err := runlog.List(dir)
	if err != nil {
		return err
	}
	var runs []*runlog.Report
	for _, a := range fs.Args() {
		found := runlog.Find(all, a)
		if len(found) == 0 {
			return fmt.Errorf("%s: no such run, session or path in %s", a, dir)
		}
		runs = append(runs, found...)
	}

	if *del {
		if len(fs.Args()) == 0 {
			return errors.New("--delete: name the runs, sessions or paths to delete")
		}
		if *export != "" {
			return errors.New("--delete and --export exclude each other")
		}
		n, err := runlog.Delete(dir, runs)
		if n > 0 {
			fmt.Fprintf(stdout, "Deleted %s\n", plural(n, "run"))
		}
		return err
	}
	if *export != "" {
		if len(runs) == 0 {
			if len(all) == 0 {
				return fmt.Errorf("no runs in %s", dir)
			}
			runs = runlog.History(all, all[0])
		}
		return exportLogs(*export, runs, !*noRedact, *force)
	}
	if *transcript {
		if len(runs) == 0 && len(all) > 0 {
			runs = all[:1]
		}
		for i, r := range runs {
			data, err := os.ReadFile(filepath.Join(r.Folder(), runlog.TranscriptFile))
			if err != nil {
				return err
			}
			if i > 0 {
				fmt.Fprintln(stdout)
			}
			stdout.Write(data)
		}
		return nil
	}
	if len(runs) == 1 && len(fs.Args()) == 1 {
		return showRun(runs[0])
	}
	if len(fs.Args()) == 0 {
		runs = all
	}
	return listRuns(runs)
}

// listRuns prints runs a line each.
func listRuns(runs []*runlog.Report) error {
	switch format {
	case output.CSV:
		var rows [][]string
		for _, r := range runs {
			rows = append(rows, []string{r.ID, r.Started.Format(time.RFC3339), r.Command, r.Subject(), r.Result, fmt.Sprintf("%.3f", r.Seconds), r.Session, r.CommandLine()})
		}
		return output.EncodeCSV(stdout, []string{"id", "started", "command", "subject", "result", "seconds", "session", "command_line"}, rows)
	case output.Fancy, output.Plain:
		if len(runs) == 0 {
			fmt.Fprintln(stdout, "No runs recorded")
			return nil
		}
		wi, wc, ws, wr := 0, 0, 0, 0
		for _, r := range runs {
			wi, wc, ws, wr = max(wi, len(r.ID)), max(wc, len(r.Command)), max(ws, len([]rune(r.Subject()))), max(wr, len(r.Result))
		}
		for _, r := range runs {
			fmt.Fprintf(stdout, "%-*s  %-*s  %-*s  %-*s  %s\n", wi, r.ID, wc, r.Command, ws, r.Subject(), wr, r.Result, shortDuration(r.Duration()))
		}
		return nil
	}
	return output.Encode(stdout, format, struct {
		Runs []*runlog.Report `json:"runs" yaml:"runs" toml:"runs"`
	}{runs})
}

// showRun prints one run's report.
func showRun(r *runlog.Report) error {
	if format.Structured() && format != output.CSV {
		return output.Encode(stdout, format, r)
	}
	if format == output.CSV {
		return listRuns([]*runlog.Report{r})
	}
	row := func(k, v string) {
		if v != "" {
			fmt.Fprintf(stdout, "%-9s %s\n", k, v)
		}
	}
	res := r.Result
	if r.Error != "" {
		res += ": " + r.Error
	}
	row("Run", r.ID)
	row("Command", r.CommandLine())
	row("In", r.Dir)
	row("Started", r.Started.Format("2006-01-02 15:04:05")+", "+shortDuration(r.Duration()))
	row("Result", res)
	row("Game", r.Name)
	row("Source", r.Source)
	row("Archive", r.Archive)
	row("Session", r.Session)
	var vs []string
	for _, k := range []string{"doszer", "dosbox-pure", "redumper"} {
		if v := r.Versions[k]; v != "" {
			vs = append(vs, k+" "+v)
		}
	}
	row("Versions", strings.Join(vs, ", "))
	row("Folder", r.Folder())

	section := func(title string) { fmt.Fprintf(stdout, "\n%s:\n", title) }
	if len(r.Steps) > 0 {
		section("Steps")
		for _, s := range r.Steps {
			mark := "✓"
			if !s.OK {
				mark = "✗"
			}
			fmt.Fprintf(stdout, "  %s %s (%s)\n", mark, s.Name, shortDuration(time.Duration(s.Seconds*float64(time.Second))))
		}
	}
	if len(r.Decisions) > 0 {
		section("Chosen")
		for _, d := range r.Decisions {
			line := fmt.Sprintf("  %s: %s", d.What, d.Choice)
			if d.Why != "" {
				line += " (" + d.Why + ")"
			}
			fmt.Fprintln(stdout, line)
		}
	}
	if len(r.Warnings) > 0 {
		section("Warnings")
		for _, w := range r.Warnings {
			fmt.Fprintf(stdout, "  %s\n", w)
		}
	}
	if b := r.Build; b != nil {
		for _, m := range b.Media {
			line := fmt.Sprintf("Disc %s", m.ID)
			if m.RedumpName != "" {
				line += fmt.Sprintf(" (Redump %q)", m.RedumpName)
			}
			if m.Image != "" {
				line += ": " + m.Image
			} else {
				line += ": no image matched"
			}
			fmt.Fprintf(stdout, "\n%s, %s\n", line, plural(len(m.Tracks), "track"))
		}
		if len(b.Input) > 0 {
			var n int64
			for _, f := range b.Input {
				n += f.Size
			}
			fmt.Fprintf(stdout, "\nCollected %s, %s; trimmed %d, moved %d, rewrote %d, converted %d\n", plural(len(b.Input), "file"), size(n), len(b.Trimmed), len(b.Moved), len(b.Rewrites), len(b.Converted))
		}
		if a := b.Archive; a != nil {
			fmt.Fprintf(stdout, "Wrote %s: %s, %s of %s content (%.0f%%), SHA-1 %s\n", a.Path, entries(len(a.Entries)), size(a.Compressed), size(a.Content), a.Ratio()*100, a.SHA1)
		}
	}
	if in := r.Install; in != nil {
		fmt.Fprintf(stdout, "\n%s changed on %s; extracted %d from %s (%d already identical)\n", plural(len(in.Changes), "file"), in.Drive, len(in.Extracted), in.Drive+`\`+strings.ReplaceAll(in.Dir, "/", `\`), len(in.Unchanged))
	}
	if d := r.Dump; d != nil {
		fmt.Fprintf(stdout, "\nDumped %s, %s", d.Cue, plural(len(d.Tracks), "track"))
		switch {
		case d.Verified:
			fmt.Fprintf(stdout, ": verified, Redump %q", d.Redump)
		case d.Redump != "":
			fmt.Fprintf(stdout, ": not verified, closest Redump %q (%d matching)", d.Redump, d.Matching)
		case d.Datfile != nil:
			fmt.Fprint(stdout, ": no Redump match")
		}
		fmt.Fprintln(stdout)
	}
	if len(r.Files) > 0 {
		section("Files")
		for _, f := range r.Files {
			fmt.Fprintf(stdout, "  %s\n", f)
		}
	}
	return nil
}

// exportLogs packs runs and this machine's environment into a ZIP at
// path, redacted unless told not to.
func exportLogs(path string, runs []*runlog.Report, redacted, force bool) error {
	if !force {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("%s already exists (use --force to overwrite)", path)
		}
	}
	extra := map[string][]byte{}
	if cfg, err := config.Load(""); err == nil {
		if r, err := env.Read(cfg, "", "", ""); err == nil {
			if data, err := json.MarshalIndent(r, "", "  "); err == nil {
				extra["env.json"] = data
			}
		}
	}
	if s, err := config.Settings(""); err == nil {
		if data, err := json.MarshalIndent(s, "", "  "); err == nil {
			extra["settings.json"] = data
		}
	}
	var red *redact.Redactor
	if redacted {
		red = redact.New()
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if err := runlog.Export(f, runs, red, extra); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	note := ""
	if !redacted {
		note = " (not redacted)"
	}
	fmt.Fprintf(stdout, "Wrote %s: %s%s\n", path, plural(len(runs), "run"), note)
	return nil
}

// entries is "1 entry" or "N entries".
func entries(n int) string {
	if n == 1 {
		return "1 entry"
	}
	return fmt.Sprintf("%d entries", n)
}

// shortDuration is d rounded for reading: ms under a second, tenths
// under a minute, else seconds.
func shortDuration(d time.Duration) string {
	switch {
	case d < time.Second:
		return d.Round(time.Millisecond).String()
	case d < time.Minute:
		return d.Round(100 * time.Millisecond).String()
	}
	return d.Round(time.Second).String()
}

// size is n bytes in KB, MB or GB.
func size(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
}
