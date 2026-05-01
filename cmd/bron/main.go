package main

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"strings"

	sdk "github.com/bronlabs/bron-sdk-go/sdk"
	sdkhttp "github.com/bronlabs/bron-sdk-go/sdk/http"
	"github.com/spf13/cobra"

	"github.com/bronlabs/bron-cli/generated"
	"github.com/bronlabs/bron-cli/internal/client"
	"github.com/bronlabs/bron-cli/internal/config"
	"github.com/bronlabs/bron-cli/internal/output"
	"github.com/bronlabs/bron-cli/internal/qparam"
)

var Version = "dev"

// global flags shared by every command
type globalFlags struct {
	profile   string
	workspace string
	baseURL   string
	keyFile   string
	proxy     string
	output    string
	query     string
	columns   string
	cellMax   int
	embed     string
	schema    bool
	debug     bool
}

const rootLong = `Bron CLI — public API client.

Resources follow the URL: bron <resource> <verb>. The <workspaceId> is implicit (from the active profile config).`

const rootExample = `  bron help
  bron help <resource> <verb> [--output yaml]
  bron help <topic>                   # signing | profiles | output | body | errors | idempotency | agents
  bron help --schema                  # full CLI schema (every command + types) as one JSON

  bron auth keygen --file ~/.config/bron/keys/me.jwk

  bron config
  bron config init --workspace <workspaceId> --key-file ~/.config/bron/keys/me.jwk
  bron config use-profile production
  bron config set workspace=<workspaceId> key_file=~/.config/bron/keys/me.jwk

  bron accounts list --accountTypes vault --statuses active --limit 50
  bron accounts get <accountId>

  bron balances list --accountId <accountId> --assetId 5000 --networkId ETH --nonEmpty true
  bron balances list --embed prices --nonEmpty true        # attaches _embedded.usdPrice / _embedded.usdValue
  bron balances list --embed prices --output table --columns accountId,symbol,totalBalance,_embedded.usdValue

  bron tx list --transactionStatuses waiting-approval,signing --limit 50
  bron tx list --transactionTypes withdrawal,allowance --createdAtFrom 2026-04-01
  bron tx get    <transactionId>
  bron tx events <transactionId>

  bron tx withdrawal \
    --accountId <accountId> \
    --externalId <idempotencyKey> \
    --description "Q2 vendor payout" \
    --params.amount=100 \
    --params.assetId=5000 \
    --params.networkId=ETH \
    --params.toAddress=<address> \
    --params.memo="invoice-42" \
    --params.feeLevel=medium \
    --params.includeFee=true

  bron tx allowance           # see "bron tx allowance --help" for params
  bron tx bridge
  bron tx deposit
  bron tx defi
  bron tx defi-message
  bron tx intents
  bron tx stake-delegation
  bron tx stake-undelegation
  bron tx stake-claim
  bron tx stake-withdrawal
  bron tx address-creation
  bron tx address-activation
  bron tx fiat-in
  bron tx fiat-out

  bron tx withdrawal --file ./tx.json
  cat tx.json | bron tx withdrawal --file -
  bron tx withdrawal --json '{"accountId":"<accountId>","params":{"amount":100,"assetId":"5000"}}'
  bron tx withdrawal --file ./tx.json --params.amount=250 --externalId <idempotencyKey>

  bron tx approve                <transactionId>
  bron tx decline                <transactionId>
  bron tx cancel                 <transactionId>
  bron tx create-signing-request <transactionId>
  bron tx accept-deposit-offer   <transactionId>
  bron tx reject-outgoing-offer  <transactionId>

  # Lower-level — when no "tx <type>" shortcut fits or you want full control:
  bron tx create \
    --transactionType withdrawal \
    --accountId <accountId> \
    --externalId <idempotencyKey> \
    --params.amount=100 \
    --params.assetId=5000 \
    --params.networkId=ETH \
    --params.toAddress=<address>

  bron tx dry-run     --file ./tx.json
  bron tx bulk-create --file ./batch.json

  bron tx list --output yaml
  bron tx list --output table --columns transactionId,status,transactionType,createdAt
  bron tx list --output table --query '.transactions[*]'
  bron tx list --output table --cell-max 0           # disable column truncation; full IDs/addresses
  bron tx get <transactionId> --query '.status'

  bron deposit-addresses list --accountId <accountId> --networkId ETH

  bron activities list --accountIds <accountId> --activityTypes login,transaction-completed --limit 50

  bron members list --includePermissionGroups true --includeUsersProfiles true

  bron stakes list --accountId <accountId> --assetId 5000

  bron transaction-limits list --statuses active
  bron transaction-limits get <limitId>

  bron workspace info --includeSettings true

  bron assets list --search btc --assetType blockchain --networkIds ETH --limit 50
  bron assets get <assetId>
  bron assets prices

  bron networks list --networkIds ETH,TRX
  bron networks get <networkId>

  bron symbols list --assetIds 5000,5001
  bron symbols prices --baseAssetIds 5000

  bron intents create --json '{"params":{...}}'
  bron intents get <intentId>

  bron address-book list --networkIds ETH,TRX
  bron address-book create --name "Alice" --address <address> --networkId ETH
  bron address-book delete <recordId>

  bron completion install              # auto-detects $SHELL (zsh|bash|fish)`

