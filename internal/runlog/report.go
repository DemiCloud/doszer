// Package runlog records what doszer's commands do, for reproducing a
// build and debugging one: each run gets a folder under the logs
// directory (config.LogsDir) holding its report (ReportFile: the command
// line, settings in effect, versions, steps with their timings, the
// decisions doszer made on its own, warnings, and what was built,
// installed or dumped), the command's output with timestamps
// (TranscriptFile), and files attached to it (the manifest written or
// embedded, redumper's log). The window's Logs page and `doszer logs`
// read them; Export packs them into one ZIP, redacted.
package runlog

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/DemiCloud/doszer/internal/config"
)

// Files in a run's folder.
const (
	ReportFile     = "report.json"
	TranscriptFile = "transcript.log"
)

// SessionEnv names the environment variable that ties runs together: the
// window sets it for every command of one flow (Import a disc: dump,
// init, install, start, build), so they are listed as one.
const SessionEnv = "DOSZER_SESSION"

// Results a run ends with.
const (
	ResultRunning     = "running" // still running, or the process died
	ResultOK          = "ok"
	ResultFailed      = "failed"
	ResultInterrupted = "interrupted"
)

// Report is one run.
type Report struct {
	ID       string            `json:"id"`
	Session  string            `json:"session,omitempty"`
	Command  string            `json:"command"`
	Args     []string          `json:"args"`
	Dir      string            `json:"dir"` // the working directory
	Started  time.Time         `json:"started"`
	Seconds  float64           `json:"seconds"`
	Result   string            `json:"result"`
	Error    string            `json:"error,omitempty"`
	Name     string            `json:"name,omitempty"`    // the game's
	Source   string            `json:"source,omitempty"`  // source directory or input archive, absolute
	Archive  string            `json:"archive,omitempty"` // the archive written or read, absolute
	Versions map[string]string `json:"versions,omitempty"`
	OS       string            `json:"os,omitempty"` // GOOS/GOARCH

	Settings  []config.Setting `json:"settings,omitempty"`
	Steps     []Step           `json:"steps,omitempty"`
	Decisions []Decision       `json:"decisions,omitempty"`
	Warnings  []string         `json:"warnings,omitempty"`
	Files     []string         `json:"files,omitempty"` // attached, in the run's folder

	Build   *Build   `json:"build,omitempty"`
	Install *Install `json:"install,omitempty"`
	Dump    *Dump    `json:"dump,omitempty"`

	dir string // the run's folder
}

// Step is one step a command showed (status.Step), with its timing.
type Step struct {
	Name    string    `json:"name"`
	Started time.Time `json:"started"`
	Seconds float64   `json:"seconds"`
	OK      bool      `json:"ok"`
}

// Decision is a choice doszer made itself: what was chosen, and why.
type Decision struct {
	What   string `json:"what"`
	Choice string `json:"choice"`
	Why    string `json:"why,omitempty"`
}

// Build is what build, repack, set, child or convert wrote.
type Build struct {
	DryRun      bool         `json:"dry_run,omitempty"`
	Input       []File       `json:"input,omitempty"` // collected, before trim
	Trimmed     []string     `json:"trimmed,omitempty"`
	Moved       []Move       `json:"moved,omitempty"`
	Rewrites    []Rewrite    `json:"rewrites,omitempty"`
	Converted   []Conversion `json:"converted,omitempty"`
	Media       []Media      `json:"media,omitempty"`
	Suggestions []string     `json:"suggestions,omitempty"`
	Archive     *Archive     `json:"archive,omitempty"`
}

