package main

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bronlabs/bron-cli/generated"
)

// buildFullSchemaDoc returns the embedded OpenAPI 3.1 spec verbatim, decorated
// with bron-CLI specifics under the `x-bron-cli` extension. Verbatim because
// the spec IS the source of truth — reshaping it loses information, and any
// jq/swagger consumer already knows how to walk the standard structure.
//
// Under x-bron-cli we add:
//   - commands[]: each (resource verb) → cli usage line + raw OpenAPI path/method
//   - tx_shortcuts[]: synthetic `bron tx <type>` commands with their params class
func buildFullSchemaDoc() (map[string]interface{}, error) {
	var doc map[string]interface{}
	if err := json.Unmarshal(generated.Spec, &doc); err != nil {
		return nil, fmt.Errorf("decode embedded spec: %w", err)
	}

	type cmdDoc struct {
		Command  string   `json:"command"`
		Usage    string   `json:"usage"`
		Method   string   `json:"method"`
		Path     string   `json:"path"`
		PathArgs []string `json:"path_args,omitempty"`
	}
	type txDoc struct {
		Type      string   `json:"type"`
		Usage     string   `json:"usage"`
		ParamsRef string   `json:"params_ref"`
		Params    []string `json:"params"`
		TopFields []string `json:"top_fields"`
	}

	resources := make([]string, 0, len(generated.HelpEntries))
	for r := range generated.HelpEntries {
		resources = append(resources, r)
	}
	sort.Strings(resources)

	commands := make([]cmdDoc, 0, 64)
	for _, r := range resources {
		verbs := make([]string, 0, len(generated.HelpEntries[r]))
		for v := range generated.HelpEntries[r] {
			verbs = append(verbs, v)
		}
		sort.Strings(verbs)
		for _, v := range verbs {
			e := generated.HelpEntries[r][v]
			usage := "bron " + r + " " + v
			for _, p := range e.PathArgs {
				usage += " <" + p + ">"
			}
			commands = append(commands, cmdDoc{
				Command:  r + " " + v,
				Usage:    usage,
				Method:   e.Method,
				Path:     e.Path,
				PathArgs: e.PathArgs,
			})
		}
	}

	txTypes := make([]string, 0, len(generated.TxShortcuts))
	for t := range generated.TxShortcuts {
		txTypes = append(txTypes, t)
	}
	sort.Strings(txTypes)
	tx := make([]txDoc, 0, len(txTypes))
	for _, t := range txTypes {
		s := generated.TxShortcuts[t]
		tx = append(tx, txDoc{
			Type:      t,
			Usage:     "bron tx " + t,
			ParamsRef: s.ParamsRef,
			Params:    s.Params,
			TopFields: s.TopFields,
		})
	}

	doc["x-bron-cli"] = map[string]interface{}{
		"version":      Version,
		"commands":     commands,
		"tx_shortcuts": tx,
	}
	return doc, nil
}

// appendReturnsHint prints a "Returns:" block at the bottom of help output for
// api verb commands. It lists top-level properties of the response schema with
// type and short description, expanding inline-arrays-of-refs by one level so
// list endpoints show the item shape directly. For `bron tx <type>` it also
// prints a `Body params: <Type>Params` block so the discriminator-specific
// shape is visible alongside the generic CreateTransaction wrapper.
func appendReturnsHint(cmd *cobra.Command) {
	r, v, ok := resourceVerbFor(cmd)
	if !ok {
		return
	}
	entry, ok := lookupEntry(r, v)
	if !ok {
		return
	}
	out := cmd.OutOrStdout()

	if parent := cmd.Parent(); parent != nil && parent.Name() == "tx" {
		if sc, ok := generated.TxShortcuts[cmd.Name()]; ok && sc.ParamsRef != "" {
			_, _ = fmt.Fprintf(out, "\nBody params: %s\n", sc.ParamsRef)
			printProps(out, topLevelProps(sc.ParamsRef, false), "  ")
		}
	}

	tokens := append([]string(nil), entry.EmbedTokens...)
	if r == "balances" && v == "list" {
		tokens = append(tokens, "prices")
	}
	if r == "tx" && v == "list" {
		tokens = append(tokens, "assets")
	}
	sort.Strings(tokens)
	if len(tokens) > 0 {
		_, _ = fmt.Fprintf(out, "\nEmbed tokens (`--embed %s`): %s\n",
			tokens[0], strings.Join(tokens, ", "))
	}

	if entry.ResponseRef == "" {
		_, _ = fmt.Fprintf(out, "\nFull schema: bron %s %s --schema\n", r, v)
		return
	}

	props := topLevelProps(entry.ResponseRef, true)
	_, _ = fmt.Fprintf(out, "\nReturns: %s\n", entry.ResponseRef)
	printProps(out, props, "  ")
	_, _ = fmt.Fprintf(out, "\nFull schema: bron %s %s --schema\n", r, v)
}

