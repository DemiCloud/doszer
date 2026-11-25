// Package output is doszer's --format: the text output, fancy (a spinner
// and finished steps, internal/status) or plain, or a command's result
// encoded as JSON, YAML, TOML or CSV for scripts. The names and the
// automatic fancy/plain choice match the sibling romctl's --format.
package output

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"go.yaml.in/yaml/v3"
)

// Format is an output mode.
type Format string

const (
	Auto  Format = "" // not given: Resolve picks Fancy or Plain
	Fancy Format = "fancy"
	Plain Format = "plain"
	JSON  Format = "json"
	YAML  Format = "yaml"
	TOML  Format = "toml"
	CSV   Format = "csv"
)

// Names lists every format Parse accepts.
var Names = []string{"fancy", "plain", "json", "yaml", "toml", "csv"}

// Parse checks a --format value; "" is Auto.
func Parse(s string) (Format, error) {
	if s == "" {
		return Auto, nil
	}
	for _, n := range Names {
		if s == n {
			return Format(s), nil
		}
	}
	return "", fmt.Errorf("invalid --format %q (want one of: %s)", s, strings.Join(Names, ", "))
}

// Resolve turns Auto into Fancy when f is a terminal (and TERM isn't
// "dumb"), else Plain. Any other format is kept: a choice wins over
// detection.
func (f Format) Resolve(out *os.File) Format {
	if f != Auto {
		return f
	}
	if os.Getenv("TERM") != "dumb" {
		if fi, err := out.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
			return Fancy
		}
	}
	return Plain
}

// Structured reports a machine-readable format: the command prints its
// result encoded, and nothing else, on standard output.
func (f Format) Structured() bool {
	switch f {
	case JSON, YAML, TOML, CSV:
		return true
	}
	return false
}

// Encode writes v as JSON, YAML or TOML. v's fields need json, yaml and
// toml tags naming them alike. TOML needs a table at the top, so v
// should be a struct.
func Encode(w io.Writer, f Format, v any) error {
	switch f {
	case JSON:
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	case YAML:
		enc := yaml.NewEncoder(w)
		enc.SetIndent(2)
		if err := enc.Encode(v); err != nil {
			return err
		}
		return enc.Close()
	case TOML:
		return toml.NewEncoder(w).Encode(v)
	}
	return fmt.Errorf("not a structured format: %s", f)
}

// EncodeCSV writes a header row, then rows.
func EncodeCSV(w io.Writer, header []string, rows [][]string) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(header); err != nil {
		return err
	}
	if err := cw.WriteAll(rows); err != nil {
		return err
	}
	return cw.Error()
}
