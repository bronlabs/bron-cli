package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/bronlabs/bron-cli/generated"
)

// TestExamplesAreInSync validates every `bron <resource> <verb> ...` snippet in
// rootExample, README.md, and topics.go against the embedded OpenAPI spec.
//
// Catches drift when the API changes (renamed flag, removed verb, new
// transactionType collision, …) but the docs were written by hand and not
// updated alongside. Doesn't validate value semantics — just shape.
//
// What's checked per snippet:
//   - <resource>+<verb> exists in generated.HelpEntries (or it's a known
//     system command: help, auth, config, completion, version)
//   - For `bron tx <type>`: <type> is a real transactionType shortcut
//   - Every --<flag> resolves to one of: a query param on the endpoint, a body
//     leaf, a tx-shortcut Params/TopFields field, or a known global flag
//
// What's NOT checked: positional argument values, flag VALUES (only names),
// shell-meta in the snippet, command exit codes.
func TestExamplesAreInSync(t *testing.T) {
	sources := []sourceFile{
		readSource(t, "main.go"),
		readSource(t, "topics.go"),
		readREADME(t),
	}

	bodyLeaves := buildBodyLeaves(t)
	globalFlags := globalFlagSet()

	var found bool
	for _, src := range sources {
		for _, snip := range extractBronSnippets(src) {
			found = true
			validateSnippet(t, snip, bodyLeaves, globalFlags)
		}
	}
	if !found {
		t.Fatal("found 0 `bron ...` snippets — extractor regression?")
	}
}

// --- snippet model ---

type sourceFile struct {
	path string
	body string
}

type snippet struct {
	source sourceFile
	line   int
	raw    string
	tokens []string
}

// --- extraction ---

// snippetPattern: line-anchored `bron <stuff>` to end of line. Source is
// preprocessed to glue shell line-continuations (`\\\n`) into single lines
// before this regex runs — see joinContinuations below.
var snippetPattern = regexp.MustCompile(`(?m)^[\s>#*]*bron\b[^\n]*`)

// joinContinuations replaces `\<newline>` pairs (possibly with trailing
// whitespace before the backslash) with a single space, so multi-line `bron
// tx withdrawal \` examples land on one logical line for the regex.
var continuationPattern = regexp.MustCompile(`\\\n[\t ]*`)

func joinContinuations(s string) string {
	return continuationPattern.ReplaceAllString(s, " ")
}

func extractBronSnippets(src sourceFile) []snippet {
	body := joinContinuations(src.body)
	out := []snippet{}
	for _, m := range snippetPattern.FindAllStringIndex(body, -1) {
		joined := body[m[0]:m[1]]
		raw := joined
		// strip leading prose markers (`# `, `> `, list bullets)
		joined = strings.TrimLeft(joined, " \t#>*-")
		// drop everything after a comment marker that follows whitespace
		if i := strings.Index(joined, " #"); i >= 0 {
			joined = joined[:i]
		}
		// drop pipe / redirect tail (e.g. `| jq ...`, `> file`)
		for _, sep := range []string{" |", " >", " 2>"} {
			if i := strings.Index(joined, sep); i >= 0 {
				joined = joined[:i]
			}
		}
		toks := tokenizeShell(strings.TrimSpace(joined))
		if len(toks) < 2 || toks[0] != "bron" {
			continue
		}
		// Heuristic guard: a real command's second token is a resource name
		// (kebab-case lowercase) or system command. Skip:
		//   prose ("bron CLI"), placeholders ("bron <resource> ..."),
		//   illustrative flag-first lines ("bron --output yaml ...").
		if !looksLikeRealResource(toks[1]) {
			continue
		}
		// line of the match start
		line := 1 + strings.Count(src.body[:m[0]], "\n")
		out = append(out, snippet{source: src, line: line, raw: raw, tokens: toks})
	}
	return out
}

