package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	sdk "github.com/bronlabs/bron-sdk-go/sdk"
	"github.com/bronlabs/bron-sdk-go/sdk/types"

	"github.com/bronlabs/bron-cli/internal/client"
	"github.com/bronlabs/bron-cli/internal/output"
)

// Client-side composites — hand-written MCP tools that orchestrate REST + WS
// to deliver capabilities not present in the OpenAPI spec as a single
// endpoint. Kept deliberately small. The vast majority of tools are
// auto-registered from the spec in `mcp.go`.
//
// Add a new composite here only when:
//   1. The capability genuinely composes multiple endpoints / a streaming
//      channel and a polling fallback (e.g. `wait_for_state`).
//   2. It cannot be expressed as a single OpenAPI operation. If it can be —
//      add it to the public-api spec, regen the SDK, and the spec-driven
//      registration in `mcp.go` picks it up automatically.

const (
	waitForStateDefaultSec = 30
	waitForStateMaxSec     = 60
)

func registerClientComposites(server *mcp.Server, _ *client.Client, sdkClient *sdk.BronClient) {
	registerTxWaitForState(server, sdkClient)
}

// --- bron_tx_wait_for_state --------------------------------------------------

type waitForStateInput struct {
	TransactionID  string   `json:"transactionId"`
	ExpectedStates []string `json:"expectedStates"`
	TimeoutSec     int      `json:"timeoutSec,omitempty"`
}

// registerTxWaitForState wires the long-poll wait-for-state tool. The handler
// opens a WebSocket subscription scoped to the target transaction (server-side
// filter `transactionIds: [id]`, no historical replay limit). The first frame
// is the snapshot at subscribe time; subsequent frames are live updates. The
// loop returns on the first frame whose status matches `expectedStates`. On
// timeout it does a final REST GET to capture the up-to-the-second status and
// returns a continuation hint so the agent can re-call.
//
// Why this shape and not `resources/subscribe`: server-initiated MCP
// notifications work in the spec but aren't surfaced to the LLM session by
// any shipping client today (Claude Desktop, Cursor, Cline, ChatGPT). A
// long-poll `tools/call` blocks the client's request budget until match-or-
// timeout, which every MCP client already supports.
func registerTxWaitForState(server *mcp.Server, sdkClient *sdk.BronClient) {
	tool := &mcp.Tool{
		Name: "bron_tx_wait_for_state",
		Description: "Wait until a transaction enters one of `expectedStates`, or until `timeoutSec` elapses. " +
			"Subscribes via WebSocket scoped to this transactionId; returns immediately on first match. " +
			"On timeout, returns the current status and a hint to call again. " +
			"Universal long-poll pattern — works in every MCP client.",
		InputSchema: &jsonschema.Schema{
			Type: "object",
			Properties: map[string]*jsonschema.Schema{
				"transactionId": {
					Type:        "string",
					Description: "Transaction id to watch.",
				},
				"expectedStates": {
					Type:        "array",
					Items:       &jsonschema.Schema{Type: "string"},
					MinItems:    ptr(1),
					Description: "Statuses to wake on. Common terminal set: completed, canceled, expired, error, failed-on-blockchain, removed-from-blockchain. Match is exact (kebab-case wire values).",
				},
				"timeoutSec": {
					Type:        "integer",
					Minimum:     ptr(1.0),
					Maximum:     ptr(float64(waitForStateMaxSec)),
					Description: fmt.Sprintf("Max seconds to wait. Default %d, max %d. On timeout, returns current state + retryHint instead of erroring — call the tool again to keep waiting.", waitForStateDefaultSec, waitForStateMaxSec),
				},
			},
			Required:             []string{"transactionId", "expectedStates"},
			AdditionalProperties: &jsonschema.Schema{},
		},
	}

	mcp.AddTool(server, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in waitForStateInput) (*mcp.CallToolResult, any, error) {
		result, err := runWaitForState(ctx, sdkClient, in)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return nil, wrapUntrustedFields(output.HumanizeDates(result)), nil
	})
}

