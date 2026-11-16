// Package manifest parses and validates DOSZ.TOML manifests as defined by the
// DOSZ Manifest Specification (spec/MANIFEST.md).
package manifest

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/DemiCloud/doszer/internal/padmap"
)

// FileName is the manifest's name within a source directory and an archive.
const FileName = "DOSZ.TOML"

// FormatVersion is the specification version this package implements.
const FormatVersion = 1

// Audio encodings for CD audio tracks.
const (
	AudioOgg = "ogg"
	AudioWAV = "wav"
)

// Track types.
const (
	TrackData  = "data"
	TrackAudio = "audio"
)

// Manifest is a parsed DOSZ.TOML.
type Manifest struct {
	Format  int        `toml:"format"`
	Name    string     `toml:"name"`
	Notes   string     `toml:"notes,omitempty"`
	Boot    Boot       `toml:"boot,omitempty"`
	Layout  Layout     `toml:"layout,omitempty"`
	Pack    Pack       `toml:"pack,omitempty"`
	Source  Source     `toml:"source,omitempty"`
	Extract []Extract  `toml:"extract,omitempty"`
	File    []FileSpec `toml:"file,omitempty"`
	Input   Input      `toml:"input,omitempty"`
}

// Boot selects the boot model and startup behaviour (spec §5.3).
type Boot struct {
	Exec   string `toml:"exec,omitempty"`
	Shell  string `toml:"shell,omitempty"`
	OS     string `toml:"os,omitempty"`
	Parent string `toml:"parent,omitempty"`
	Disc   string `toml:"disc,omitempty"`
	Skip   *int   `toml:"skip,omitempty"`
}

// Layout controls trimming, flattening and rewriting (spec §5.4).
type Layout struct {
	Trim    []string  `toml:"trim,omitempty"`
	Flatten bool      `toml:"flatten,omitempty"`
	Rewrite []Rewrite `toml:"rewrite,omitempty"`
}

// Rewrite is a literal, case-insensitive text replacement.
type Rewrite struct {
	From  string   `toml:"from"`
	To    string   `toml:"to"`
	Files []string `toml:"files"`
}

// Pack controls archive compression and audio encoding (spec §5.5).
type Pack struct {
	Store []string `toml:"store,omitempty"`
	Audio string   `toml:"audio,omitempty"`
}

// Source identifies the media an archive is built from (spec §5.6).
type Source struct {
	Media []Media `toml:"media,omitempty"`
}

// Media is one disc image, identified by its normalized track hashes.
type Media struct {
	ID     string  `toml:"id"`
	Label  string  `toml:"label,omitempty"`
	Redump string  `toml:"redump,omitempty"`
	Tracks []Track `toml:"tracks"`
}

// Track is one track of a disc image.
type Track struct {
	Type string `toml:"type"`
	SHA1 string `toml:"sha1"`
}

// Extract copies content from identified media into the build (spec §5.7).
type Extract struct {
	Media string `toml:"media"`
	From  string `toml:"from"`
	To    string `toml:"to"`
}

// FileSpec is a file whose content is given in the manifest (spec §5.8).
type FileSpec struct {
	Path string  `toml:"path"`
	Text *string `toml:"text"`
}

// Input holds gamepad mappings (spec §5.9).
type Input struct {
	Pad []Pad `toml:"pad,omitempty"`
}

// Pad maps one controller port (spec §5.9). Each control's value is a
// binding string; fields follow padmap.Controls' order.
type Pad struct {
	B           string   `toml:"b,omitempty"`
	Y           string   `toml:"y,omitempty"`
	Select      string   `toml:"select,omitempty"`
	Start       string   `toml:"start,omitempty"`
	Up          string   `toml:"up,omitempty"`
	Down        string   `toml:"down,omitempty"`
	Left        string   `toml:"left,omitempty"`
	Right       string   `toml:"right,omitempty"`
	A           string   `toml:"a,omitempty"`
	X           string   `toml:"x,omitempty"`
	L           string   `toml:"l,omitempty"`
	R           string   `toml:"r,omitempty"`
	L2          string   `toml:"l2,omitempty"`
	R2          string   `toml:"r2,omitempty"`
	L3          string   `toml:"l3,omitempty"`
	R3          string   `toml:"r3,omitempty"`
	LStickLeft  string   `toml:"lstick_left,omitempty"`
	LStickRight string   `toml:"lstick_right,omitempty"`
	LStickUp    string   `toml:"lstick_up,omitempty"`
	LStickDown  string   `toml:"lstick_down,omitempty"`
	RStickLeft  string   `toml:"rstick_left,omitempty"`
	RStickRight string   `toml:"rstick_right,omitempty"`
	RStickUp    string   `toml:"rstick_up,omitempty"`
	RStickDown  string   `toml:"rstick_down,omitempty"`
	Wheel       []string `toml:"wheel,omitempty"`
}

