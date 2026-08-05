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

// The backend has no includePrices/includeNetworks on the balances endpoint,
// so these embeds are client-side joins.
func wrapBalancesListEmbeds(root *cobra.Command, gf *globalFlags) {
	var bal *cobra.Command
	for _, res := range root.Commands() {
		if res.Name() != "balances" {
			continue
		}
		for _, sub := range res.Commands() {
			if sub.Name() == "list" {
				bal = sub
				break
			}
		}
	}
	if bal == nil {
		return
	}
	orig := bal.RunE
	bal.RunE = func(cmd *cobra.Command, args []string) error {
		wantsPrices := embedHasToken(gf.embed, "prices")
		wantsNetworks := embedHasToken(gf.embed, "networks")
		if !wantsPrices && !wantsNetworks {
			return orig(cmd, args)
		}
		return runBalancesWithEmbeds(cmd, gf, wantsPrices, wantsNetworks)
	}
}

func runBalancesWithEmbeds(cmd *cobra.Command, gf *globalFlags, wantsPrices, wantsNetworks bool) error {
	cli, err := buildClient(gf)
	if err != nil {
		return err
	}
	ctx := cmd.Context()

	balances, err := fetchBalances(ctx, cli, cmd)
	if err != nil {
		return err
	}

	if wantsPrices {
		if assetIds := mcptools.UniqueAssetIds(balances); len(assetIds) > 0 {
			priceByAsset, err := mcptools.FetchAssetPrices(ctx, cli, assetIds)
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: --embed prices: could not fetch market prices: %v\n", err)
			} else {
				mcptools.MergeBalancePrices(balances, priceByAsset)
			}
		}
	}

	if wantsNetworks {
		if networkIds := mcptools.UniqueNetworkIds(balances); len(networkIds) > 0 {
			testnetById, err := mcptools.FetchNetworkTestnetFlags(ctx, cli, networkIds)
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: --embed networks: could not fetch networks: %v\n", err)
			} else {
				mcptools.MergeBalanceNetworks(balances, testnetById)
			}
		}
	}

	return output.Print(balances)
}

// fetchBalances issues `GET /workspaces/{workspaceId}/balances` mirroring the
// generated cligen flow: read every query-typed flag the user actually set
// and forward to the backend. Stays in sync with HelpEntries automatically —
// no hardcoded flag list to drift.
func fetchBalances(ctx context.Context, cli *client.Client, cmd *cobra.Command) (interface{}, error) {
	entry, ok := catalog.HelpEntries["balances"]["list"]
	if !ok {
		return nil, fmt.Errorf("balances list entry missing from generated HelpEntries")
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