// tokenizeShell handles only what example snippets actually use: whitespace
// separation plus single/double-quoted runs. Backslash escapes are NOT
// interpreted (none of the snippets need that).
func tokenizeShell(s string) []string {
	var out []string
	var b strings.Builder
	quote := byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == quote {
				quote = 0
				continue
			}
			b.WriteByte(c)
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
		case ' ', '\t':
			if b.Len() > 0 {
				out = append(out, b.String())
				b.Reset()
			}
		default:
			b.WriteByte(c)
		}
	}
	if b.Len() > 0 {
		out = append(out, b.String())
	}
	return out
}

// --- validation ---

// handwrittenTxVerbs lists `bron tx <verb>` commands that are NOT in
// generated.HelpEntries (because they're hand-written in cmd/bron/, not
// emitted by cligen). The map value is the set of valid flag names.
var handwrittenTxVerbs = map[string]map[string]bool{
	"subscribe": {
		"accountId":           true,
		"transactionStatuses": true,
		"transactionTypes":    true,
		"no-history":          true,
		"correlation-id":      true,
	},
}

func validateHandwrittenTxVerb(t *testing.T, loc string, rest []string, verb string, validFlags, globals map[string]bool) {
	t.Helper()
	for _, tok := range rest {
		if !strings.HasPrefix(tok, "--") {
			continue
		}
		flag, _ := splitFlagValue(tok)
		if globals[flag] || systemFlag(flag) || validFlags[flag] {
			continue
		}
		valid := make([]string, 0, len(validFlags))
		for f := range validFlags {
			valid = append(valid, f)
		}
		sort.Strings(valid)
		t.Errorf("%s: unknown flag --%s on `bron tx %s`; valid: %s", loc, flag, verb, strings.Join(valid, ", "))
	}
}

// systemCmds that don't go through HelpEntries.
var systemCmds = map[string]bool{
	"help":       true,
	"auth":       true,
	"config":     true,
	"completion": true,
	"mcp":        true,
	"version":    true,
	"--version":  true,
	"--help":     true,
	"-h":         true,
}

func validateSnippet(t *testing.T, s snippet, bodyLeaves bodyLeavesByRef, globals map[string]bool) {
	t.Helper()
	loc := fmt.Sprintf("%s:%d", s.source.path, s.line)

	resource := s.tokens[1]
	if systemCmds[resource] {
		return // system command — out of scope
	}

	verb := ""
	if len(s.tokens) >= 3 {
		verb = s.tokens[2]
	}

	// Special-case: `bron tx <type>` is the create-shortcut subtree.
	if resource == "tx" {
		// Verb may be a real tx verb (list/get/...) or a transactionType.
		if _, ok := generated.TxShortcuts[verb]; ok {
			validateTxShortcut(t, loc, s.tokens, verb, globals)
			return
		}
		// Hand-written tx children (cmd/bron/*.go, not from cligen) — validate
		// flags against a curated allowlist instead of the generated entries.
		if validators, ok := handwrittenTxVerbs[verb]; ok {
			validateHandwrittenTxVerb(t, loc, s.tokens[3:], verb, validators, globals)
			return
		}
		// fall through: validate as bron tx <verb>
	}

	verbs, ok := generated.HelpEntries[resource]
	if !ok {
		t.Errorf("%s: unknown resource %q (`bron %s ...`); known: %s", loc, resource, resource, strings.Join(sortedKeysHelp(generated.HelpEntries), ", "))
		return
	}
	entry, ok := verbs[verb]
	if !ok {
		t.Errorf("%s: unknown verb %q on `bron %s ...`; known: %s", loc, verb, resource, strings.Join(sortedKeysVerb(verbs), ", "))
		return
	}

	validateFlags(t, loc, s.tokens[3:], entry, bodyLeaves, globals)
}

