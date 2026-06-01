package main

import (
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestWireNameEmbedded(t *testing.T) {
	if got := wireName("embedded"); got != "_embedded" {
		t.Fatalf("wireName(embedded) = %q, want _embedded", got)
	}
	if got := wireName("params"); got != "params" {
		t.Fatalf("wireName(params) = %q, want params", got)
	}
}

func TestResolveSchemaFieldsUsesWireEmbedded(t *testing.T) {
	fields := resolveSchemaFieldsAt("Transactions", "")
	if len(fields) == 0 {
		t.Fatal("no fields resolved for Transactions")
	}
	joined := strings.Join(fields, "\n")
	if strings.Contains(joined, "`embedded.") {
		t.Fatalf("schema listing leaked the spec name `embedded.` — must be `_embedded.`:\n%s", joined)
	}
}

func TestHelpForToolUnknown(t *testing.T) {
	if out := helpForTool("bron_not_a_tool"); !strings.Contains(out, "Unknown tool") {
		t.Fatalf("expected unknown-tool message, got: %s", out)
	}
}

func TestHelpForTxShortcutRendersParams(t *testing.T) {
	out := helpForTool("bron_tx_withdrawal")
	if !strings.Contains(out, "params.") {
		t.Fatalf("expected per-type params in shortcut help, got:\n%s", out)
	}
	if !strings.Contains(out, "externalId") {
		t.Fatalf("expected top-level fields in shortcut help, got:\n%s", out)
	}
	if !strings.Contains(out, "bron_tx_dry_run") {
		t.Fatalf("expected dry-run pointer in shortcut help, got:\n%s", out)
	}
}

func TestParamTypeMapTypesBooleans(t *testing.T) {
	types := paramTypeMap("AllowanceParams")
	if got := types["unlimited"]; got != "boolean" {
		t.Fatalf("AllowanceParams.unlimited type = %q, want boolean (got map: %v)", got, types)
	}
}

func TestHelpResultTopicFallback(t *testing.T) {
	res := helpResult(map[string]any{"topic": "does-not-exist"})
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected *mcp.TextContent, got %T", res.Content[0])
	}
	if !strings.Contains(tc.Text, "Available:") {
		t.Fatalf("expected available-topics list, got: %s", tc.Text)
	}
}