// File is a file collected from a source.
type File struct {
	Path     string    `json:"path"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
}

// Move is a path flatten renamed.
type Move struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Rewrite is a layout.rewrite rule and the files it changed.
type Rewrite struct {
	From  string   `json:"from"`
	To    string   `json:"to"`
	Files []string `json:"files"`
}

// Conversion is a disc image converted or renamed (spec §6.4).
type Conversion struct {
	Cue   string   `json:"cue"`
	Files []string `json:"files"`
}

// Media is a declared disc and the image that matched it.
type Media struct {
	ID         string  `json:"id"`
	Label      string  `json:"label,omitempty"`
	Redump     string  `json:"redump,omitempty"`
	RedumpName string  `json:"redump_name,omitempty"`
	Image      string  `json:"image,omitempty"` // "" if none matched
	Tracks     []Track `json:"tracks,omitempty"`
}

// Track is one of a disc's tracks.
type Track struct {
	Type       string `json:"type"`
	SHA1       string `json:"sha1"`
	RedumpSHA1 string `json:"redump_sha1,omitempty"`
}

// Archive is an archive as written.
type Archive struct {
	Path       string  `json:"path"`
	Size       int64   `json:"size"`
	SHA1       string  `json:"sha1"`
	CRC32      string  `json:"crc32"`
	Content    int64   `json:"content"`    // entries' uncompressed bytes
	Compressed int64   `json:"compressed"` // entries' compressed bytes
	Entries    []Entry `json:"entries"`
}

// Entry is one archive entry.
type Entry struct {
	Path       string    `json:"path"`
	Size       int64     `json:"size"`
	Compressed int64     `json:"compressed"`
	Method     string    `json:"method"` // deflate or store
	CRC32      string    `json:"crc32"`
	Modified   time.Time `json:"modified"`
}

// Ratio is compressed bytes over content bytes, 0 for no content.
func (a *Archive) Ratio() float64 {
	if a.Content == 0 {
		return 0
	}
	return float64(a.Compressed) / float64(a.Content)
}

// Install is what install found and extracted.
type Install struct {
	From      string   `json:"from,omitempty"`  // the save or image read instead of a session
	Drive     string   `json:"drive,omitempty"` // C: or D:
	Dir       string   `json:"dir,omitempty"`   // the folder extracted
	Changes   []Change `json:"changes,omitempty"`
	Other     []Change `json:"other,omitempty"` // on the OS image's C:, not extracted
	Extracted []string `json:"extracted,omitempty"`
	Unchanged []string `json:"unchanged,omitempty"` // already identical in the source
	Skipped   []string `json:"skipped,omitempty"`   // source files left out of the session
	Kept      []string `json:"kept,omitempty"`      // save files kept
	Programs  []string `json:"programs,omitempty"`
	Registry  string   `json:"registry,omitempty"`
	DLLs      []Move   `json:"dlls,omitempty"`
	Folders   []string `json:"folders,omitempty"` // to choose from, when none stood out
}

// Change is a file an installer added, modified or deleted.
type Change struct {
	Path     string    `json:"path"`
	Kind     string    `json:"kind"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
}

// Dump is what dump made, and how it compared with Redump.
type Dump struct {
	Drive    string      `json:"drive,omitempty"`
	From     string      `json:"from,omitempty"` // dump --from's image
	Cue      string      `json:"cue,omitempty"`
	Log      string      `json:"log,omitempty"` // redumper's, kept in the source
	Errors   []string    `json:"errors,omitempty"`
	Tracks   []DumpTrack `json:"tracks,omitempty"`
	Datfile  string      `json:"datfile,omitempty"`
	Redump   string      `json:"redump,omitempty"` // the disc matched, or the closest
	Verified bool        `json:"verified"`
	Matching int         `json:"matching,omitempty"` // of the closest disc's tracks
	Link     string      `json:"link,omitempty"`
}

// DumpTrack is a dumped track in Redump's layout.
type DumpTrack struct {
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	SHA1      string `json:"sha1"`
	Mode2SHA1 string `json:"mode2_sha1,omitempty"`
}

// Folder returns the run's folder.
func (r *Report) Folder() string { return r.dir }

// Duration is how long the run took.
func (r *Report) Duration() time.Duration {
	return time.Duration(r.Seconds * float64(time.Second))
}

// CommandLine is the run as one would type it.
func (r *Report) CommandLine() string { return CommandLine(append([]string{r.Command}, r.Args...)) }