func runWaitForState(ctx context.Context, sdkClient *sdk.BronClient, in waitForStateInput) (any, error) {
	if in.TransactionID == "" {
		return nil, fmt.Errorf("transactionId is required")
	}
	if len(in.ExpectedStates) == 0 {
		return nil, fmt.Errorf("expectedStates must contain at least one status")
	}

	timeoutSec := in.TimeoutSec
	if timeoutSec <= 0 {
		timeoutSec = waitForStateDefaultSec
	}
	if timeoutSec > waitForStateMaxSec {
		timeoutSec = waitForStateMaxSec
	}

	expected := make(map[string]bool, len(in.ExpectedStates))
	for _, s := range in.ExpectedStates {
		expected[s] = true
	}

	waitCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	// Subscribe with `transactionIds: [id]` — the server filters server-side and
	// the first frame is the snapshot at subscribe time, eliminating the
	// classic "GET-then-subscribe" race where a transition between the two
	// calls would be lost.
	filter := map[string]interface{}{
		"transactionIds": []string{in.TransactionID},
	}

	stream, err := sdkClient.Transactions.SubscribeWithFilter(waitCtx, filter)
	if err != nil {
		return nil, fmt.Errorf("subscribe: %w", err)
	}
	defer func() { _ = stream.Close() }()

	updates := stream.Updates()
	for {
		select {
		case <-waitCtx.Done():
			return waitForStateTimeout(ctx, sdkClient, in.TransactionID, expected)
		case batch, ok := <-updates:
			if !ok {
				if err := stream.Err(); err != nil && waitCtx.Err() == nil {
					return nil, fmt.Errorf("subscribe stream closed: %w", err)
				}
				return waitForStateTimeout(ctx, sdkClient, in.TransactionID, expected)
			}
			tx := pickTransaction(batch, in.TransactionID)
			if tx == nil {
				continue
			}
			if expected[string(tx.Status)] {
				return waitForStateMatched(tx), nil
			}
		}
	}
}

func pickTransaction(batch *types.Transactions, txID string) *types.Transaction {
	if batch == nil {
		return nil
	}
	for i := range batch.Transactions {
		if batch.Transactions[i].TransactionID == txID {
			return &batch.Transactions[i]
		}
	}
	return nil
}

// waitForStateTimeout does a final REST GET to capture the up-to-the-second
// status and returns a non-error result with a continuation hint. We use the
// outer ctx (not waitCtx, which is already done) so the GET has its own
// deadline budget.
func waitForStateTimeout(ctx context.Context, sdkClient *sdk.BronClient, txID string, expected map[string]bool) (any, error) {
	getCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	tx, err := sdkClient.Transactions.GetTransactionByID(getCtx, txID)
	if err != nil {
		return nil, fmt.Errorf("final get: %w", err)
	}
	// Belt-and-suspenders: in case the matching transition arrived between the
	// last frame and the timeout firing, the final GET will catch it.
	if expected[string(tx.Status)] {
		return waitForStateMatched(tx), nil
	}
	return map[string]any{
		"matched":      false,
		"currentState": string(tx.Status),
		"transaction":  asJSON(tx),
		"retryHint":    fmt.Sprintf("Transaction still in %q. Call bron_tx_wait_for_state again with the same args to keep waiting.", tx.Status),
	}, nil
}

func waitForStateMatched(tx *types.Transaction) any {
	return map[string]any{
		"matched":      true,
		"currentState": string(tx.Status),
		"transaction":  asJSON(tx),
	}
}

// asJSON round-trips a typed value through JSON so the MCP structuredContent
// surfaces it as a plain JSON object (consistent with the rest of the tool
// outputs in this server).
func asJSON(v interface{}) interface{} {
	b, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out interface{}
	if err := json.Unmarshal(b, &out); err != nil {
		return v
	}
	return out
}

func ptr[T any](v T) *T { return &v }