func main() {
	gf := &globalFlags{}

	dateKeys := collectDateKeysFromSpec()
	output.SetDateKeys(dateKeys)
	qparam.SetDateKeys(dateKeys)

	root := &cobra.Command{
		Use:           "bron <resource> <verb> [<id>...] [flags]",
		Short:         "Bron CLI — public API client",
		Long:          rootLong,
		Example:       rootExample,
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddGroup(
		&cobra.Group{ID: "api", Title: "API commands:"},
		&cobra.Group{ID: "system", Title: "System commands:"},
	)
	defaultHelp := root.HelpFunc()
	root.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		if cmd != root {
			defaultHelp(cmd, args)
			appendReturnsHint(cmd)
			return
		}
		out := cmd.OutOrStdout()
		fp := func(args ...interface{}) { _, _ = fmt.Fprintln(out, args...) }
		if cmd.Long != "" {
			fp(cmd.Long)
			fp()
		}
		if cmd.Example != "" {
			fp("Examples:")
			fp(cmd.Example)
			fp()
		}
		if cmd.HasAvailableLocalFlags() {
			fp("Flags:")
			_, _ = fmt.Fprint(out, cmd.LocalFlags().FlagUsages())
			fp()
		}
		fp(`Use "bron <resource> <verb> --help" for any command's flags.`)
	})
	root.PersistentFlags().StringVar(&gf.profile, "profile", "", "config profile name")
	root.PersistentFlags().StringVar(&gf.workspace, "workspace", "", "workspace id (overrides profile)")
	root.PersistentFlags().StringVar(&gf.baseURL, "base-url", "", "API base URL (overrides profile; mostly for QA/staging)")
	_ = root.PersistentFlags().MarkHidden("base-url")
	root.PersistentFlags().StringVar(&gf.keyFile, "key-file", "", "path to JWK private key (overrides profile)")
	root.PersistentFlags().StringVar(&gf.proxy, "proxy", "", "HTTP/HTTPS proxy URL (overrides profile)")
	root.PersistentFlags().StringVar(&gf.output, "output", "", "output format: table|json|yaml|jsonl (default json)")
	root.PersistentFlags().StringVar(&gf.query, "query", "", "JSONPath subset filter, e.g. .transactions[*].transactionId")
	root.PersistentFlags().StringVar(&gf.columns, "columns", "", "comma-separated keys to keep, e.g. transactionId,status,createdAt (works for json/yaml/jsonl/table)")
	root.PersistentFlags().IntVar(&gf.cellMax, "cell-max", 28, "max chars per table cell; 0 disables truncation")
	root.PersistentFlags().StringVar(&gf.embed, "embed", "", "comma list of related entities to embed in the response (e.g. events,settings,permission-groups). Each token routes to the matching backend includeXxx flag if the endpoint exposes one")
	root.PersistentFlags().BoolVar(&gf.schema, "schema", false, "print JSON schema (request + response) for the command instead of running it")
	root.PersistentFlags().BoolVar(&gf.debug, "debug", false, "print debug logs to stderr (envelope dumps, ping/pong, dial attempts) — useful for `bron tx subscribe` and other long-running commands")

	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		output.SetFormat(gf.output)
		output.SetQuery(gf.query)
		output.SetColumns(gf.columns)
		output.SetCellMax(gf.cellMax)
		applyEmbed(cmd, gf.embed)
		if gf.schema {
			r, v, ok := resourceVerbFor(cmd)
			if !ok {
				return nil
			}
			// `bron tx subscribe` is "GET extended" — same Req/Resp as
			// `tx list` plus an open WebSocket. Fall back to the list
			// entry's schema and tag it as a streaming command.
			streaming := false
			if r == "tx" && v == "subscribe" {
				v = "list"
				streaming = true
			}
			entry, ok := lookupEntry(r, v)
			if !ok {
				return fmt.Errorf("no schema found for `bron %s %s`", r, v)
			}
			doc, err := buildCommandHelpDoc(r, v, entry)
			if err != nil {
				return err
			}
			if streaming {
				if x, ok := doc["x-bron-cli"].(map[string]interface{}); ok {
					x["streaming"] = "websocket"
					x["command"] = "tx subscribe"
					x["usage"] = "bron tx subscribe"
				}
			}
			if err := output.Print(doc); err != nil {
				return err
			}
			return errSchemaHandled
		}
		return nil
	}

	generated.Register(root, func() (*client.Client, error) { return buildClient(gf) })

	// `bron balances list --embed prices` and `bron tx list --embed assets`
	// are CLI-side orchestrations: the generated RunE doesn't know about
	// these tokens, so wrap each one to fall through to the augmented path
	// when the token is set.
	wrapBalancesListEmbedPrices(root, gf)
	wrapTxListEmbedAssets(root, gf)

	// --schema short-circuits the verb command, but cobra validates Args before
	// PersistentPreRun. Wrap each api-group cmd's Args so it is bypassed when
	// --schema is set, allowing `bron tx get --schema` to skip the
	// "transactionId required" check.
	walkAPICommands(root, func(c *cobra.Command) {
		orig := c.Args
		c.Args = func(c *cobra.Command, args []string) error {
			if gf.schema {
				return nil
			}
			if orig == nil {
				return nil
			}
			return orig(c, args)
		}
	})

	helpCmd := newHelpCmd()
	helpCmd.GroupID = "system"
	root.SetHelpCommand(helpCmd)

	// `bron tx subscribe` — WebSocket prototype, hand-written. Attach as a child
	// of the generated `tx` resource so it sits next to the regular verbs.
	for _, c := range root.Commands() {
		if c.Name() == "tx" {
			c.AddCommand(newTxSubscribeCmd(gf))
			break
		}
	}

	authCmd := newAuthCmd()
	authCmd.GroupID = "system"
	root.AddCommand(authCmd)

	configCmd := newConfigCmd()
	configCmd.GroupID = "system"
	root.AddCommand(configCmd)

	mcpCmd := newMCPCmd(gf)
	mcpCmd.GroupID = "system"
	root.AddCommand(mcpCmd)

	root.InitDefaultCompletionCmd()
	for _, c := range root.Commands() {
		if c.Name() == "completion" {
			c.GroupID = "system"
			c.AddCommand(newCompletionInstallCmd())
		}
	}

	if err := root.Execute(); err != nil {
		if errors.Is(err, errSchemaHandled) {
			return
		}
		var apiErr *sdkhttp.APIError
		if errors.As(err, &apiErr) {
			fmt.Fprintf(os.Stderr, "error: %s\n", output.SanitizeForTerminal(apiErr.Message))
			fmt.Fprintf(os.Stderr, "  status:   %d\n", apiErr.Status)
			if apiErr.Code != "" {
				fmt.Fprintf(os.Stderr, "  code:     %s\n", output.SanitizeForTerminal(apiErr.Code))
			}
			if apiErr.RequestID != "" {
				fmt.Fprintf(os.Stderr, "  trace:    %s\n", output.SanitizeForTerminal(apiErr.RequestID))
				fmt.Fprintln(os.Stderr, "  (paste the trace ID into a support ticket — the backend logs it as Error ID)")
			}
			os.Exit(exitCodeForStatus(apiErr.Status))
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// errSchemaHandled — sentinel returned by --schema's PersistentPreRunE so
// main() can short-circuit cleanly. os.Exit(0) inside PersistentPreRunE would
// bypass deferred test-harness cleanup.
var errSchemaHandled = errors.New("schema handled")

// resolveProfile loads the YAML config, picks the right profile (per
// `--profile` / `BRON_PROFILE` / active), and applies global-flag overrides
// for workspace / base-url / key-file / proxy. Shared by `buildClient`
// (REST-only) and `buildSDKClient` (REST + WS realtime).
func resolveProfile(gf *globalFlags) (*config.Profile, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	profile, err := cfg.Resolve(gf.profile)
	if err != nil {
		return nil, err
	}
	if gf.workspace != "" {
		profile.Workspace = gf.workspace
	}
	if gf.baseURL != "" {
		profile.BaseURL = gf.baseURL
	}
	if gf.keyFile != "" {
		// Explicit `--key-file` flag overrides any `BRON_API_KEY` env Resolve
		// may have picked up. Otherwise the env-injected JWK silently wins
		// and the flag is a no-op — confusing to debug, especially in CI
		// where both are sometimes set.
		profile.KeyFile = gf.keyFile
		profile.APIKey = ""
	}
	if gf.proxy != "" {
		profile.Proxy = gf.proxy
	}
	return profile, nil
}

func buildClient(gf *globalFlags) (*client.Client, error) {
	profile, err := resolveProfile(gf)
	if err != nil {
		return nil, err
	}
	return client.New(profile)
}

// buildSDKClient builds the bron-sdk-go BronClient, wiring REST + WS realtime
// transport. Used by composite MCP tools (e.g. `bron_tx_wait_for_state`) that
// need WS subscriptions on top of the REST surface. The simpler `buildClient`
// returns just the REST wrapper used by the auto-generated MCP tools.
//
// Reuses `client.BuildHTTPClient` so the proxy auth + scheme/host validation
// in REST and WS transports stay identical (otherwise the SDK's own proxy
// handling skips the validation pass and `proxy=host:8080` silently falls
// back to env).
func buildSDKClient(gf *globalFlags) (*sdk.BronClient, error) {
	profile, err := resolveProfile(gf)
	if err != nil {
		return nil, err
	}
	if profile.Workspace == "" {
		return nil, fmt.Errorf("workspace not set (configure ~/.config/bron/config.yaml or pass --workspace)")
	}
	keyBytes, err := profile.LoadKey()
	if err != nil {
		return nil, err
	}
	httpClient, err := client.BuildHTTPClient(profile.Proxy)
	if err != nil {
		return nil, err
	}
	opts := []sdk.ClientOption{sdk.WithNetHTTPClient(httpClient)}
	if gf.debug {
		h := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})
		opts = append(opts, sdk.WithRealtimeLogger(slog.New(h)))
	}
	return sdk.NewBronClientWithOptions(sdk.BronClientConfig{
		APIKey:      strings.TrimSpace(string(keyBytes)),
		WorkspaceID: profile.Workspace,
		BaseURL:     profile.BaseURL,
		Proxy:       profile.Proxy,
	}, opts...), nil
}

