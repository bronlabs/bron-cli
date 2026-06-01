package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bronlabs/bron-cli/generated"
)

// bron_help is the single discovery tool. Its own description is deliberately
// tiny; one call teaches the agent the data model, a tool's response shape,
// and ready-to-use jq recipes. This keeps per-tool descriptions short — the
// expensive guidance lives behind one on-demand call instead of being paid on
// every session's tool list.
//
// Three modes, by argument:
//   - no args                → the overview: data model + how `fields`/`jq`
//     work + the topic/tool index.
//   - tool:"bron_tx_list"    → that tool's response shape, resolved from the
//     embedded OpenAPI spec, plus field-level notes.
//   - topic:"tx-aggregation" → a jq cookbook entry.

const helpToolName = "bron_help"

const helpOverview = "# Bron MCP — agent guide\n\n" +
	"Call `bron_help` again with a `tool` or `topic` argument for detail.\n\n" +
	"## Data model — read this before computing money\n\n" +
	"A transaction carries two very different sources of amounts:\n\n" +
	"- `params.*` — the **intent / quote**: what was requested (a withdrawal's\n" +
	"  requested amount, a swap's quoted rate). NOT what settled.\n" +
	"- `_embedded.events[]` — the **settlement facts**: the actual on-chain\n" +
	"  movements, each with `amount`, `assetId`, `symbol`, `networkId`,\n" +
	"  `eventType` (`in`/`out`) and `usdAmount`.\n\n" +
	"**For any financial total — volume, net flow, P&L — aggregate\n" +
	"`_embedded.events[]`, never `params.amount`.** Summing `params.amount`\n" +
	"gives a plausible but wrong number, especially for swaps, bridges,\n" +
	"intents, fiat and fee-bearing transfers. Events are omitted by default —\n" +
	"pass `includeEvents: true` on `bron_tx_list` / `bron_tx_get` to get them.\n\n" +
	"## Shaping responses (save your context)\n\n" +
	"Both are optional and compose; `jq` runs after `fields`:\n\n" +
	"- `fields` — comma-separated dot-paths to keep, e.g.\n" +
	"  `transactionId,status,_embedded.events.usdAmount`. A path crossing an\n" +
	"  array applies to every element. List each leaf (no `{a,b}` groups).\n" +
	"- `jq` — a gojq program to filter/reshape/aggregate server-side, e.g.\n" +
	"  `.transactions | length`. Sandboxed: no env, no stdin, no imports;\n" +
	"  time- and size-bounded. A bad program returns an error you can fix.\n\n" +
	"## Topics (call with topic:\"…\")\n\n" +
	"- `tx-aggregation` — volume, net-by-asset, count-by-status jq recipes.\n" +
	"- `events-vs-params` — why settlement ≠ intent, with examples.\n" +
	"- `shaping` — more fields/jq patterns.\n\n" +
	"## Schema (call with tool:\"…\")\n\n" +
	"Pass any read tool name (e.g. `tool:\"bron_tx_list\"`) to see its response\n" +
	"shape resolved from the OpenAPI spec — field names, types and notes — so\n" +
	"you can write `fields`/`jq` without guessing."

