package runlog

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/DemiCloud/doszer/internal/redact"
)

// Export writes runs into a ZIP for debugging, every file through red
// (nil: unredacted):
//
//   - commands.txt: the runs' command lines in the order they ran, each
//     with its time, result and the choices doszer made, to replay them;
//   - runs/<id>/: each run's folder (report, transcript, attachments);
//   - extra's files at their names (the machine's environment).
func Export(w io.Writer, runs []*Report, red *redact.Redactor, extra map[string][]byte) error {
	runs = append([]*Report(nil), runs...)
	sort.SliceStable(runs, func(i, j int) bool { return runs[i].Started.Before(runs[j].Started) })
	text := func(b []byte) []byte {
		if red == nil {
			return b
		}
		return red.Bytes(b)
	}
	zw := zip.NewWriter(w)
	add := func(name string, data []byte, modified time.Time) error {
		f, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: modified})
		if err != nil {
			return err
		}
		_, err = f.Write(data)
		return err
	}
	now := time.Now()
	if err := add("commands.txt", text([]byte(Commands(runs))), now); err != nil {
		return err
	}
	for _, r := range runs {
		ents, err := os.ReadDir(r.dir)
		if err != nil {
			return err
		}
		for _, e := range ents {
			if e.IsDir() || strings.HasSuffix(e.Name(), ".tmp") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(r.dir, e.Name()))
			if err != nil {
				return err
			}
			if e.Name() == ReportFile && red != nil {
				if data, err = red.JSON(data); err != nil {
					return fmt.Errorf("%s: %w", r.ID, err)
				}
			} else {
				data = text(data)
			}
			mod := now
			if fi, err := e.Info(); err == nil {
				mod = fi.ModTime()
			}
			if err := add("runs/"+r.ID+"/"+e.Name(), data, mod); err != nil {
				return err
			}
		}
	}
	names := make([]string, 0, len(extra))
	for n := range extra {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		data := extra[n]
		if strings.HasSuffix(n, ".json") && red != nil {
			if j, err := red.JSON(data); err == nil {
				data = j
			} else {
				data = red.Bytes(data)
			}
		} else {
			data = text(data)
		}
		if err := add(n, data, now); err != nil {
			return err
		}
	}
	return zw.Close()
}

// Commands lists runs' command lines in order, as a POSIX shell script
// would run them: a cd where the working directory changes, and as
// comments, each run's time, result and the choices doszer made itself -
// what replaying it relies on coming out the same.
func Commands(runs []*Report) string {
	var b strings.Builder
	b.WriteString("# doszer runs, in the order they ran\n")
	dir := ""
	for _, r := range runs {
		b.WriteString("\n")
		res := r.Result
		if r.Error != "" {
			res += ": " + strings.ReplaceAll(r.Error, "\n", "; ")
		}
		fmt.Fprintf(&b, "# %s  %s  %s  (%s)\n", r.Started.Format("2006-01-02 15:04:05"), r.ID, res, r.Duration().Round(time.Millisecond))
		for _, d := range r.Decisions {
			line := "#   chose " + d.What + ": " + d.Choice
			if d.Why != "" {
				line += " (" + d.Why + ")"
			}
			b.WriteString(line + "\n")
		}
		if r.Dir != dir && r.Dir != "" {
			fmt.Fprintf(&b, "cd %s\n", CommandLine([]string{r.Dir})[len("doszer "):])
			dir = r.Dir
		}
		b.WriteString(r.CommandLine() + "\n")
	}
	return b.String()
}
