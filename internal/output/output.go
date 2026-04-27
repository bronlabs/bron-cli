// Package output renders API responses in one of the formats from BRO-486:
// json (default), yaml, jsonl, table — optionally filtered by --query
// (a small JSONPath subset).
package output

import (
	"bytes"
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
	format  = "json"
	query   = ""
	columns []string
)

// SetFormat updates the global output format. Empty string is ignored.
func SetFormat(f string) {
	if f != "" {
		format = f
	}
}

// SetQuery updates the global JSONPath query. Empty string disables filtering.
func SetQuery(q string) { query = q }

// SetColumns selects which fields to render in --output table. Comma-separated
// list (e.g. "transactionId,status,createdAt"). Empty disables filtering.
func SetColumns(c string) {
	columns = nil
	for _, p := range strings.Split(c, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			columns = append(columns, p)
		}
	}
}

// Print renders v according to the global format/query.
func Print(v interface{}) error {
	if query != "" {
		filtered, err := applyQuery(v, query)
		if err != nil {
			return err
		}
		v = filtered
	}
	f := strings.ToLower(format)
	// Table renders columns through selectTableColumns (which already
	// honors the global `columns` list); for json/yaml/jsonl we narrow
	// the value here so the rendered output mirrors the user's pick.
	if len(columns) > 0 && f != "table" {
		v = applyColumns(v)
	}
	switch f {
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

// orderedMap preserves field order across json / yaml rendering. Used when
// --columns explicitly fixes which fields to print and in what order.
type orderedMap struct {
	keys []string
	vals map[string]interface{}
}

func (o orderedMap) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		buf.Write(kb)
		buf.WriteByte(':')
		vb, err := json.Marshal(o.vals[k])
		if err != nil {
			return nil, err
		}
		buf.Write(vb)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

func (o orderedMap) MarshalYAML() (interface{}, error) {
	n := &yaml.Node{Kind: yaml.MappingNode}
	for _, k := range o.keys {
		kn := &yaml.Node{Kind: yaml.ScalarNode, Value: k}
		vn := &yaml.Node{}
		if err := vn.Encode(o.vals[k]); err != nil {
			return nil, err
		}
		n.Content = append(n.Content, kn, vn)
	}
	return n, nil
}

// applyColumns narrows v to the fields in `columns`. For maps, picks listed
// keys in order. For arrays, applies recursively. For the typical list-shape
// `{"transactions": [...]}`, recurses into the inner array.
func applyColumns(v interface{}) interface{} {
	switch t := v.(type) {
	case []interface{}:
		out := make([]interface{}, len(t))
		for i, item := range t {
			out[i] = applyColumns(item)
		}
		return out
	case map[string]interface{}:
		if len(t) == 1 {
			for k, val := range t {
				if arr, ok := val.([]interface{}); ok {
					return map[string]interface{}{k: applyColumns(arr)}
				}
			}
		}
		return pickFields(t)
	}
	return v
}

func pickFields(m map[string]interface{}) interface{} {
	om := orderedMap{vals: map[string]interface{}{}}
	for _, c := range columns {
		if val, ok := m[c]; ok {
			om.keys = append(om.keys, c)
			om.vals[c] = val
		}
	}
	return om
}

// PrintJSON is kept as an alias for callers that want JSON regardless of globals.
func PrintJSON(v interface{}) error {
	return printJSON(os.Stdout, v)
}

func printJSON(w io.Writer, v interface{}) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return err
	}
	if useColor(w) {
		_, err := w.Write(colorizeJSON(buf.Bytes()))
		return err
	}
	_, err := w.Write(buf.Bytes())
	return err
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
	color := useColor(w)
	for _, item := range arr {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(item); err != nil {
			return err
		}
		out := buf.Bytes()
		if color {
			out = colorizeJSON(out)
		}
		if _, err := w.Write(out); err != nil {
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
	rows := make([]map[string]interface{}, 0, len(arr))
	for _, item := range arr {
		m, ok := item.(map[string]interface{})
		if !ok {
			return printJSON(w, arr)
		}
		rows = append(rows, m)
	}

	keys := selectTableColumns(rows)

	const cellMax = 28 // per-cell hard cap; long ids/json get an ellipsis
	cells := make([][]string, len(rows))
	widths := make([]int, len(keys))
	for i, k := range keys {
		widths[i] = len(k)
	}
	for i, row := range rows {
		cells[i] = make([]string, len(keys))
		for j, k := range keys {
			s := truncCell(formatScalar(row[k]), cellMax)
			cells[i][j] = s
			if l := runeLen(s); l > widths[j] {
				widths[j] = l
			}
		}
	}
	for i, k := range keys {
		fmt.Fprintf(w, "%-*s  ", widths[i], truncCell(k, cellMax))
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

// selectTableColumns picks which keys to render. Honors an explicit --columns
// list; otherwise drops keys whose values are all empty/nested in every row,
// so wide noise like `extra` and `params` doesn't blow up the layout. The
// remaining columns are sorted alphabetically with a small set of well-known
// "id-ish / status-ish" fields hoisted to the front for legibility.
func selectTableColumns(rows []map[string]interface{}) []string {
	if len(columns) > 0 {
		return columns
	}
	keys := map[string]bool{}
	hasScalar := map[string]bool{}
	for _, row := range rows {
		for k, v := range row {
			keys[k] = true
			if isScalar(v) {
				hasScalar[k] = true
			}
		}
	}
	var out []string
	for k := range keys {
		if hasScalar[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	priority := []string{
		"id", "transactionId", "accountId", "workspaceId", "name",
		"status", "transactionType", "type", "createdAt", "updatedAt",
	}
	rank := map[string]int{}
	for i, p := range priority {
		rank[p] = i + 1
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := rank[out[i]], rank[out[j]]
		if ri == 0 && rj == 0 {
			return false
		}
		if ri == 0 {
			return false
		}
		if rj == 0 {
			return true
		}
		return ri < rj
	})
	return out
}

func isScalar(v interface{}) bool {
	switch v.(type) {
	case nil, string, bool, float64, int:
		return true
	}
	return false
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
	case map[string]interface{}:
		if len(t) == 0 {
			return "{}"
		}
		return "{…}"
	case []interface{}:
		if len(t) == 0 {
			return "[]"
		}
		return fmt.Sprintf("[…%d]", len(t))
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

// truncCell shortens a single cell value to fit table width.
func truncCell(s string, max int) string {
	if max <= 0 || runeLen(s) <= max {
		return s
	}
	r := []rune(s)
	if max < 2 {
		return string(r[:max])
	}
	return string(r[:max-1]) + "…"
}

func runeLen(s string) int { return len([]rune(s)) }

func sortedKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// useColor reports whether ANSI colors should be used when rendering to w.
// Honors NO_COLOR (disable, any value) and FORCE_COLOR (enable, any value).
func useColor(w io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if os.Getenv("FORCE_COLOR") != "" {
		return true
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// colorizeJSON walks a pre-formatted JSON byte slice and wraps tokens in ANSI
// color codes:
//
//	keys           cyan
//	string values  green
//	numbers        yellow
//	bool / null    magenta
//
// The input is expected to be valid JSON (Encoder output), so no recovery
// logic is needed for malformed input.
func colorizeJSON(b []byte) []byte {
	const (
		reset   = "\x1b[0m"
		cyan    = "\x1b[36m"
		green   = "\x1b[32m"
		yellow  = "\x1b[33m"
		magenta = "\x1b[35m"
	)
	var out bytes.Buffer
	out.Grow(len(b) + len(b)/4)
	i := 0
	for i < len(b) {
		c := b[i]
		switch {
		case c == '"':
			start := i
			i++
			for i < len(b) {
				if b[i] == '\\' && i+1 < len(b) {
					i += 2
					continue
				}
				if b[i] == '"' {
					i++
					break
				}
				i++
			}
			s := b[start:i]
			j := i
			for j < len(b) && (b[j] == ' ' || b[j] == '\t') {
				j++
			}
			if j < len(b) && b[j] == ':' {
				out.WriteString(cyan)
			} else {
				out.WriteString(green)
			}
			out.Write(s)
			out.WriteString(reset)
		case c == 't' || c == 'f' || c == 'n':
			start := i
			for i < len(b) && b[i] >= 'a' && b[i] <= 'z' {
				i++
			}
			out.WriteString(magenta)
			out.Write(b[start:i])
			out.WriteString(reset)
		case c == '-' || (c >= '0' && c <= '9'):
			start := i
			for i < len(b) && (b[i] == '-' || b[i] == '+' || b[i] == '.' || b[i] == 'e' || b[i] == 'E' || (b[i] >= '0' && b[i] <= '9')) {
				i++
			}
			out.WriteString(yellow)
			out.Write(b[start:i])
			out.WriteString(reset)
		default:
			out.WriteByte(c)
			i++
		}
	}
	return out.Bytes()
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
				return nil, fmt.Errorf("--query: invalid index [%s] — only [N] (integer) or [*] (all) are supported; --query is navigation only, use --columns or pipe to jq for filtering", inner)
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