// newHelpCmd replaces cobra's default help command. Modes:
//
//	bron help                          — short navigation page (where to look)
//	bron help <topic>                  — print a topic blurb (signing, profiles, output, …)
//	bron help <resource>               — print resource subcommands (cobra default)
//	bron help <resource> <verb>        — same as `bron <resource> <verb> --help` (cobra-style text)
//	bron help <resource> <verb> --schema — JSON/YAML schema dump (request + response schema)
//	bron help --schema                 — dump the full CLI schema as one document
//
// `bron help` deliberately differs from `bron --help`: the bare flag mirrors
// cobra's flag-and-examples dump (good for humans scanning a terminal), while
// the subcommand prints a navigation page that points at topics, per-command
// help, and the JSON schema (good for agents deciding where to read next).
//
// `--schema` is the global root flag, so `bron <resource> <verb> --schema`
// produces the same dump as `bron help <resource> <verb> --schema`.
func newHelpCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "help [resource|topic] [verb]",
		Short: "Navigation page; pass <topic> or <resource> <verb> for details. Add --schema for a JSON dump.",
		Long: "Without arguments — prints a navigation page (topics + how to read per-command help + schema entry points).\n" +
			"For full root flags and examples, use `bron --help` instead.\n" +
			"With <topic> — prints the named topic blurb (`bron help topics` for the list).\n" +
			"With <resource> <verb> — prints the same text as `bron <resource> <verb> --help`.\n" +
			"With --schema — dumps the full CLI schema (without args) or a per-command schema (with <resource> <verb>).",
		DisableFlagParsing: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			schema, _ := cmd.Root().PersistentFlags().GetBool("schema")
			if schema && len(args) < 2 {
				doc, err := buildFullSchemaDoc()
				if err != nil {
					return err
				}
				return output.Print(doc)
			}
			if len(args) == 0 {
				return printHelpNavigation(cmd.OutOrStdout())
			}
			if len(args) == 1 {
				if blurb, ok := topics[args[0]]; ok {
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), blurb)
					return nil
				}
				if args[0] == "topics" {
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Available topics (run `bron help <topic>`):")
					for _, n := range topicNames() {
						_, _ = fmt.Fprintln(cmd.OutOrStdout(), "  "+n)
					}
					return nil
				}
			}
			if len(args) >= 2 && schema {
				entry, ok := lookupEntry(args[0], args[1])
				if !ok {
					return fmt.Errorf("unknown command: bron %s %s", args[0], args[1])
				}
				doc, err := buildCommandHelpDoc(args[0], args[1], entry)
				if err != nil {
					return err
				}
				return output.Print(doc)
			}
			target, _, err := cmd.Root().Find(args)
			if err != nil || target == nil {
				return cmd.Root().Help()
			}
			return target.Help()
		},
	}
	return cmd
}