var helpTopics = map[string]string{
	"tx-aggregation": "# jq recipes — transaction aggregation\n\n" +
		"All assume `bron_tx_list` with `includeEvents: true`. Amounts come from\n" +
		"`_embedded.events[]`, the settlement facts.\n\n" +
		"**Total USD volume** (sum of every event's usd value):\n" +
		"```\n[.transactions[]._embedded.events[]? | (.usdAmount // \"0\" | tonumber)] | add\n```\n\n" +
		"**Net USD flow** (in positive, out negative):\n" +
		"```\n[.transactions[]._embedded.events[]? | (.usdAmount // \"0\" | tonumber) * (if .eventType==\"in\" then 1 else -1 end)] | add\n```\n\n" +
		"**Net amount per asset:**\n" +
		"```\n[.transactions[]._embedded.events[]? | {k:(.assetId+\"|\"+.symbol), amt:((.amount // \"0\" | tonumber)*(if .eventType==\"in\" then 1 else -1 end))}]\n| group_by(.k) | map({asset:.[0].k, net:(map(.amt)|add)})\n```\n\n" +
		"**Count by status:**\n" +
		"```\n.transactions | group_by(.status) | map({status:.[0].status, n:length})\n```\n",
	"events-vs-params": "# Settlement (events) vs intent (params)\n\n" +
		"`params.amount` is what the user *asked for*; `_embedded.events[].amount`\n" +
		"is what *actually moved on-chain*. They diverge whenever a transaction is\n" +
		"more than a 1:1 transfer:\n\n" +
		"- **swap / bridge / intent** — `params` has the quote; `events` has the\n" +
		"  real in-leg and out-leg, often different assets.\n" +
		"- **fee-bearing transfers** — `events` split principal and network fee.\n" +
		"- **fiat** — `params` is the order; `events` are the realised legs.\n\n" +
		"Rule: report `_embedded.events[]` for anything financial; use `params`\n" +
		"only to show what was requested. Always pass `includeEvents: true`.\n",
	"shaping": "# Shaping patterns\n\n" +
		"`fields` keeps just the leaves you name; `jq` runs after, for anything\n" +
		"beyond projection.\n\n" +
		"Trim a list to a few columns:\n" +
		"```\nfields: \"transactionId,status,createdAt,_embedded.events.usdAmount\"\n```\n\n" +
		"Filter rows then project:\n" +
		"```\njq: \"[.transactions[] | select(.status==\\\"completed\\\") | {id:.transactionId, ts:.createdAt}]\"\n```\n\n" +
		"Just a count or a single number:\n" +
		"```\njq: \".transactions | length\"\n```\n\n" +
		"If a `fields` path yields nothing, the field may not exist OR your path\n" +
		"is wrong — call `bron_help` with `tool:\"<name>\"` to confirm the shape.\n",
}

func helpToolSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"topic": {
				Type:        "string",
				Description: "Guide topic: `tx-aggregation`, `events-vs-params`, `shaping`.",
			},
			"tool": {
				Type:        "string",
				Description: "A read tool name (e.g. `bron_tx_list`) to see its response shape.",
			},
		},
		AdditionalProperties: &jsonschema.Schema{},
	}
}

// registerHelpTool wires bron_help. Always registered (read-only safe) so the
// discovery path exists in every mode, including `--read-only`.
func registerHelpTool(server *mcp.Server) {
	tool := &mcp.Tool{
		Name: helpToolName,
		Description: "Discovery: the Bron data model, a tool's response shape, and jq recipes. " +
			"Call once with no args for the overview, then `topic`/`tool` for detail. " +
			"Start here before computing financial totals. Read-only.",
		InputSchema: helpToolSchema(),
	}
	mcp.AddTool(server, tool, func(_ context.Context, _ *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, any, error) {
		return helpResult(in), nil, nil
	})
}