type schemaProp struct {
	name, typ, desc, example string
	sub                      []schemaProp // populated for `<Ref>[]` properties (one level deep)
}

func printProps(out io.Writer, props []schemaProp, indent string) {
	if len(props) == 0 {
		return
	}
	nameWidth, typeWidth := 0, 0
	for _, p := range props {
		if l := len(p.name); l > nameWidth {
			nameWidth = l
		}
		if l := len(p.typ); l > typeWidth {
			typeWidth = l
		}
	}
	for _, p := range props {
		desc := p.desc
		if p.example != "" {
			if desc == "" {
				desc = "(e.g. " + p.example + ")"
			} else {
				desc = desc + " (e.g. " + p.example + ")"
			}
		}
		if desc == "" {
			_, _ = fmt.Fprintf(out, "%s%-*s  %s\n", indent, nameWidth, p.name, p.typ)
		} else {
			_, _ = fmt.Fprintf(out, "%s%-*s  %-*s  %s\n", indent, nameWidth, p.name, typeWidth, p.typ, desc)
		}
		if len(p.sub) > 0 {
			printProps(out, p.sub, indent+"  ")
		}
	}
}

// topLevelProps returns the top-level properties of a named schema. When
// expandArrays is true, properties shaped `<Ref>[]` get their sub items'
// properties expanded one level deep (used to flesh out list endpoints
// like `transactions: Transaction[]` → show Transaction's fields).
func topLevelProps(name string, expandArrays bool) []schemaProp {
	if name == "" {
		return nil
	}
	v, err := resolveRef(name)
	if err != nil {
		return nil
	}
	m, ok := v.(map[string]interface{})
	if !ok {
		return nil
	}
	props, _ := m["properties"].(map[string]interface{})
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]schemaProp, 0, len(keys))
	for _, k := range keys {
		pm, _ := props[k].(map[string]interface{})
		sp := schemaProp{
			name:    k,
			typ:     schemaTypeLabel(pm),
			desc:    firstLine(asString(pm["description"])),
			example: firstExample(pm),
		}
		if expandArrays {
			if t, _ := pm["type"].(string); t == "array" {
				if items, ok := pm["items"].(map[string]interface{}); ok {
					if ref, _ := items["$ref"].(string); ref != "" {
						inner := strings.TrimPrefix(ref, "#/components/schemas/")
						if inner != name {
							sp.sub = topLevelProps(inner, false)
						}
					}
				}
			}
		}
		out = append(out, sp)
	}
	return out
}