func validateTxShortcut(t *testing.T, loc string, tokens []string, txType string, globals map[string]bool) {
	t.Helper()
	sc := generated.TxShortcuts[txType]
	rest := tokens[3:]
	for _, tok := range rest {
		if !strings.HasPrefix(tok, "--") {
			continue
		}
		flag, _ := splitFlagValue(tok)
		if globals[flag] || systemFlag(flag) {
			continue
		}
		// CreateTransaction top-level field?
		if contains(sc.TopFields, flag) {
			continue
		}
		// params.<field>?
		if strings.HasPrefix(flag, "params.") {
			leaf := strings.TrimPrefix(flag, "params.")
			if contains(sc.Params, leaf) {
				continue
			}
			t.Errorf("%s: unknown body flag --%s on `bron tx %s`; valid params: %s",
				loc, flag, txType, strings.Join(sc.Params, ", "))
			continue
		}
		// "transactionType" set inside a manual `--transactionType` (e.g. as part
		// of `bron tx create --transactionType <type>`) is also valid via the verbs
		// path — but that goes through validateFlags, not this branch.
		t.Errorf("%s: unknown flag --%s on `bron tx %s`; expected --params.* or one of %s",
			loc, flag, txType, strings.Join(sc.TopFields, ", "))
	}
}

func validateFlags(t *testing.T, loc string, rest []string, entry generated.HelpEntry, bodyLeaves bodyLeavesByRef, globals map[string]bool) {
	t.Helper()
	queries := map[string]bool{}
	for _, q := range entry.QueryParams {
		queries[q.Name] = true
	}
	leaves := bodyLeaves[entry.BodyRef]
	leafSet := map[string]bool{}
	for _, l := range leaves {
		leafSet[l] = true
	}
	for _, tok := range rest {
		if !strings.HasPrefix(tok, "--") {
			continue
		}
		flag, _ := splitFlagValue(tok)
		switch {
		case globals[flag], systemFlag(flag):
		case queries[flag]:
		case leafSet[flag]:
		case strings.HasPrefix(flag, "params."):
			// A nested params.* on the generic `tx create` route — accept; type
			// not picked yet.
		default:
			suggestions := []string{}
			if entry.BodyRef != "" {
				suggestions = append(suggestions, "body: "+truncList(leaves, 6))
			}
			if len(entry.QueryParams) > 0 {
				suggestions = append(suggestions, "query: "+truncList(queryNames(entry.QueryParams), 6))
			}
			t.Errorf("%s: unknown flag --%s on `bron <r> <v>` (resource=%s body=%s); %s",
				loc, flag, entry.Path, entry.BodyRef, strings.Join(suggestions, "; "))
		}
	}
}

// --- body leaves from the embedded spec ---

type bodyLeavesByRef map[string][]string

