// Package output renders API responses in one of four formats:
// json (default), yaml, jsonl, table — optionally narrowed by --columns
// (dotted-path projection). Heavier transformations belong on the right
// of a `| jq` pipe — we deliberately don't ship a half-jq. Epoch-millis
// date fields (anything with format=date-time-millis in the spec) are
// humanised to ISO-8601 across every format so jq pipelines and humans
// see the same readable timestamp.
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
	"time"

	"gopkg.in/yaml.v3"
)

// Globals are set by the root command's PersistentPreRunE, then read by
// generated subcommands when they call Print.
var (
	format   = "json"
	columns  []string
	cellMax  = 28
	dateKeys = map[string]bool{}
)

// SetFormat updates the global output format. Empty string is ignored.
func SetFormat(f string) {
	if f != "" {
		format = f
	}
}

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

// SetCellMax sets the per-cell character cap for table output. 0 disables
// truncation entirely (useful on wide terminals where full IDs / addresses
// matter more than alignment). Negative or unset values keep the default.
func SetCellMax(n int) {
	if n >= 0 {
		cellMax = n
	}
}

// SetDateKeys configures the set of property names whose values are
// epoch-milliseconds — populated at CLI startup from the OpenAPI spec by
// collecting leaves with `format: "date-time-millis"`. Output formatters
// humanize values for these keys to ISO-8601 UTC. Empty set disables
// humanization entirely.
func SetDateKeys(keys map[string]bool) {
	dateKeys = make(map[string]bool, len(keys))
	for k := range keys {
		dateKeys[k] = true
	}
}

// Print renders v according to the global format/columns.
func Print(v interface{}) error {
	f := strings.ToLower(format)
	// Table renders columns through selectTableColumns (which already
	// honors the global `columns` list); for json/yaml/jsonl we narrow
	// the value here so the rendered output mirrors the user's pick.
	if len(columns) > 0 && f != "table" {
		v = applyColumns(v)
	}
	// Convert epoch-millis date fields to ISO-8601 across every format. The
	// public API ships timestamps as 13-digit millisecond strings ("1777304897620"),
	// which is unreadable in CLI output and unhelpful for piping to grep/awk.
	// jq keeps working — just on the ISO string instead of the raw integer.
	v = humanizeDates(v)
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

// HumanizeDates is the exported entry point for the same transformation Print
// applies — used by hand-written commands (e.g. `bron tx subscribe`) that
// build their own output pipelines but still want ISO dates by default.
func HumanizeDates(v interface{}) interface{} { return humanizeDates(v) }

// humanizeDates walks v and rewrites any value whose key is registered in the
// dateKeys set (populated at startup from the OpenAPI spec via SetDateKeys —
// every leaf with `format: "date-time-millis"`) into a UTC ISO-8601 timestamp.
// Returns a new tree; the input is not mutated.
func humanizeDates(v interface{}) interface{} {
	switch t := v.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(t))
		for k, val := range t {
			if isDateColumn(k) {
				if iso, ok := tryEpochMsToISO(val); ok {
					out[k] = iso
					continue
				}
			}
			out[k] = humanizeDates(val)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(t))
		for i, item := range t {
			out[i] = humanizeDates(item)
		}
		return out
	case orderedMap:
		// orderedMap surfaces from applyColumns. Walk it preserving key order.
		out := orderedMap{keys: append([]string(nil), t.keys...), vals: make(map[string]interface{}, len(t.vals))}
		for k, val := range t.vals {
			if isDateColumn(k) {
				if iso, ok := tryEpochMsToISO(val); ok {
					out.vals[k] = iso
					continue
				}
			}
			out.vals[k] = humanizeDates(val)
		}
		return out
	}
	return v
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
	om := &orderedMap{vals: map[string]interface{}{}}
	for _, c := range columns {
		val, ok := getDotPath(m, c)
		if !ok {
			continue
		}
		setOrderedDotPath(om, c, val)
	}
	return *om
}

// setOrderedDotPath places val into om at the given dot-path, creating
// intermediate orderedMaps so the rendered JSON / YAML keeps the nested
// shape ({"params": {"amount": "10"}}) instead of flat dotted keys.
func setOrderedDotPath(om *orderedMap, path string, val interface{}) {
	parts := strings.SplitN(path, ".", 2)
	root := parts[0]
	if len(parts) == 1 {
		if _, exists := om.vals[root]; !exists {
			om.keys = append(om.keys, root)
		}
		om.vals[root] = val
		return
	}
	var sub *orderedMap
	if existing, ok := om.vals[root]; ok {
		if e, ok := existing.(orderedMap); ok {
			sub = &orderedMap{keys: append([]string(nil), e.keys...), vals: e.vals}
		}
	}
	if sub == nil {
		om.keys = append(om.keys, root)
		sub = &orderedMap{vals: map[string]interface{}{}}
	}
	setOrderedDotPath(sub, parts[1], val)
	om.vals[root] = *sub
}