func helpResult(in map[string]any) *mcp.CallToolResult {
	text := helpOverview

	if topic := strings.TrimSpace(stringArg(in, "topic")); topic != "" {
		if body, ok := helpTopics[topic]; ok {
			text = body
		} else {
			text = fmt.Sprintf("Unknown topic %q. Available: %s.\n\n%s",
				topic, strings.Join(sortedKeys(helpTopics), ", "), helpOverview)
		}
	} else if tool := strings.TrimSpace(stringArg(in, "tool")); tool != "" {
		text = helpForTool(tool)
	}

	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

func stringArg(in map[string]any, key string) string {
	if v, ok := in[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// helpForTool resolves a tool name to its endpoint and renders the response
// schema (from the embedded spec) as a compact field listing.
func helpForTool(tool string) string {
	e, ok := findHelpEntry(tool)
	if !ok {
		return fmt.Sprintf("Unknown tool %q. Pass an endpoint tool name like `bron_tx_list`.", tool)
	}
	if e.ResponseRef == "" {
		return fmt.Sprintf("`%s` — no response schema in the spec.", tool)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "# %s\n\n## Response shape\n\n", tool)
	fields := resolveSchemaFields(e.ResponseRef)
	if len(fields) == 0 {
		fmt.Fprintf(&sb, "(could not resolve `%s`)\n", e.ResponseRef)
	} else {
		for _, f := range fields {
			sb.WriteString(f)
			sb.WriteString("\n")
		}
	}
	if strings.HasPrefix(tool, "bron_tx_") {
		sb.WriteString("\nFor financial totals pass `includeEvents: true` and aggregate " +
			"`_embedded.events[]` (see `topic:\"tx-aggregation\"`), not `params.amount`.\n")
	}
	return sb.String()
}

func findHelpEntry(tool string) (generated.HelpEntry, bool) {
	for r, verbs := range generated.HelpEntries {
		for v, e := range verbs {
			if toolName(r, v) == tool {
				return e, true
			}
		}
	}
	return generated.HelpEntry{}, false
}

// resolveSchemaFields walks the embedded OpenAPI spec from a component schema
// name and produces a flat `path: type — description` listing, expanding $refs
// and array items up to a bounded depth so deeply nested or recursive schemas
// don't explode the output.
func resolveSchemaFields(ref string) []string {
	var spec map[string]any
	if err := json.Unmarshal(generated.Spec, &spec); err != nil {
		return nil
	}
	components, _ := spec["components"].(map[string]any)
	if components == nil {
		return nil
	}
	schemas, _ := components["schemas"].(map[string]any)
	if schemas == nil {
		return nil
	}

	var out []string
	seen := map[string]bool{}
	var walk func(node any, path string, depth int)
	walk = func(node any, path string, depth int) {
		m, ok := node.(map[string]any)
		if !ok || depth > 6 {
			return
		}
		if r, ok := m["$ref"].(string); ok {
			name := r[strings.LastIndex(r, "/")+1:]
			if seen[name] {
				return
			}
			seen[name] = true
			walk(schemas[name], path, depth)
			return
		}
		switch m["type"] {
		case "object":
			props, _ := m["properties"].(map[string]any)
			for _, k := range sortedAnyKeys(props) {
				child, ok := props[k].(map[string]any)
				if !ok {
					continue
				}
				childPath := wireName(k)
				if path != "" {
					childPath = path + "." + wireName(k)
				}
				if leaf, ok := scalarLeaf(child); ok {
					out = append(out, fmt.Sprintf("- `%s` (%s)%s", childPath, leaf, descSuffix(child)))
				} else {
					walk(child, childPath, depth+1)
				}
			}
		case "array":
			if items, ok := m["items"].(map[string]any); ok {
				walk(items, path+"[]", depth+1)
			}
		}
	}
	walk(map[string]any{"$ref": "#/components/schemas/" + ref}, "", 0)
	return out
}

// scalarLeaf returns a type label when node is a scalar (no further nesting
// worth expanding), so the listing stops at useful leaves.
// wireName maps a spec property name to the name that appears on the wire.
// The OpenAPI schema calls the HATEOAS extras bag `embedded`, but it
// serializes as `_embedded` (`@JsonProperty("_embedded")` in the datamodel) —
// so a `fields`/`jq` path must use the underscore. Without this fix the schema
// listing would hand the agent `embedded.events`, the exact wrong path this
// tool exists to prevent.
func wireName(prop string) string {
	if prop == "embedded" {
		return "_embedded"
	}
	return prop
}

func scalarLeaf(node map[string]any) (string, bool) {
	if _, ok := node["$ref"]; ok {
		return "", false
	}
	t, _ := node["type"].(string)
	switch t {
	case "string", "boolean", "integer", "number":
		return t, true
	case "", "object", "array":
		return "", false
	default:
		return t, true
	}
}

func descSuffix(node map[string]any) string {
	if d, ok := node["description"].(string); ok && d != "" {
		d = strings.TrimSpace(strings.SplitN(d, "\n", 2)[0])
		if len(d) > 80 {
			d = d[:79] + "…"
		}
		return " — " + d
	}
	return ""
}

func sortedAnyKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
