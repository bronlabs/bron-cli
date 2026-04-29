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
// Backend currently has no `includePrices` filter — this is a CLI-side
// orchestration that joins three calls (`balances list`, `assets list`,
// `symbols prices`) the same way a manual jq pipeline would. The asset
// list is needed because `balances` returns assetId while `symbols prices`
// keys by symbolId. Until /dictionary/asset-market-prices is fixed and the
// spec regenerates, this 3-way merge is what gives us a single CLI command.
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

// fetchAssetPrices returns a map keyed by assetId. Joins the three sources
// the public API exposes today: assets list (assetId↔symbolId mapping),
// symbols prices (symbolId→price). Resolves both in parallel.
func fetchAssetPrices(ctx context.Context, cli *client.Client, assetIds []string) (map[string]assetPrice, error) {
	idsCSV := strings.Join(assetIds, ",")

	type result struct {
		v   interface{}
		err error
	}
	assetsCh := make(chan result, 1)
	pricesCh := make(chan result, 1)

	go func() {
		var v interface{}
		err := cli.Do(ctx, "GET", "/dictionary/assets", nil, nil, map[string]interface{}{"assetIds": idsCSV}, &v)
		assetsCh <- result{v, err}
	}()
	go func() {
		var v interface{}
		err := cli.Do(ctx, "GET", "/dictionary/symbol-market-prices", nil, nil, map[string]interface{}{"baseAssetIds": idsCSV}, &v)
		pricesCh <- result{v, err}
	}()

	assets := <-assetsCh
	prices := <-pricesCh
	if assets.err != nil {
		return nil, fmt.Errorf("fetch assets: %w", assets.err)
	}
	if prices.err != nil {
		return nil, fmt.Errorf("fetch prices: %w", prices.err)
	}

	symbolByAsset := mapAssetIdToSymbolId(assets.v)
	priceBySymbol := mapSymbolIdToPrice(prices.v)

	out := map[string]assetPrice{}
	for assetId, symbolId := range symbolByAsset {
		if p, ok := priceBySymbol[symbolId]; ok {
			out[assetId] = p
		}
	}
	return out, nil
}

type assetPrice struct {
	SymbolId      string
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

func mapAssetIdToSymbolId(v interface{}) map[string]string {
	out := map[string]string{}
	m, ok := v.(map[string]interface{})
	if !ok {
		return out
	}
	arr, _ := m["assets"].([]interface{})
	for _, item := range arr {
		am, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		assetId, _ := am["assetId"].(string)
		symbolId, _ := am["symbolId"].(string)
		if assetId != "" && symbolId != "" {
			out[assetId] = symbolId
		}
	}
	return out
}

func mapSymbolIdToPrice(v interface{}) map[string]assetPrice {
	out := map[string]assetPrice{}
	m, ok := v.(map[string]interface{})
	if !ok {
		return out
	}
	arr, _ := m["prices"].([]interface{})
	for _, item := range arr {
		pm, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		baseSymbolId, _ := pm["baseSymbolId"].(string)
		quoteSymbolId, _ := pm["quoteSymbolId"].(string)
		price := numberAsString(pm["price"])
		if baseSymbolId == "" || price == "" {
			continue
		}
		out[baseSymbolId] = assetPrice{
			SymbolId:      baseSymbolId,
			QuoteSymbolId: quoteSymbolId,
			Price:         price,
		}
	}
	return out
}

// mergeBalancePrices mutates the balances response in place: each item gets
// `usdPrice` (raw price string), `usdQuoteSymbolId`, and `usdValue =
// totalBalance * price` rendered with the same precision as totalBalance so
// big.Rat output matches what users expect from --output table.
func mergeBalancePrices(v interface{}, prices map[string]assetPrice) {
	for _, b := range balanceItems(v) {
		assetId, _ := b["assetId"].(string)
		p, ok := prices[assetId]
		if !ok {
			continue
		}
		b["usdPrice"] = p.Price
		if p.QuoteSymbolId != "" {
			b["usdQuoteSymbolId"] = p.QuoteSymbolId
		}
		total := numberAsString(b["totalBalance"])
		if total == "" {
			continue
		}
		if usdValue := mulDecimal(total, p.Price); usdValue != "" {
			b["usdValue"] = usdValue
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
