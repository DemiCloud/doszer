package manifest

import (
	"fmt"
	"sort"
	"strings"
)

// extensionPrefix marks implementation-specific keys, which are ignored
// (spec §5.9).
const extensionPrefix = "x-"

// node describes the keys allowed at one level of the manifest.
type node struct {
	tables map[string]*node // keys whose value is a table or array of tables
	leaves map[string]bool  // keys whose value is a scalar or array of scalars
}

func leaves(keys ...string) map[string]bool {
	m := make(map[string]bool, len(keys))
	for _, k := range keys {
		m[k] = true
	}
	return m
}

// schema is the set of keys defined by spec §5.
var schema = &node{
	leaves: leaves("format", "name", "notes"),
	tables: map[string]*node{
		"boot": {leaves: leaves("exec", "shell", "os", "parent", "disc", "skip")},
		"layout": {
			leaves: leaves("trim", "flatten"),
			tables: map[string]*node{
				"rewrite": {leaves: leaves("from", "to", "files")},
			},
		},
		"pack": {leaves: leaves("store", "audio")},
		"source": {
			tables: map[string]*node{
				"media": {
					leaves: leaves("id", "label", "redump"),
					tables: map[string]*node{
						"tracks": {leaves: leaves("type", "sha1")},
					},
				},
			},
		},
		"extract": {leaves: leaves("media", "from", "to")},
		"file":    {leaves: leaves("path", "text")},
	},
}

// checkKeys reports keys not defined by the specification. Keys beginning
// with extensionPrefix, and everything beneath them, are ignored.
func checkKeys(raw map[string]any) []error {
	var errs []error
	walk(schema, raw, "", &errs)
	return errs
}

func walk(n *node, values map[string]any, path string, errs *[]error) {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		if strings.HasPrefix(key, extensionPrefix) {
			continue
		}

		full := key
		if path != "" {
			full = path + "." + key
		}

		if n.leaves[key] {
			continue
		}

		child, ok := n.tables[key]
		if !ok {
			*errs = append(*errs, fmt.Errorf("unknown key %q", full))
			continue
		}

		switch v := values[key].(type) {
		case map[string]any:
			walk(child, v, full, errs)
		case []any:
			for i, item := range v {
				if table, ok := item.(map[string]any); ok {
					walk(child, table, fmt.Sprintf("%s[%d]", full, i), errs)
				} else {
					*errs = append(*errs, fmt.Errorf("%s[%d]: must be a table", full, i))
				}
			}
		default:
			*errs = append(*errs, fmt.Errorf("%s: must be a table", full))
		}
	}
}