// firstExample picks one example from an OpenAPI schema property. Supports both
// `example: "..."` and `examples: ["...", ...]` shapes. Returns "" when neither
// is set. DocsMacro emits string examples only — the spec never carries
// non-string example shapes today, so we don't bother with json.Marshal fallbacks.
func firstExample(pm map[string]interface{}) string {
	if v, ok := pm["example"].(string); ok && v != "" {
		return v
	}
	if arr, ok := pm["examples"].([]interface{}); ok {
		for _, x := range arr {
			if s, ok := x.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

func schemaTypeLabel(m map[string]interface{}) string {
	if m == nil {
		return ""
	}
	if ref, ok := m["$ref"].(string); ok {
		return strings.TrimPrefix(ref, "#/components/schemas/")
	}
	t, _ := m["type"].(string)
	if t == "array" {
		if items, ok := m["items"].(map[string]interface{}); ok {
			return schemaTypeLabel(items) + "[]"
		}
		return "array"
	}
	if format, _ := m["format"].(string); format != "" && t != "" {
		return t + "(" + format + ")"
	}
	if t == "" {
		if _, ok := m["properties"]; ok {
			return "object"
		}
		return ""
	}
	return t
}

func asString(v interface{}) string {
	s, _ := v.(string)
	return s
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	const max = 120
	if len([]rune(s)) > max {
		r := []rune(s)
		return string(r[:max-1]) + "…"
	}
	return s
}

// buildCommandHelpDoc emits a valid OpenAPI 3.1 fragment scoped to a single
// command. Strict spec shape so agents/tools (jq, swagger viewer, OpenAPI
// validators) can navigate it the same way they navigate the project's full
// `bron-open-api-public.json`. Bron-CLI extras (the human command, extracted
// permissions) live under the `x-bron-cli` extension namespace so the doc
// stays valid OpenAPI.
//
// The components.schemas map is the transitive closure of refs reachable from
// the operation — keeps the per-command payload small while still letting
// consumers resolve every $ref locally without a second fetch.
func buildCommandHelpDoc(resource, verb string, entry generated.HelpEntry) (map[string]interface{}, error) {
	usage := "bron " + resource + " " + verb
	for _, p := range entry.PathArgs {
		usage += " <" + p + ">"
	}

	op, err := lookupOperation(entry.Method, entry.Path)
	if err != nil {
		return nil, err
	}

	description := ""
	permissions := []string{}
	if d, ok := op["description"].(string); ok {
		description, permissions = stripPermissions(d)
	}
	if description != "" {
		op["description"] = description
	} else {
		delete(op, "description")
	}

	schemas, err := closureSchemas(op)
	if err != nil {
		return nil, err
	}

	doc := map[string]interface{}{
		"openapi": "3.1.0",
		"info": map[string]interface{}{
			"title":   usage,
			"version": Version,
		},
		"paths": map[string]interface{}{
			entry.Path: map[string]interface{}{
				strings.ToLower(entry.Method): op,
			},
		},
		"components": map[string]interface{}{
			"schemas": schemas,
		},
		"x-bron-cli": map[string]interface{}{
			"command": resource + " " + verb,
			"usage":   usage,
		},
	}
	if len(permissions) > 0 {
		doc["x-bron-cli"].(map[string]interface{})["permissions"] = permissions
	}
	return doc, nil
}

// closureSchemas walks every $ref reachable from root and returns the named
// component schemas it touches, transitively. Empty map is fine — used to
// keep the per-command openapi fragment self-contained without dumping the
// whole 250KB schema universe.
func closureSchemas(root interface{}) (map[string]interface{}, error) {
	var allSchemas struct {
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(generated.Spec, &allSchemas); err != nil {
		return nil, fmt.Errorf("decode spec components: %w", err)
	}
	out := map[string]interface{}{}
	queue := collectRefs(root)
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if _, seen := out[name]; seen {
			continue
		}
		raw, ok := allSchemas.Components.Schemas[name]
		if !ok {
			continue
		}
		var v interface{}
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("decode schema %q: %w", name, err)
		}
		out[name] = v
		queue = append(queue, collectRefs(v)...)
	}
	return out, nil
}

// collectRefs walks v and returns every component name reachable through
// `$ref: #/components/schemas/<Name>`. Order is breadth-first, duplicates
// allowed — caller dedupes via the visited map.
func collectRefs(v interface{}) []string {
	var out []string
	var walk func(node interface{})
	walk = func(node interface{}) {
		switch t := node.(type) {
		case map[string]interface{}:
			if ref, ok := t["$ref"].(string); ok {
				if name := strings.TrimPrefix(ref, "#/components/schemas/"); name != ref {
					out = append(out, name)
				}
			}
			for _, child := range t {
				walk(child)
			}
		case []interface{}:
			for _, item := range t {
				walk(item)
			}
		}
	}
	walk(v)
	return out
}

// collectDateKeysFromSpec scans the embedded OpenAPI spec for every name that
// the spec declares as `format: "date-time-millis"` (the OpenAPI translation
// of the backend's `@EpochMillis` marker) and returns them as a set.
//
// Two scans are merged into one set:
//   - response/body shapes — `components.schemas[*].properties[*]`
//   - request query/path params — `paths[*][<method>].parameters[]`
//
// One level of properties per component schema is sufficient: `@EpochMillis`
// is applied directly on Long fields in datamodel DTOs, each DTO becomes its
// own component, and timestamp names (createdAt, expiresAt, terminatedAtFrom,
// updatedSince, ...) are unique enough across the API that name-only matching
// has no false positives. If a future change nests `@EpochMillis` deeper
// (e.g. inside an inline object property), expand this scan.
//
// The result drives both ends of the date pipeline:
//   - request-side coercion (qparam.IsDateParam → MaybeDate, used by
//     compactQuery on every CLI list-call and by mcp.go on body composition);
//   - response-side humanization (output.SetDateKeys → ISO-8601 rendering).
func collectDateKeysFromSpec() map[string]bool {
	keys := map[string]bool{}
	var spec struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]struct {
					Format string `json:"format"`
					Items  struct {
						Format string `json:"format"`
					} `json:"items"`
				} `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
		Paths map[string]map[string]struct {
			Parameters []struct {
				Name   string `json:"name"`
				Schema struct {
					Format string `json:"format"`
					Items  struct {
						Format string `json:"format"`
					} `json:"items"`
				} `json:"schema"`
			} `json:"parameters"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(generated.Spec, &spec); err != nil {
		return keys
	}
	for _, schema := range spec.Components.Schemas {
		for name, prop := range schema.Properties {
			if prop.Format == "date-time-millis" || prop.Items.Format == "date-time-millis" {
				keys[name] = true
			}
		}
	}
	for _, methods := range spec.Paths {
		for _, op := range methods {
			for _, p := range op.Parameters {
				if p.Schema.Format == "date-time-millis" || p.Schema.Items.Format == "date-time-millis" {
					keys[p.Name] = true
				}
			}
		}
	}
	return keys
}

// lookupOperation pulls the operation node for a (method, path) pair from the
// embedded spec.
func lookupOperation(method, path string) (map[string]interface{}, error) {
	var spec struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(generated.Spec, &spec); err != nil {
		return nil, fmt.Errorf("parse spec: %w", err)
	}
	pathItem, ok := spec.Paths[path]
	if !ok {
		return nil, fmt.Errorf("path %q not found in spec", path)
	}
	raw, ok := pathItem[strings.ToLower(method)]
	if !ok {
		return nil, fmt.Errorf("operation %s %s not found in spec", method, path)
	}
	var op map[string]interface{}
	if err := json.Unmarshal(raw, &op); err != nil {
		return nil, fmt.Errorf("decode operation %s %s: %w", method, path, err)
	}
	return op, nil
}

// stripPermissions extracts trailing "<sup>API Key permissions: A, B</sup>"
// from a description and returns (cleanedDescription, permissionsList).
func stripPermissions(desc string) (string, []string) {
	const tagOpen = "<sup>API Key permissions: "
	const tagClose = "</sup>"
	i := strings.Index(desc, tagOpen)
	if i < 0 {
		return strings.TrimSpace(desc), nil
	}
	j := strings.Index(desc[i:], tagClose)
	if j < 0 {
		return strings.TrimSpace(desc), nil
	}
	perms := desc[i+len(tagOpen) : i+j]
	clean := strings.TrimSpace(desc[:i])
	parts := strings.Split(perms, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return clean, out
}

func resolveRef(name string) (interface{}, error) {
	if name == "" {
		return nil, nil
	}
	var spec struct {
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(generated.Spec, &spec); err != nil {
		return nil, fmt.Errorf("parse spec: %w", err)
	}
	raw, ok := spec.Components.Schemas[name]
	if !ok {
		return nil, fmt.Errorf("schema %q not found in spec", name)
	}
	var v interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("decode schema %q: %w", name, err)
	}
	return v, nil
}
