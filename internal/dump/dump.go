// Package dump implements `doszer dump`: it reads a CD in an optical drive
// to a BIN/CUE in a source directory with redumper (embedded,
// internal/builtin), which corrects the drive's read offset, so a dump of
// a disc matches anyone else's (and Redump's) track for track.
package dump

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/DemiCloud/doszer/internal/builtin"
	"github.com/DemiCloud/doszer/internal/cue"
)

// Options configures a dump.
type Options struct {
	Dir   string // source directory the BIN/CUE goes into, created if missing
	Drive string // drive to read (E:, /dev/sg1); "" for redumper's first drive with a disc
}

// Result is what a dump produced.
type Result struct {
	Cue    string   // the CUE sheet in Dir
	Tracks []string // the track files it names, in Dir
	Log    string   // redumper's log, kept outside Dir (it would end up in the archive)
	// Errors are redumper's reports of samples it couldn't read, when it
	// had to be told to split anyway ("errors detected, track: ..."). A
	// generic drive with a positive read offset can't read the few samples
	// of the last track that lie in the lead-out; left as zeros (silence,
	// as they almost always are), the dump can still match Redump.
	Errors []string
}

// Run dumps the disc in opts.Drive to disc<N>.cue and its tracks in
// opts.Dir, N one past the discs already there, so dumping each disc of a
// multi-disc game into the same directory keeps them apart.
//
// redumper works in a hidden directory beside opts.Dir (on the same file
// system, so its tracks are moved, not copied, and outside it, so nothing
// of redumper's ends up in an archive). It's removed once the dump is
// done. After a failure or an interruption it's kept: running Run again
// resumes, re-reading only the sectors still missing or in error
// (redumper's refine) instead of the whole disc.
//
// If the dump is complete except for unreadable samples, it is split
// anyway with those samples left as read (zero) rather than whole sectors
// replaced by fill bytes, and Result.Errors reports them: whether it is
// right is then for a Redump match to tell.
func Run(opts Options) (*Result, error) {
	if err := os.MkdirAll(opts.Dir, 0o755); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(opts.Dir)
	if err != nil {
		return nil, err
	}
	work := filepath.Join(filepath.Dir(abs), "."+filepath.Base(abs)+".doszer-dump")
	if err := os.MkdirAll(work, 0o755); err != nil {
		return nil, err
	}
	name, resume := resumeName(work)
	if !resume {
		name = nextName(opts.Dir)
	}

	wsl := isWSL()
	exe, err := redumper(wsl)
	if err != nil {
		return nil, err
	}
	// Relative paths throughout, from the work directory: nothing to
	// translate for a Windows redumper under WSL, whose working
	// directory interop converts.
	run := func(args ...string) error {
		args = append(args, "--image-path=.", "--image-name="+name)
		if opts.Drive != "" {
			args = append(args, "--drive="+opts.Drive)
		}
		cmd := exec.Command(exe, args...)
		cmd.Dir = work
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		return cmd.Run()
	}
	args := []string{"disc", "--auto-detect", "--retries=20"}
	if resume {
		args = append(args, "--continue=refine")
	}
	runErr := run(args...)

	res := &Result{}
	logPath := filepath.Join(work, name+".log")
	if runErr != nil && splitBlockedByErrors(logPath) {
		res.Errors = sampleErrors(logPath)
		runErr = run("split", "--force-split", "--leave-unchanged")
	}
	if log, err := keepLog(logPath, abs, name); err == nil {
		res.Log = log
	}
	if runErr != nil {
		msg := fmt.Sprintf("redumper: %v", runErr)
		if res.Log != "" {
			msg += " (log: " + res.Log + ")"
		}
		if wsl && strings.HasPrefix(opts.Drive, "/") {
			msg += "; under WSL, name the drive by its Windows letter (e.g. E:)"
		}
		msg += "; run dump again to resume, re-reading only what failed"
		return res, errors.New(msg)
	}

	sheetPath := filepath.Join(work, name+".cue")
	data, err := os.ReadFile(sheetPath)
	if err != nil {
		return res, fmt.Errorf("redumper wrote no CUE sheet: %w", err)
	}
	sheet, err := cue.Parse(data)
	if err != nil {
		return res, fmt.Errorf("%s: %w", sheetPath, err)
	}
	for _, f := range sheet.Files {
		if err := move(work, opts.Dir, f.Name); err != nil {
			return res, err
		}
		res.Tracks = append(res.Tracks, filepath.Join(opts.Dir, f.Name))
	}
	if err := move(work, opts.Dir, name+".cue"); err != nil {
		return res, err
	}
	res.Cue = filepath.Join(opts.Dir, name+".cue")
	os.RemoveAll(work)
	return res, nil
}

// resumeName is the image name of an unfinished dump in work, if any.
func resumeName(work string) (string, bool) {
	states, _ := filepath.Glob(filepath.Join(work, "disc*.state"))
	if len(states) != 1 {
		return "", false
	}
	return strings.TrimSuffix(filepath.Base(states[0]), ".state"), true
}

// splitBlockedByErrors reports whether redumper read the disc but refused
// to split it over unreadable or C2-error samples (cd/cd_split.ixx).
func splitBlockedByErrors(log string) bool {
	b, err := os.ReadFile(log)
	return err == nil && strings.Contains(string(b), "data errors detected, unable to continue")
}

// sampleErrors are the log's "errors detected, track: ..." lines.
func sampleErrors(log string) []string {
	b, _ := os.ReadFile(log)
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); strings.HasPrefix(l, "errors detected, track:") {
			out = append(out, l)
		}
	}
	return out
}

// redumper returns the redumper to run; tests replace it.
var redumper = builtin.Redumper

// nextName is disc<N>.cue's base for the first N not in dir.
func nextName(dir string) string {
	for n := 1; ; n++ {
		name := fmt.Sprintf("disc%d", n)
		if _, err := os.Stat(filepath.Join(dir, name+".cue")); errors.Is(err, os.ErrNotExist) {
			return name
		}
	}
}

func move(from, to, name string) error {
	if strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("redumper's CUE sheet names a file outside its directory: %s", name)
	}
	dst := filepath.Join(to, name)
	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("%s already exists", dst)
	}
	return os.Rename(filepath.Join(from, name), dst)
}

// keepLog copies redumper's log to doszer's cache directory: a log in the
// source directory would be packed into the archive.
func keepLog(log, abs, name string) (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	logDir := filepath.Join(cache, "doszer", "dumps")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return "", err
	}
	dst := filepath.Join(logDir, filepath.Base(abs)+" - "+name+".log")
	in, err := os.Open(log)
	if err != nil {
		return "", err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return "", err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return dst, err
}

// isWSL reports whether doszer runs under WSL, whose Linux side can't
// reach optical drives: the Windows redumper runs instead, through
// interop.
func isWSL() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	if os.Getenv("WSL_DISTRO_NAME") != "" {
		return true
	}
	b, err := os.ReadFile("/proc/sys/kernel/osrelease")
	return err == nil && strings.Contains(strings.ToLower(string(b)), "microsoft")
}