// getDotPath walks a "foo.bar.baz" path through nested maps. Returns the leaf
// value and true if every segment resolved.
func getDotPath(v interface{}, path string) (interface{}, bool) {
	cur := v
	for _, p := range strings.Split(path, ".") {
		m, ok := cur.(map[string]interface{})
		if !ok {
			return nil, false
		}
		cur, ok = m[p]
		if !ok {
			return nil, false
		}
	}
	return cur, true
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
	defer func() { _ = enc.Close() }()
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
		_, _ = fmt.Fprintln(w, v)
		return nil
	}
	keys := sortedKeys(m)
	if len(columns) > 0 {
		keys = columns
	}
	width := 0
	for _, k := range keys {
		if len(k) > width {
			width = len(k)
		}
	}
	for _, k := range keys {
		val, _ := getDotPath(m, k)
		_, _ = fmt.Fprintf(w, "%-*s  %s\n", width, k, formatTableCell(k, val))
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

	cells := make([][]string, len(rows))
	widths := make([]int, len(keys))
	for i, k := range keys {
		widths[i] = len(k)
	}
	for i, row := range rows {
		cells[i] = make([]string, len(keys))
		for j, k := range keys {
			val, _ := getDotPath(row, k)
			s := truncCell(formatTableCell(k, val), cellMax)
			cells[i][j] = s
			if l := runeLen(s); l > widths[j] {
				widths[j] = l
			}
		}
	}
	for i, k := range keys {
		_, _ = fmt.Fprintf(w, "%-*s  ", widths[i], truncCell(k, cellMax))
	}
	_, _ = fmt.Fprintln(w)
	for _, row := range cells {
		for i, c := range row {
			_, _ = fmt.Fprintf(w, "%-*s  ", widths[i], c)
		}
		_, _ = fmt.Fprintln(w)
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
	case nil, string, bool, float64, int, json.Number:
		return true
	}
	return false
}

// formatScalar prints a leaf value. json.Number arrives from the client's
// UseNumber decode and must be rendered by its underlying string so decimal
// precision and trailing zeros survive (`"0.10"` stays `0.10`, never `0.1`).
func formatScalar(v interface{}) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return SanitizeForTerminal(t)
	case json.Number:
		return string(t)
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
		return SanitizeForTerminal(string(b))
	}
}

// SanitizeForTerminal strips C0/C1 control bytes from a server-controlled
// string before it lands in a human-readable terminal context (table cells,
// stderr error fields). Without it, an attacker with workspace-write access
// could embed `\x1b]0;evil\x07` (title hijack) or `\r` (overwrite earlier
// columns) in account names / memos. JSON/YAML structured encoders escape
// control chars natively, so this is only needed on the raw-print path.
//
// Keeps `\t` (column separator) and `\n` (line break in long strings).
func SanitizeForTerminal(s string) string {
	if !strings.ContainsAny(s, "\x00\x01\x02\x03\x04\x05\x06\x07\x08\x0b\x0c\x0d\x0e\x0f\x10\x11\x12\x13\x14\x15\x16\x17\x18\x19\x1a\x1b\x1c\x1d\x1e\x1f\x7f") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\t' || r == '\n':
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			// drop
		case r >= 0x80 && r < 0xa0:
			// drop C1 controls
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// formatTableCell renders a value for a table cell. Like formatScalar, but
// auto-converts epoch-millis values in *At/*Time columns to readable UTC.
func formatTableCell(key string, v interface{}) string {
	if isDateColumn(key) {
		if iso, ok := tryEpochMsToISO(v); ok {
			return iso
		}
	}
	return formatScalar(v)
}

func isDateColumn(key string) bool {
	// Match the last path segment (handles dot-paths like "transaction.createdAt").
	if i := strings.LastIndex(key, "."); i >= 0 {
		key = key[i+1:]
	}
	return dateKeys[key]
}

// tryEpochMsToISO converts a 13-digit string or numeric millis-since-epoch to
// an ISO-8601 / RFC3339 UTC timestamp ("2026-04-27T15:48:17.620Z"). Anything
// else is rejected.
func tryEpochMsToISO(v interface{}) (string, bool) {
	var ms int64
	switch t := v.(type) {
	case string:
		if len(t) != 13 {
			return "", false
		}
		n, err := strconv.ParseInt(t, 10, 64)
		if err != nil {
			return "", false
		}
		ms = n
	case json.Number:
		s := string(t)
		if len(s) != 13 {
			return "", false
		}
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return "", false
		}
		ms = n
	case float64:
		if t < 1e12 || t >= 1e14 {
			return "", false
		}
		ms = int64(t)
	case int64:
		if t < 1e12 || t >= 1e14 {
			return "", false
		}
		ms = t
	default:
		return "", false
	}
	return time.UnixMilli(ms).UTC().Format("2006-01-02T15:04:05.000Z"), true
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

// UseColor reports whether ANSI colors should be used when writing to w.
// Honors NO_COLOR (disable, any value) and FORCE_COLOR (enable, any value),
// otherwise enables colors only for ttys. Exposed so other packages can match
// the CLI's rendering rules without rewriting the detection logic.
func UseColor(w io.Writer) bool { return useColor(w) }

// ColorizeJSON wraps a pre-formatted JSON byte slice in the same ANSI color
// scheme `--output json` uses (cyan keys, green strings, yellow numbers,
// magenta bool/null). Pass through unchanged input that isn't a tty target.
func ColorizeJSON(b []byte) []byte { return colorizeJSON(b) }

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