// CommandLine is doszer with args, quoted for a POSIX shell where needed.
func CommandLine(args []string) string {
	parts := []string{"doszer"}
	for _, s := range args {
		if s == "" || strings.ContainsAny(s, " '\"&()$`\\;|<>*?!#~") {
			s = "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, " ")
}

// Subject is what the run was about: its game's name, else its source's
// or archive's base name, else "".
func (r *Report) Subject() string {
	switch {
	case r.Name != "":
		return r.Name
	case r.Source != "":
		return filepath.Base(r.Source)
	case r.Archive != "":
		return strings.TrimSuffix(filepath.Base(r.Archive), filepath.Ext(r.Archive))
	}
	return ""
}

// Load reads the report in a run's folder.
func Load(dir string) (*Report, error) {
	data, err := os.ReadFile(filepath.Join(dir, ReportFile))
	if err != nil {
		return nil, err
	}
	var r Report
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	r.dir = dir
	return &r, nil
}

// List reads every run under dir, newest first. A folder without a
// readable report is skipped; a missing dir is no runs.
func List(dir string) ([]*Report, error) {
	ents, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*Report
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		if r, err := Load(filepath.Join(dir, e.Name())); err == nil {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].Started.Equal(out[j].Started) {
			return out[i].Started.After(out[j].Started)
		}
		return out[i].ID > out[j].ID
	})
	return out, nil
}

// Find returns the runs id names, from runs (newest first), oldest
// first: the run with that ID, every run of the session with that ID,
// or - for a path - the history (History) of every run whose source or
// archive it is; else the run whose ID starts with id, if only one does.
func Find(runs []*Report, id string) []*Report {
	for _, r := range runs {
		if r.ID == id {
			return []*Report{r}
		}
	}
	var seed []*Report
	for _, r := range runs {
		if r.Session != "" && r.Session == id {
			seed = append(seed, r)
		}
	}
	if len(seed) > 0 {
		return oldestFirst(seed)
	}
	if abs, err := filepath.Abs(id); err == nil && (strings.ContainsAny(id, `/\`) || exists(id)) {
		for _, r := range runs {
			if samePath(r.Source, abs) || samePath(r.Archive, abs) {
				seed = append(seed, r)
			}
		}
		if len(seed) > 0 {
			return related(runs, seed)
		}
	}
	for _, r := range runs {
		if strings.HasPrefix(r.ID, id) {
			seed = append(seed, r)
		}
	}
	if len(seed) != 1 {
		return nil // none, or ambiguous
	}
	return seed
}

// History returns every run about the same game as r, oldest first: its
// session's, those whose source or archive is r's, and so on along the
// chain - the source's build, the archive's repack, the repack's set.
func History(runs []*Report, r *Report) []*Report {
	return related(runs, []*Report{r})
}

// related returns seed and every run linked to it, directly or through
// others, by a session or a path one's source or archive and the
// other's share.
func related(runs, seed []*Report) []*Report {
	in := map[*Report]bool{}
	paths := map[string]bool{}
	sessions := map[string]bool{}
	key := func(p string) string {
		if filepath.Separator == '\\' {
			return strings.ToLower(p)
		}
		return p
	}
	add := func(r *Report) {
		in[r] = true
		for _, p := range []string{r.Source, r.Archive} {
			if p != "" {
				paths[key(p)] = true
			}
		}
		if r.Session != "" {
			sessions[r.Session] = true
		}
	}
	for _, r := range seed {
		add(r)
	}
	for grew := true; grew; {
		grew = false
		for _, r := range runs {
			if in[r] {
				continue
			}
			if sessions[r.Session] && r.Session != "" || r.Source != "" && paths[key(r.Source)] || r.Archive != "" && paths[key(r.Archive)] {
				add(r)
				grew = true
			}
		}
	}
	var out []*Report
	for _, r := range runs {
		if in[r] {
			out = append(out, r)
		}
	}
	return oldestFirst(out)
}

// Latest returns the newest run that wrote archive, or nil.
func Latest(runs []*Report, archive string) *Report {
	for _, r := range runs {
		if samePath(r.Archive, archive) {
			return r
		}
	}
	return nil
}

func oldestFirst(rs []*Report) []*Report {
	sort.SliceStable(rs, func(i, j int) bool { return rs[i].Started.Before(rs[j].Started) })
	return rs
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return a == b || strings.EqualFold(a, b) && (filepath.Separator == '\\' || strings.Contains(a, `:\`))
}