// Padmap returns the pad in padmap's form. A control set to "" is
// treated as unset, as omitempty leaves it out when re-serialized.
func (p *Pad) Padmap() padmap.Pad {
	values := []string{
		p.B, p.Y, p.Select, p.Start, p.Up, p.Down, p.Left, p.Right,
		p.A, p.X, p.L, p.R, p.L2, p.R2, p.L3, p.R3,
		p.LStickLeft, p.LStickRight, p.LStickUp, p.LStickDown,
		p.RStickLeft, p.RStickRight, p.RStickUp, p.RStickDown,
	}
	controls := make(map[string]string)
	for i, v := range values {
		if v != "" {
			controls[padmap.Controls[i]] = v
		}
	}
	return padmap.Pad{Controls: controls, Wheel: p.Wheel}
}

// Pads returns every [[input.pad]] entry in padmap's form.
func (m *Manifest) Pads() []padmap.Pad {
	pads := make([]padmap.Pad, len(m.Input.Pad))
	for i := range m.Input.Pad {
		pads[i] = m.Input.Pad[i].Padmap()
	}
	return pads
}

// Model is an archive's boot model (spec §4).
type Model int

// Boot models.
const (
	ModelPlain Model = iota
	ModelShell
	ModelOS
)

// String returns the model's name as used in the specification.
func (m Model) String() string {
	switch m {
	case ModelShell:
		return "shell"
	case ModelOS:
		return "OS image"
	default:
		return "plain"
	}
}

// Model returns the boot model selected by the manifest.
func (m *Manifest) Model() Model {
	switch {
	case m.Boot.OS != "":
		return ModelOS
	case m.Boot.Shell != "":
		return ModelShell
	default:
		return ModelPlain
	}
}

// AudioEncoding returns the effective CD audio encoding.
func (m *Manifest) AudioEncoding() string {
	if m.Pack.Audio == "" {
		return AudioOgg
	}
	return m.Pack.Audio
}

// Load reads, parses and validates the manifest in a source directory.
// The source directory's base name is the default for Name.
func Load(sourceDir string) (*Manifest, error) {
	data, err := os.ReadFile(filepath.Join(sourceDir, FileName))
	if err != nil {
		return nil, err
	}

	abs, err := filepath.Abs(sourceDir)
	if err != nil {
		return nil, err
	}

	return Parse(data, filepath.Base(abs))
}

// Parse parses and validates manifest data. defaultName is used when the
// manifest does not set name.
func Parse(data []byte, defaultName string) (*Manifest, error) {
	var raw map[string]any
	if err := toml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("%s: %w", FileName, err)
	}

	if errs := checkKeys(raw); len(errs) > 0 {
		return nil, joinErrors(errs)
	}

	var m Manifest
	dec := toml.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("%s: %w", FileName, err)
	}

	if m.Name == "" {
		m.Name = defaultName
	}

	if errs := m.validate(); len(errs) > 0 {
		return nil, joinErrors(errs)
	}

	return &m, nil
}

var (
	sha1Pattern  = regexp.MustCompile(`^[0-9a-f]{40}$`)
	osExtensions = []string{".vhd", ".img", ".ima", ".dosz", ".zip"}
)

