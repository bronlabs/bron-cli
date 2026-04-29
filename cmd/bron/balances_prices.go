package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/bronlabs/bron-cli/generated"
	"github.com/bronlabs/bron-cli/internal/client"
	"github.com/bronlabs/bron-cli/internal/output"
)

// wrapBalancesListEmbedPrices replaces the generated `balances list` RunE with
// a wrapper that augments each balance with `usdPrice` and `usdValue` when
// `--embed prices` is on. Falls through to the original RunE otherwise.
//
// The backend has no `includePrices` filter on the balances endpoint, so this
// is a CLI-side join: fetch balances, then call /dictionary/asset-market-prices
// keyed by assetId. AssetMarketPrice already carries baseAssetId/quoteSymbolId
// natively, so the merge is a single lookup per balance entry.
func wrapBalancesListEmbedPrices(root *cobra.Command, gf *globalFlags) {
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
		if !embedHasToken(gf.embed, "prices") {
			return orig(cmd, args)
		}
		return runBalancesWithPrices(cmd, gf)
	}
}

func runBalancesWithPrices(cmd *cobra.Command, gf *globalFlags) error {
	cli, err := buildClient(gf)
	if err != nil {
		return err
	}
	ctx := cmd.Context()

	balances, err := fetchBalances(ctx, cli, cmd)
	if err != nil {
		return err
	}

	assetIds := uniqueAssetIds(balances)
	if len(assetIds) == 0 {
		return output.Print(balances)
	}

	priceByAsset, err := fetchAssetPrices(ctx, cli, assetIds)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: --embed prices: could not fetch market prices: %v\n", err)
		return output.Print(balances)
	}

	mergeBalancePrices(balances, priceByAsset)
	return output.Print(balances)
}

// fetchBalances issues `GET /workspaces/{workspaceId}/balances` mirroring the
// generated cligen flow: read every query-typed flag the user actually set
// and forward to the backend. Stays in sync with HelpEntries automatically —
// no hardcoded flag list to drift.
func fetchBalances(ctx context.Context, cli *client.Client, cmd *cobra.Command) (interface{}, error) {
	entry, ok := generated.HelpEntries["balances"]["list"]
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

// fetchAssetPrices returns a price-by-assetId map. /dictionary/asset-market-prices
// already echoes baseAssetId on each row, so a single call covers what used to
// take three (balances → assets list → symbol-market-prices) before the spec
// fix in libs/datamodel + platform/public-api.
func fetchAssetPrices(ctx context.Context, cli *client.Client, assetIds []string) (map[string]assetPrice, error) {
	var v interface{}
	query := map[string]interface{}{"baseAssetIds": strings.Join(assetIds, ",")}
	if err := cli.Do(ctx, "GET", "/dictionary/asset-market-prices", nil, nil, query, &v); err != nil {
		return nil, err
	}

	out := map[string]assetPrice{}
	m, ok := v.(map[string]interface{})
	if !ok {
		return out, nil
	}
	prices, _ := m["prices"].([]interface{})
	for _, item := range prices {
		pm, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		assetId, _ := pm["baseAssetId"].(string)
		quoteSymbolId, _ := pm["quoteSymbolId"].(string)
		price := numberAsString(pm["price"])
		if assetId == "" || price == "" {
			continue
		}
		out[assetId] = assetPrice{QuoteSymbolId: quoteSymbolId, Price: price}
	}
	return out, nil
}

type assetPrice struct {
	QuoteSymbolId string
	Price         string
}

// uniqueAssetIds walks a balances response and returns deduplicated assetIds.
// Treats both the wrapped `{"balances":[...]}` shape and a bare array as input.
func uniqueAssetIds(v interface{}) []string {
	seen := map[string]bool{}
	var out []string
	for _, b := range balanceItems(v) {
		if id, ok := b["assetId"].(string); ok && id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func balanceItems(v interface{}) []map[string]interface{} {
	switch t := v.(type) {
	case map[string]interface{}:
		if arr, ok := t["balances"].([]interface{}); ok {
			return castMapSlice(arr)
		}
	case []interface{}:
		return castMapSlice(t)
	}
	return nil
}

func castMapSlice(arr []interface{}) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(arr))
	for _, item := range arr {
		if m, ok := item.(map[string]interface{}); ok {
			out = append(out, m)
		}
	}
	return out
}

// mergeBalancePrices mutates the balances response in place: each item gets
// `usdPrice`, `usdQuoteSymbolId`, and `usdValue = totalBalance * price` placed
// under `_embedded` so calculated fields don't pollute the spec-defined
// Balance shape. Same HATEOAS-style nesting that backend uses for resolved
// entities (e.g. WorkspaceMemberEmbedded). Multiplication uses big.Rat so
// trailing precision survives.
func mergeBalancePrices(v interface{}, prices map[string]assetPrice) {
	for _, b := range balanceItems(v) {
		assetId, _ := b["assetId"].(string)
		p, ok := prices[assetId]
		if !ok {
			continue
		}
		emb, _ := b["_embedded"].(map[string]interface{})
		if emb == nil {
			emb = map[string]interface{}{}
			b["_embedded"] = emb
		}
		emb["usdPrice"] = p.Price
		if p.QuoteSymbolId != "" {
			emb["usdQuoteSymbolId"] = p.QuoteSymbolId
		}
		total := numberAsString(b["totalBalance"])
		if total == "" {
			continue
		}
		if usdValue := mulDecimal(total, p.Price); usdValue != "" {
			emb["usdValue"] = usdValue
		}
	}
}

// numberAsString accepts the raw types `cli.Do` produces under UseNumber:
// json.Number values stringify to their wire form, plain strings pass through.
func numberAsString(v interface{}) string {
	switch t := v.(type) {
	case string:
		return t
	case json.Number:
		return string(t)
	}
	return ""
}

// mulDecimal multiplies two decimal strings without losing precision (the
// whole point of this CLI keeping decimals as strings). Result is rendered
// with up to 18 digits after the dot, trailing zeros trimmed; "" on parse
// failure so the merge step skips silently.
func mulDecimal(a, b string) string {
	ar, ok := new(big.Rat).SetString(a)
	if !ok {
		return ""
	}
	br, ok := new(big.Rat).SetString(b)
	if !ok {
		return ""
	}
	out := new(big.Rat).Mul(ar, br).FloatString(18)
	out = strings.TrimRight(out, "0")
	out = strings.TrimRight(out, ".")
	return out
}