// printHelpNavigation prints a compact map of bron's help system. Listing
// resources comes from the generated HelpEntries map so it stays in sync with
// the spec; topic names come from the static topics table.
func printHelpNavigation(w io.Writer) error {
	resources := make([]string, 0, len(generated.HelpEntries))
	for r := range generated.HelpEntries {
		resources = append(resources, r)
	}
	sort.Strings(resources)

	tx := make([]string, 0, len(generated.TxShortcuts))
	for k := range generated.TxShortcuts {
		tx = append(tx, k)
	}
	sort.Strings(tx)

	lines := []string{
		"Bron CLI — help navigation. Pick a depth:",
		"",
		"  bron --help                          full root help (flags + examples)",
		"  bron <resource> <verb> --help        per-command help (flags + body schema + return shape)",
		"  bron help <topic>                    topic blurb",
		"  bron help <resource> <verb> --schema per-command JSON schema (machine-readable)",
		"  bron help --schema                   full CLI schema (every command, every type)",
		"",
		"Topics (`bron help <topic>`):",
		"  " + strings.Join(topicNames(), ", "),
		"",
		"Resources (`bron <resource> --help`):",
		"  " + strings.Join(resources, ", "),
		"",
		"Tx shortcuts (`bron tx <type> --help`):",
		"  " + strings.Join(tx, ", "),
	}
	for _, l := range lines {
		if _, err := fmt.Fprintln(w, l); err != nil {
			return err
		}
	}
	return nil
}

