package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/bronlabs/bron-api-toolkit/catalog"
	"github.com/bronlabs/bron-api-toolkit/mcptools"
	"github.com/bronlabs/bron-api-toolkit/output"
	"github.com/bronlabs/bron-cli/internal/client"
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

	assetIds := mcptools.UniqueTxAssetIds(txs)
	if len(assetIds) == 0 {
		return output.Print(txs)
	}

	assetById, err := mcptools.FetchAssetsById(ctx, cli, assetIds)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: --embed assets: could not fetch asset details: %v\n", err)
		return output.Print(txs)
	}

	mcptools.EmbedAssetsIntoTxs(txs, assetById)
	return output.Print(txs)
}

func fetchTxList(ctx context.Context, cli *client.Client, cmd *cobra.Command) (interface{}, error) {
	entry, ok := catalog.HelpEntries["tx"]["list"]
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
