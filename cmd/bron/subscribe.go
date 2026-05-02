package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	sdk "github.com/bronlabs/bron-sdk-go/sdk"
	"github.com/bronlabs/bron-sdk-go/sdk/realtime"
	"github.com/bronlabs/bron-sdk-go/sdk/types"

	"github.com/bronlabs/bron-cli/internal/config"
	"github.com/bronlabs/bron-cli/internal/output"
	"github.com/bronlabs/bron-cli/internal/qparam"
)

// newTxSubscribeCmd builds `bron tx subscribe`.
//
// All wire-protocol logic (WS dial, JWT signing, envelope encoding, status
// decoding, shutdown coordination) lives in bron-sdk-go's `sdk/realtime` and
// `sdk/api/transactions.go`. This file is a thin CLI surface — flag parsing,
// signal handling, JSON-line output.
func newTxSubscribeCmd(gf *globalFlags) *cobra.Command {
	var (
		accountID      string
		accountIDs     string
		txID           string
		txIDs          string
		statuses       string
		statusNotIn    string
		txTypes        string
		createdAtFrom  string
		createdAtTo    string
		withHistory    bool
	)
	cmd := &cobra.Command{
		Use:   "subscribe",
		Short: "Stream transaction updates over WebSocket (live-only by default)",
		Long: `Stream transaction updates from the Bron public API over a WebSocket.

The CLI connects to wss://<api-host>/ws and prints each pushed transaction as
one JSON line on stdout (newline-delimited, easy to pipe to jq / awk / fzf).

Output is always JSONL — the global --output flag is ignored for this command
(table/yaml don't make sense on an open-ended stream). Pipe to jq for any
reshaping you'd normally do with --output / --columns.

By default the CLI subscribes to live updates only, with no replay of existing
matching transactions on connect. This is the right default for long-running
watchers ("show me as transactions move into signing-required"). Pass
--with-history to also replay every currently-matching transaction on connect
before the live stream begins (useful for scripts that need a full snapshot
plus live tail in one command).

Filter flags mirror the list endpoint: --accountId, --accountIds,
--transactionId, --transactionIds, --transactionTypes, --transactionStatuses,
--transactionStatusNotIn, --createdAtFrom, --createdAtTo.`,
		Example: `  bron tx subscribe
  bron tx subscribe --with-history
  bron tx subscribe --transactionId <txId>                                  # follow one tx
  bron tx subscribe --transactionStatuses signing-required,waiting-approval
  bron tx subscribe --accountId <accountId> --transactionTypes withdrawal,bridge
  bron tx subscribe --transactionStatusNotIn canceled,expired,error
  bron tx subscribe | jq 'select(.status=="signed") | .transactionId'`,
		RunE: func(c *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			profile, err := cfg.Resolve(gf.profile)
			if err != nil {
				return err
			}
			workspace := firstNonEmpty(gf.workspace, profile.Workspace)
			if workspace == "" {
				return fmt.Errorf("workspace not set")
			}
			keyBytes, err := profile.LoadKey()
			if err != nil {
				return err
			}

			// Stay quiet on the happy reconnect path (instant 1-attempt
			// retry, zero backoff). Only surface real flapping — when we
			// hit any backoff at all or need >1 attempt — so the user
			// notices when something is actually wrong.
			lifecycle := func(evt realtime.LifecycleEvent) {
				switch evt.Kind {
				case realtime.EventReconnecting:
					if evt.Backoff > 0 || evt.Attempt > 1 {
						fmt.Fprintf(os.Stderr, "reconnecting (attempt %d, after %s): %v\n", evt.Attempt, evt.Backoff, evt.Err)
					}
				case realtime.EventReconnected:
					if evt.Attempt > 1 {
						fmt.Fprintf(os.Stderr, "reconnected (after %d attempts)\n", evt.Attempt)
					}
				}
			}

			sdkOpts := []sdk.ClientOption{sdk.WithRealtimeLifecycleHandler(lifecycle)}
			if gf.debug {
				h := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})
				sdkOpts = append(sdkOpts, sdk.WithRealtimeLogger(slog.New(h)))
			}

			client := sdk.NewBronClientWithOptions(sdk.BronClientConfig{
				APIKey:      strings.TrimSpace(string(keyBytes)),
				WorkspaceID: workspace,
				BaseURL:     profile.BaseURL,
				Proxy:       profile.Proxy,
			}, sdkOpts...)

			filter := buildTxFilter(txFilterArgs{
				accountID:     accountID,
				accountIDs:    accountIDs,
				txID:          txID,
				txIDs:         txIDs,
				statuses:      statuses,
				statusNotIn:   statusNotIn,
				txTypes:       txTypes,
				createdAtFrom: createdAtFrom,
				createdAtTo:   createdAtTo,
				withHistory:   withHistory,
			})

			ctx, cancel := signal.NotifyContext(c.Context(), os.Interrupt, syscall.SIGTERM)
			defer cancel()

			stream, err := client.Transactions.SubscribeWithFilter(ctx, filter)
			if err != nil {
				return fmt.Errorf("subscribe: %w", err)
			}
			defer func() { _ = stream.Close() }()

			fmt.Fprintln(os.Stderr, "subscribed; Ctrl-C to exit")

			enc := json.NewEncoder(os.Stdout)
			for batch := range stream.Updates() {
				if batch == nil {
					continue
				}
				for i := range batch.Transactions {
					_ = enc.Encode(output.HumanizeDates(asMap(&batch.Transactions[i])))
				}
			}
			return stream.Err()
		},
	}
	cmd.Flags().StringVar(&accountID, "accountId", "", "filter by account ID")
	cmd.Flags().StringVar(&accountIDs, "accountIds", "", "comma-separated account IDs")
	cmd.Flags().StringVar(&txID, "transactionId", "", "follow one transaction by ID")
	cmd.Flags().StringVar(&txIDs, "transactionIds", "", "comma-separated transaction IDs")
	cmd.Flags().StringVar(&statuses, "transactionStatuses", "", "comma-separated status filter (e.g. signing-required,waiting-approval)")
	cmd.Flags().StringVar(&statusNotIn, "transactionStatusNotIn", "", "comma-separated statuses to exclude (e.g. canceled,expired,error)")
	cmd.Flags().StringVar(&txTypes, "transactionTypes", "", "comma-separated transactionType filter (e.g. withdrawal,bridge)")
	cmd.Flags().StringVar(&createdAtFrom, "createdAtFrom", "", "filter by created date since timestamp (ISO-8601 or epoch millis)")
	cmd.Flags().StringVar(&createdAtTo, "createdAtTo", "", "filter by created date until timestamp (ISO-8601 or epoch millis)")
	cmd.Flags().BoolVar(&withHistory, "with-history", false, "also replay every currently-matching transaction on connect, before the live stream begins (off by default — most watchers want live-only)")
	return cmd
}