// walkAPICommands invokes fn on every cobra command tagged with GroupID="api"
// (resource cmds and their verb children, plus the `tx <type>` shortcuts).
// `tx` itself has GroupID="api", so its children are picked up by the
// parent-GroupID check — no need to special-case the parent's name.
func walkAPICommands(root *cobra.Command, fn func(*cobra.Command)) {
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		if c.GroupID == "api" || (c.Parent() != nil && c.Parent().GroupID == "api") {
			fn(c)
		}
		for _, child := range c.Commands() {
			walk(child)
		}
	}
	for _, child := range root.Commands() {
		walk(child)
	}
}

// resourceVerbFor maps an api-group cobra cmd to (resource, verb) for schema
// lookup. Handles bare verb commands (`bron tx get`), `bron tx <type>` create
// shortcuts (collapse to `tx create`), and resource commands with a default
// verb (`bron accounts` → list).
func resourceVerbFor(cmd *cobra.Command) (string, string, bool) {
	if cmd == nil {
		return "", "", false
	}
	parent := cmd.Parent()
	if parent != nil && parent.Name() == "tx" {
		if _, isShortcut := generated.TxShortcuts[cmd.Name()]; isShortcut {
			return "tx", "create", true
		}
		return "tx", cmd.Name(), true
	}
	if parent != nil && parent.GroupID == "api" {
		return parent.Name(), cmd.Name(), true
	}
	if cmd.GroupID == "api" {
		return cmd.Name(), "list", true
	}
	return "", "", false
}

