// Package body parses and composes the request body for write operations.
//
// Two baseline sources (mutually exclusive), then field-flag overlay:
//
//  1. --file <path>           baseline JSON read from a file ("-" = stdin)
//  2. --json <inline>         baseline JSON given inline on the command line
//  3. --<field> / --<a>.<b>   structured field flags overlay on top of the baseline
//
// The result is a Go value ready to hand off to the HTTP client.
package body

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// Parse converts the --file or --json argument into a Go value.
// Exactly one of `file` and `inline` may be non-empty (caller validates).
// Both empty → returns nil (caller may still merge field flags).
func Parse(file, inline string) (interface{}, error) {
	if file == "" && inline == "" {
		return nil, nil
	}
	if file != "" && inline != "" {
		return nil, fmt.Errorf("--file and --json are mutually exclusive")
	}

	var data []byte
	switch {
	case inline != "":
		data = []byte(inline)
	case file == "-":
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return nil, fmt.Errorf("read --file from stdin: %w", err)
		}
		data = b
	default:
		b, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("read --file %s: %w", file, err)
		}
		data = b
	}

	var v interface{}
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("parse body as JSON: %w", err)
	}
	return v, nil
}

// Compose merges baseline (from --file/--json) with structured field flags.
// Field flag values override baseline fields at matching dot-paths.
// Field values are JSON-parsed when possible (numbers, booleans, arrays, objects)
// so callers can pass typed scalars; otherwise the value is treated as a string.
//
// Returns nil when the resulting body is empty (no baseline, no flags).
func Compose(baseline interface{}, fields map[string]string) (interface{}, error) {
	hasFields := false
	for _, raw := range fields {
		if raw != "" {
			hasFields = true
			break
		}
	}

	// Field flags only make sense on top of a JSON object. Bulk endpoints take
	// arrays — silently merging --field flags into an empty map and returning
	// it would drop the user's array baseline entirely, so refuse instead.
	if hasFields && baseline != nil {
		if _, ok := baseline.(map[string]interface{}); !ok {
			return nil, fmt.Errorf("--<field> flags require a JSON object baseline; got %T (use --json/--file with an object body, or omit field flags)", baseline)
		}
	}

	root, err := asObject(baseline)
	if err != nil {
		return nil, err
	}

	for path, raw := range fields {
		if raw == "" {
			continue
		}
		var v interface{}
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			v = raw
		}
		setDotPath(root, path, v)
	}

	if len(root) == 0 {
		return baseline, nil
	}
	return root, nil
}

// asObject returns a deep-copied map representation of baseline, or an empty
// map if baseline is nil/non-object. Compose has already rejected the
// non-object-with-fields case, so callers reaching here only see empty maps
// when the baseline was nil or also empty.
func asObject(baseline interface{}) (map[string]interface{}, error) {
	if baseline == nil {
		return map[string]interface{}{}, nil
	}
	m, ok := baseline.(map[string]interface{})
	if !ok {
		return map[string]interface{}{}, nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("clone baseline: %w", err)
	}
	var out map[string]interface{}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("clone baseline: %w", err)
	}
	return out, nil
}

func setDotPath(root map[string]interface{}, dotPath string, value interface{}) {
	keys := strings.Split(dotPath, ".")
	cur := root
	for i, k := range keys {
		if i == len(keys)-1 {
			cur[k] = value
			return
		}
		next, ok := cur[k].(map[string]interface{})
		if !ok {
			next = map[string]interface{}{}
			cur[k] = next
		}
		cur = next
	}
}