// validate applies the rules of spec §11 that concern the manifest itself.
func (m *Manifest) validate() []error {
	var errs []error
	add := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf(format, args...))
	}

	if m.Format != FormatVersion {
		add("format: must be %d, got %d", FormatVersion, m.Format)
	}

	if strings.TrimSpace(m.Name) == "" {
		add("name: must not be empty")
	}

	// [boot]
	startups := 0
	for _, s := range []string{m.Boot.Exec, m.Boot.Shell, m.Boot.OS} {
		if s != "" {
			startups++
		}
	}
	if startups > 1 {
		add("boot: at most one of exec, shell and os may be set")
	}

	for _, kv := range [][2]string{{"boot.shell", m.Boot.Shell}, {"boot.os", m.Boot.OS}} {
		if kv[1] == "" {
			continue
		}
		if err := checkSystemPath(kv[1]); err != nil {
			add("%s: %v", kv[0], err)
		}
	}

	if p := m.Boot.Parent; p != "" {
		switch {
		case strings.ContainsAny(p, `/\`):
			add("boot.parent: must be a bare file name, got %q", p)
		case filepath.Ext(p) == "":
			add("boot.parent: must include an extension, got %q", p)
		}
	}

	if m.Boot.Skip != nil {
		if *m.Boot.Skip < 0 {
			add("boot.skip: must not be negative")
		}
		if startups == 0 {
			add("boot.skip: requires one of exec, shell or os")
		}
	}

	// [layout]
	for i, pattern := range m.Layout.Trim {
		if err := checkRelativePath(pattern); err != nil {
			add("layout.trim[%d]: %v", i, err)
		}
	}
	for i, rw := range m.Layout.Rewrite {
		if rw.From == "" {
			add("layout.rewrite[%d].from: must not be empty", i)
		}
		if len(rw.Files) == 0 {
			add("layout.rewrite[%d].files: must not be empty", i)
		}
		for j, pattern := range rw.Files {
			if err := checkRelativePath(pattern); err != nil {
				add("layout.rewrite[%d].files[%d]: %v", i, j, err)
			}
		}
	}

	// [pack]
	for i, ext := range m.Pack.Store {
		if ext == "" || strings.HasPrefix(ext, ".") || strings.ContainsAny(ext, `/\`) {
			add("pack.store[%d]: must be an extension without a leading dot, got %q", i, ext)
		}
	}
	if a := m.Pack.Audio; a != "" && a != AudioOgg && a != AudioWAV {
		add("pack.audio: must be %q or %q, got %q", AudioOgg, AudioWAV, a)
	}

	// [[source.media]]
	ids := make(map[string]bool)
	for i, media := range m.Source.Media {
		if media.ID == "" {
			add("source.media[%d].id: must not be empty", i)
		} else if ids[media.ID] {
			add("source.media[%d].id: duplicate id %q", i, media.ID)
		}
		ids[media.ID] = true

		if len(media.Tracks) == 0 {
			add("source.media[%d].tracks: must not be empty", i)
		}
		for j, t := range media.Tracks {
			if t.Type != TrackData && t.Type != TrackAudio {
				add("source.media[%d].tracks[%d].type: must be %q or %q, got %q", i, j, TrackData, TrackAudio, t.Type)
			}
			if !sha1Pattern.MatchString(t.SHA1) {
				add("source.media[%d].tracks[%d].sha1: must be 40 lowercase hexadecimal characters", i, j)
			}
		}
	}

	// [[extract]]
	for i, ex := range m.Extract {
		if !ids[ex.Media] {
			add("extract[%d].media: unknown media id %q", i, ex.Media)
		}
		if ex.From != "/" { // the root directory (§5.7)
			if err := CheckPath(ex.From); err != nil {
				add("extract[%d].from: %v", i, err)
			}
		}
		if err := checkRelativePath(ex.To); err != nil {
			add("extract[%d].to: %v", i, err)
		}
	}

	// [[file]]
	for i, f := range m.File {
		if err := checkRelativePath(f.Path); err != nil {
			add("file[%d].path: %v", i, err)
		}
		if f.Text == nil {
			add("file[%d].text: must be set", i)
		}
	}

	// [[input.pad]]
	if n := len(m.Input.Pad); n > padmap.MaxPads {
		add("input.pad: at most %d entries, got %d", padmap.MaxPads, n)
	}
	for i, p := range m.Pads() {
		for _, err := range p.Validate() {
			add("input.pad[%d].%v", i, err)
		}
	}

	return errs
}

// checkSystemPath validates a path relative to DOSBox Pure's system directory.
func checkSystemPath(p string) error {
	if strings.HasPrefix(p, "/") {
		return fmt.Errorf("must be relative to the system directory, got %q", p)
	}
	if err := CheckPath(p); err != nil {
		return err
	}

	lower := strings.ToLower(p)
	for _, ext := range osExtensions {
		if strings.HasSuffix(lower, ext) {
			return fmt.Errorf("must not include the file extension, got %q", p)
		}
	}

	return nil
}

// checkRelativePath validates a build-relative path or glob pattern.
func checkRelativePath(p string) error {
	if strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`) || len(p) >= 2 && p[1] == ':' {
		return fmt.Errorf("must be relative, got %q", p)
	}
	return CheckPath(p)
}

// CheckPath reports whether p is a path as spec §2 defines it: "/"
// separated, with no empty component (no leading, trailing or doubled
// "/"), no "." or ".." component, and no "\". It doesn't check that p is
// relative beyond that. An archive's entry names must be such paths too
// (§8.2).
func CheckPath(p string) error {
	if p == "" {
		return errors.New("must not be empty")
	}
	if strings.Contains(p, `\`) {
		return fmt.Errorf("must use / as separator, got %q", p)
	}
	for _, part := range strings.Split(p, "/") {
		switch part {
		case "":
			return fmt.Errorf("must not have an empty component (a leading, trailing or doubled /), got %q", p)
		case ".", "..":
			return fmt.Errorf("must not have a %q component, got %q", part, p)
		}
	}
	return nil
}

func joinErrors(errs []error) error {
	wrapped := make([]error, len(errs))
	for i, e := range errs {
		wrapped[i] = fmt.Errorf("%s: %w", FileName, e)
	}
	return errors.Join(wrapped...)
}