// applyEmbed routes each token from --embed to a matching includeXxx flag on
// the command. Tokens are kebab-case and get camelised: "permission-groups" →
// "includePermissionGroups". Unknown tokens print a stderr warning naming the
// missing flag — silent no-ops are hard to debug for typos like "settngs".
//
// `prices` and `assets` are CLI-only tokens (no backend includeXxx flag yet);
// each triggers a post-process orchestrator on its specific command. Skipped
// silently for any other command. cligen guards at gen-time against an
// `includeXxx` query param producing a colliding token, so the hand-wired and
// generated paths can never both match the same token.
func applyEmbed(cmd *cobra.Command, embed string) {
	if embed == "" {
		return
	}
	for _, tok := range strings.Split(embed, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		if tok == "prices" {
			if r, v, ok := resourceVerbFor(cmd); ok && r == "balances" && v == "list" {
				continue
			}
		}
		if tok == "assets" {
			if r, v, ok := resourceVerbFor(cmd); ok && r == "tx" && v == "list" {
				continue
			}
		}
		var camel strings.Builder
		upNext := true
		for _, r := range tok {
			if r == '-' || r == '_' {
				upNext = true
				continue
			}
			if upNext && r >= 'a' && r <= 'z' {
				camel.WriteRune(r - 'a' + 'A')
			} else {
				camel.WriteRune(r)
			}
			upNext = false
		}
		if camel.Len() == 0 {
			fmt.Fprintf(os.Stderr, "warning: --embed token %q has no alphanumeric content\n", tok)
			continue
		}
		flagName := "include" + camel.String()
		if f := cmd.Flags().Lookup(flagName); f != nil {
			_ = cmd.Flags().Set(flagName, "true")
			continue
		}
		fmt.Fprintf(os.Stderr, "warning: --embed token %q has no matching --%s flag on `bron %s`\n", tok, flagName, cmd.CommandPath())
	}
}

// embedHasToken returns true when the global --embed list contains tok.
func embedHasToken(embed, tok string) bool {
	for _, t := range strings.Split(embed, ",") {
		if strings.TrimSpace(t) == tok {
			return true
		}
	}
	return false
}

func lookupEntry(resource, verb string) (generated.HelpEntry, bool) {
	verbs, ok := generated.HelpEntries[resource]
	if !ok {
		return generated.HelpEntry{}, false
	}
	e, ok := verbs[verb]
	return e, ok
}

// exitCodeForStatus maps HTTP status onto the CLI's stable exit-code contract:
// 3 = unauthorised (401/403), 4 = not found (404/410), 5 = bad request
// (400/422), 6 = conflict (409), 7 = rate limited (429), 8 = server (5xx),
// 1 = anything else. Documented in `bron help errors`.
func exitCodeForStatus(status int) int {
	switch {
	case status == 401, status == 403:
		return 3
	case status == 404, status == 410:
		return 4
	case status == 400, status == 422:
		return 5
	case status == 409:
		return 6
	case status == 429:
		return 7
	case status >= 500:
		return 8
	default:
		return 1
	}
}
