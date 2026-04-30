package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/bronlabs/bron-cli/generated"
	"github.com/bronlabs/bron-cli/internal/client"
	"github.com/bronlabs/bron-cli/internal/output"
)

// wrapTxListEmbedAssets replaces the generated `tx list` RunE with a wrapper
// that resolves `params.assetId` for every transaction into the full Asset DTO
// when `--embed assets` is on.
//
// Without it, agents listing transactions get raw assetIds (`5002`, `22611`)
// and have to issue one `assets get <id>` per unique id to learn the symbol /
// networkId / decimals — exactly the N+1 the v0.3.3 sub-agent test surfaced.
// Backend's TransactionEmbedded does not yet carry an Asset slot, so the join
// happens here: one batch `/dictionary/assets?assetIds=<csv>` after the tx
// fetch, then a per-tx mutation under `_embedded.asset`.
func wrapTxListEmbedAssets(root *cobra.Command, gf *globalFlags) {
	var tx *cobra.Command
	for _, res := range root.Commands() {
		if res.Name() != "tx" {
			continue
		}
		for _, sub := range res.Commands() {
			if sub.Name() == "list" {
				tx = sub
				break
			}
		}
	}
	if tx == nil {
		return
	}
	orig := tx.RunE
	tx.RunE = func(cmd *cobra.Command, args []string) error {
		if !embedHasToken(gf.embed, "assets") {
			return orig(cmd, args)
		}
		return runTxListWithAssets(cmd, gf)
	}
}

func runTxListWithAssets(cmd *cobra.Command, gf *globalFlags) error {
	cli, err := buildClient(gf)
	if err != nil {
		return err
	}
	ctx := cmd.Context()

	txs, err := fetchTxList(ctx, cli, cmd)
	if err != nil {
		return err
	}

	assetIds := uniqueTxAssetIds(txs)
	if len(assetIds) == 0 {
		return output.Print(txs)
	}

	assetById, err := fetchAssetsById(ctx, cli, assetIds)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: --embed assets: could not fetch asset details: %v\n", err)
		return output.Print(txs)
	}

	embedAssetsIntoTxs(txs, assetById)
	return output.Print(txs)
}

func fetchTxList(ctx context.Context, cli *client.Client, cmd *cobra.Command) (interface{}, error) {
	entry, ok := generated.HelpEntries["tx"]["list"]
	if !ok {
		return nil, fmt.Errorf("tx list entry missing from generated HelpEntries")
	}
	queryNames := map[string]bool{}
	for _, q := range entry.QueryParams {
		queryNames[q.Name] = true
	}
	query := map[string]interface{}{}
	cmd.Flags().Visit(func(f *pflag.Flag) {
		if !queryNames[f.Name] {
			return
		}
		if v := f.Value.String(); v != "" {
			query[f.Name] = v
		}
	})
	var result interface{}
	if err := cli.Do(ctx, entry.Method, entry.Path, nil, nil, query, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func fetchAssetsById(ctx context.Context, cli *client.Client, assetIds []string) (map[string]map[string]interface{}, error) {
	var v interface{}
	query := map[string]interface{}{"assetIds": strings.Join(assetIds, ",")}
	if err := cli.Do(ctx, "GET", "/dictionary/assets", nil, nil, query, &v); err != nil {
		return nil, err
	}

	out := map[string]map[string]interface{}{}
	m, ok := v.(map[string]interface{})
	if !ok {
		return out, nil
	}
	arr, _ := m["assets"].([]interface{})
	for _, item := range arr {
		am, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if id, _ := am["assetId"].(string); id != "" {
			out[id] = am
		}
	}
	return out, nil
}

// uniqueTxAssetIds collects every assetId reachable through `params.assetId` on
// each transaction. Intent transactions store fromAssetId/toAssetId in a
// separate intents resource, not on the tx itself, so they're not resolved
// here — that needs `--embed intents` (out of scope for this commit).
func uniqueTxAssetIds(v interface{}) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range txItems(v) {
		params, _ := t["params"].(map[string]interface{})
		if id, _ := params["assetId"].(string); id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func txItems(v interface{}) []map[string]interface{} {
	return mapItems(v, "transactions")
}

// embedAssetsIntoTxs attaches the resolved Asset under `_embedded.asset` on
// each transaction whose `params.assetId` is in the map. Sticks to the
// existing TransactionEmbedded convention (`_embedded` already carries other
// resolved entities like accounts, events, signing requests).
func embedAssetsIntoTxs(v interface{}, assetById map[string]map[string]interface{}) {
	for _, t := range txItems(v) {
		params, _ := t["params"].(map[string]interface{})
		assetId, _ := params["assetId"].(string)
		asset, ok := assetById[assetId]
		if !ok {
			continue
		}
		emb, _ := t["_embedded"].(map[string]interface{})
		if emb == nil {
			emb = map[string]interface{}{}
			t["_embedded"] = emb
		}
		emb["asset"] = asset
	}
}
