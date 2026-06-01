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
	fields := resolveSchemaFields("Transactions")
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