// buildBodyLeaves walks every component schema and returns a map from
// component name → flat list of dot-path leaves. Used to validate body flags
// against the actual schema shape (not just the top-level properties).
func buildBodyLeaves(t *testing.T) bodyLeavesByRef {
	t.Helper()
	var spec struct {
		Components struct {
			Schemas map[string]rawSchemaForLeaves `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(generated.Spec, &spec); err != nil {
		t.Fatalf("parse embedded spec: %v", err)
	}
	out := bodyLeavesByRef{}
	for name := range spec.Components.Schemas {
		visited := map[string]bool{}
		out[name] = leavesOf(name, "", spec.Components.Schemas, visited)
	}
	return out
}

type rawSchemaForLeaves struct {
	Type       string                        `json:"type"`
	Ref        string                        `json:"$ref"`
	Properties map[string]rawSchemaForLeaves `json:"properties"`
	Items      *rawSchemaForLeaves           `json:"items"`
	OneOf      []rawSchemaForLeaves          `json:"oneOf"`
	AllOf      []rawSchemaForLeaves          `json:"allOf"`
	AnyOf      []rawSchemaForLeaves          `json:"anyOf"`
	Enum       []interface{}                 `json:"enum"`
	Format     string                        `json:"format"`
}

func leavesOf(name, prefix string, reg map[string]rawSchemaForLeaves, visited map[string]bool) []string {
	if visited[name] {
		return nil
	}
	visited[name] = true
	defer delete(visited, name)
	s := reg[name]
	var out []string
	for k, prop := range s.Properties {
		dot := k
		if prefix != "" {
			dot = prefix + "." + k
		}
		switch propKindForLeaves(prop, reg) {
		case "scalar", "array":
			out = append(out, dot)
		case "object":
			out = append(out, walkInline(prop, dot, reg, visited)...)
		}
	}
	for _, branch := range s.AllOf {
		out = append(out, walkInline(branch, prefix, reg, visited)...)
	}
	return out
}

func walkInline(s rawSchemaForLeaves, prefix string, reg map[string]rawSchemaForLeaves, visited map[string]bool) []string {
	if s.Ref != "" {
		return leavesOf(refName(s.Ref), prefix, reg, visited)
	}
	var out []string
	for k, prop := range s.Properties {
		dot := k
		if prefix != "" {
			dot = prefix + "." + k
		}
		switch propKindForLeaves(prop, reg) {
		case "scalar", "array":
			out = append(out, dot)
		case "object":
			out = append(out, walkInline(prop, dot, reg, visited)...)
		}
	}
	return out
}

func propKindForLeaves(s rawSchemaForLeaves, reg map[string]rawSchemaForLeaves) string {
	if s.Ref != "" {
		s = reg[refName(s.Ref)]
	}
	if len(s.Enum) > 0 || s.Format == "date-time-millis" || s.Type == "string" || s.Type == "boolean" || s.Type == "integer" || s.Type == "number" {
		return "scalar"
	}
	if s.Type == "array" {
		return "array"
	}
	if len(s.Properties) > 0 || s.Type == "object" {
		return "object"
	}
	return "scalar"
}

func refName(ref string) string {
	return strings.TrimPrefix(ref, "#/components/schemas/")
}

// --- helpers ---

// looksLikeRealResource returns true if the token is a kebab-case ASCII
// identifier — a real resource name (or system cmd / `tx`). Filters out:
//   - prose like "CLI", "Bron", "API" (capital letters)
//   - placeholders like "<resource>" or "<id>"
//   - global flags ("--output yaml" with no resource yet — illustrative, skip)
var realResourcePattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

func looksLikeRealResource(s string) bool { return realResourcePattern.MatchString(s) }

func splitFlagValue(tok string) (string, string) {
	t := strings.TrimPrefix(tok, "--")
	if i := strings.Index(t, "="); i >= 0 {
		return t[:i], t[i+1:]
	}
	return t, ""
}

func systemFlag(f string) bool {
	return f == "help" || f == "version" || f == "h"
}

// globalFlagSet returns the set of root-level persistent flags exposed by main.go.
// Kept manually here — it's a short list and changes infrequently. If main.go
// gains new globals, update here.
func globalFlagSet() map[string]bool {
	return map[string]bool{
		"profile":   true,
		"workspace": true,
		"base-url":  true,
		"key-file":  true,
		"proxy":     true,
		"output":    true,
		"query":     true,
		"columns":   true,
		"cell-max":  true,
		"embed":     true,
		"schema":    true,
		"file":      true,
		"json":      true,
	}
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func queryNames(ps []generated.HelpQueryParam) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Name
	}
	return out
}

func truncList(xs []string, n int) string {
	if len(xs) <= n {
		return strings.Join(xs, ", ")
	}
	return strings.Join(xs[:n], ", ") + ", ..."
}

func sortedKeysHelp(m map[string]map[string]generated.HelpEntry) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedKeysVerb(m map[string]generated.HelpEntry) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// --- source readers ---

func readSource(t *testing.T, name string) sourceFile {
	t.Helper()
	path := filepath.Join("./", name)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return sourceFile{path: name, body: string(b)}
}

func readREADME(t *testing.T) sourceFile {
	t.Helper()
	// cmd/bron tests run from the cmd/bron directory; README is at repo root.
	candidates := []string{"../../README.md", "../README.md", "README.md"}
	for _, p := range candidates {
		if b, err := os.ReadFile(p); err == nil {
			return sourceFile{path: "README.md", body: string(b)}
		}
	}
	t.Fatalf("README.md not found in %v", candidates)
	return sourceFile{}
}