// txFilterArgs groups flag values for the SUBSCRIBE envelope body. Adding to
// this struct + a single `if v != "" filter[k] = ...` line below is the only
// place new filters need to be wired through — no other call sites.
type txFilterArgs struct {
	accountID     string
	accountIDs    string
	txID          string
	txIDs         string
	statuses      string
	statusNotIn   string
	txTypes       string
	createdAtFrom string
	createdAtTo   string
	withHistory   bool
}

// buildTxFilter assembles the SUBSCRIBE envelope body as a map. Map (vs the
// typed TransactionsQuery) lets us send `limit` as a JSON number — backend's
// Long limit field doesn't coerce "0" → 0L over WS. Strings/arrays for the
// other filters work the same in both shapes.
//
// `limit: 0` tells the backend to skip the snapshot replay and only stream
// live updates — that's the default for `bron tx subscribe`. The user opts
// into replay with --with-history, which omits the limit and lets the
// backend send everything matching the filter on connect.
func buildTxFilter(args txFilterArgs) map[string]interface{} {
	filter := map[string]interface{}{}
	if args.accountID != "" {
		filter["accountId"] = args.accountID
	}
	if args.accountIDs != "" {
		filter["accountIds"] = splitCSV(args.accountIDs)
	}
	if args.txID != "" {
		filter["transactionId"] = args.txID
	}
	if args.txIDs != "" {
		filter["transactionIds"] = splitCSV(args.txIDs)
	}
	if args.statuses != "" {
		filter["transactionStatuses"] = splitCSV(args.statuses)
	}
	if args.statusNotIn != "" {
		filter["transactionStatusNotIn"] = splitCSV(args.statusNotIn)
	}
	if args.txTypes != "" {
		filter["transactionTypes"] = splitCSV(args.txTypes)
	}
	for k, v := range map[string]string{
		"createdAtFrom": args.createdAtFrom,
		"createdAtTo":   args.createdAtTo,
	} {
		if v == "" {
			continue
		}
		coerced, err := qparam.MaybeDate(k, v)
		if err != nil {
			// Surface as a backend-level filter mismatch rather than a CLI
			// hard-fail — the WS dispatcher will reject invalid timestamps.
			filter[k] = v
			continue
		}
		filter[k] = coerced
	}
	if !args.withHistory {
		filter["limit"] = 0
	}
	return filter
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// asMap round-trips a typed Transaction through JSON so output.HumanizeDates
// can apply the same epoch-millis-to-ISO conversion the REST path uses. Falls
// back to the raw value if the round-trip fails (which it shouldn't for any
// generated DTO).
func asMap(v *types.Transaction) interface{} {
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
