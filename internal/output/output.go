// Package output renders API responses in one of the formats from BRO-486:
// json (default), yaml, jsonl, table — optionally filtered by --query
// (a small JSONPath subset).
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Globals are set by the root command's PersistentPreRun, then read by
// generated subcommands when they call Print.
var (
	format = "json"
	query  = ""
)

// SetFormat updates the global output format. Empty string is ignored.
func SetFormat(f string) {
	if f != "" {
		format = f
	}
}

// SetQuery updates the global JSONPath query. Empty string disables filtering.
func SetQuery(q string) { query = q }

// Print renders v according to the global format/query.
func Print(v interface{}) error {
	if query != "" {
		filtered, err := applyQuery(v, query)
		if err != nil {
			return err
		}
		v = filtered
	}
	switch strings.ToLower(format) {
	case "", "json":
		return printJSON(os.Stdout, v)
	case "yaml", "yml":
		return printYAML(os.Stdout, v)
	case "jsonl":
		return printJSONL(os.Stdout, v)
	case "table":
		return printTable(os.Stdout, v)
	default:
		return fmt.Errorf("unknown --output %q (table|json|yaml|jsonl)", format)
	}
}

// PrintJSON is kept as an alias for callers that want JSON regardless of globals.
func PrintJSON(v interface{}) error {
	return printJSON(os.Stdout, v)
}

func printJSON(w io.Writer, v interface{}) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func printYAML(w io.Writer, v interface{}) error {
	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	defer enc.Close()
	return enc.Encode(v)
}

func printJSONL(w io.Writer, v interface{}) error {
	arr, ok := asArray(v)
	if !ok {
		return printJSON(w, v)
	}
	enc := json.NewEncoder(w)
	for _, item := range arr {
		if err := enc.Encode(item); err != nil {
			return err
		}
	}
	return nil
}

func printTable(w io.Writer, v interface{}) error {
	if arr, ok := asArray(v); ok {
		return printArrayTable(w, arr)
	}
	return printKVTable(w, v)
}

// asArray returns a flat array if v is one, or unwraps an object that has
// exactly one array property. This matches the Bron API list-shape (e.g.
// `{"transactions": [...]}`).
func asArray(v interface{}) ([]interface{}, bool) {
	if arr, ok := v.([]interface{}); ok {
		return arr, true
	}
	if m, ok := v.(map[string]interface{}); ok {
		var found []interface{}
		var hits int
		for _, val := range m {
			if a, ok := val.([]interface{}); ok {
				found = a
				hits++
			}
		}
		if hits == 1 {
			return found, true
		}
	}
	return nil, false
}

func printKVTable(w io.Writer, v interface{}) error {
	m, ok := v.(map[string]interface{})
	if !ok {
		fmt.Fprintln(w, v)
		return nil
	}
	keys := sortedKeys(m)
	width := 0
	for _, k := range keys {
		if len(k) > width {
			width = len(k)
		}
	}
	for _, k := range keys {
		fmt.Fprintf(w, "%-*s  %s\n", width, k, formatScalar(m[k]))
	}
	return nil
}

func printArrayTable(w io.Writer, arr []interface{}) error {
	keySet := map[string]struct{}{}
	rows := make([]map[string]interface{}, 0, len(arr))
	for _, item := range arr {
		m, ok := item.(map[string]interface{})
		if !ok {
			return printJSON(w, arr)
		}
		for k := range m {
			keySet[k] = struct{}{}
		}
		rows = append(rows, m)
	}
	keys := make([]string, 0, len(keySet))
	for k := range keySet {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	cells := make([][]string, len(rows))
	widths := make([]int, len(keys))
	for i, k := range keys {
		widths[i] = len(k)
	}
	for i, row := range rows {
		cells[i] = make([]string, len(keys))
		for j, k := range keys {
			s := formatScalar(row[k])
			cells[i][j] = s
			if len(s) > widths[j] {
				widths[j] = len(s)
			}
		}
	}
	for i, k := range keys {
		fmt.Fprintf(w, "%-*s  ", widths[i], k)
	}
	fmt.Fprintln(w)
	for _, row := range cells {
		for i, c := range row {
			fmt.Fprintf(w, "%-*s  ", widths[i], c)
		}
		fmt.Fprintln(w)
	}
	return nil
}

func formatScalar(v interface{}) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case int:
		return strconv.Itoa(t)
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

func sortedKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// applyQuery applies a simplified JSONPath subset:
//
//	.foo               object key
//	.foo.bar           nested key
//	.foo[0]            array index
//	.foo[*]            map remaining selector over every element
//	.foo[*].bar        same, then pick `.bar` from each
func applyQuery(v interface{}, q string) (interface{}, error) {
	q = strings.TrimSpace(q)
	if q == "" || q == "." {
		return v, nil
	}
	if !strings.HasPrefix(q, ".") {
		return nil, fmt.Errorf("--query must start with '.': %s", q)
	}
	rest := q[1:]
	cur := v
	for rest != "" {
		// Read the next selector: either a dotted key or [index|*].
		if rest[0] == '[' {
			j := strings.IndexByte(rest, ']')
			if j < 0 {
				return nil, fmt.Errorf("--query: unterminated [ in %s", q)
			}
			inner := rest[1:j]
			rest = rest[j+1:]
			arr, ok := cur.([]interface{})
			if !ok {
				return nil, fmt.Errorf("--query: cannot index %T with [%s]", cur, inner)
			}
			if inner == "*" {
				out := make([]interface{}, 0, len(arr))
				for _, item := range arr {
					sub := item
					if rest != "" {
						v, err := applyQuery(sub, "."+strings.TrimPrefix(rest, "."))
						if err != nil {
							return nil, err
						}
						sub = v
					}
					out = append(out, sub)
				}
				return out, nil
			}
			n, err := strconv.Atoi(inner)
			if err != nil {
				return nil, fmt.Errorf("--query: invalid index [%s]", inner)
			}
			if n < 0 || n >= len(arr) {
				return nil, nil
			}
			cur = arr[n]
			continue
		}

		// Dotted key.
		end := len(rest)
		for i := 0; i < len(rest); i++ {
			if rest[i] == '.' || rest[i] == '[' {
				end = i
				break
			}
		}
		key := rest[:end]
		m, ok := cur.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("--query: cannot index %T with %q", cur, key)
		}
		cur = m[key]
		rest = strings.TrimPrefix(rest[end:], ".")
	}
	return cur, nil
}
