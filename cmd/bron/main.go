package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	sdkhttp "github.com/bronlabs/bron-sdk-go/sdk/http"
	"github.com/spf13/cobra"

	"github.com/bronlabs/bron-cli/generated"
	"github.com/bronlabs/bron-cli/internal/client"
	"github.com/bronlabs/bron-cli/internal/config"
	"github.com/bronlabs/bron-cli/internal/output"
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
}

const rootLong = `Bron CLI — public API client.

Resources follow the URL: bron <resource> <verb>. The <workspaceId> is implicit (from the active profile config).`

const rootExample = `  bron help
  bron help <resource> <verb> [--output yaml]
  bron help <topic>                   # signing | profiles | output | body | errors | idempotency | agents
  bron help --schema                  # full CLI schema (every command + types) as one JSON

  bron auth keygen --out ~/.config/bron/keys/me.jwk

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

	output.SetDateKeys(collectDateKeysFromSpec())

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
		if cmd.Long != "" {
			fmt.Fprintln(out, cmd.Long)
			fmt.Fprintln(out)
		}
		if cmd.Example != "" {
			fmt.Fprintln(out, "Examples:")
			fmt.Fprintln(out, cmd.Example)
			fmt.Fprintln(out)
		}
		if cmd.HasAvailableLocalFlags() {
			fmt.Fprintln(out, "Flags:")
			fmt.Fprint(out, cmd.LocalFlags().FlagUsages())
			fmt.Fprintln(out)
		}
		fmt.Fprintln(out, `Use "bron <resource> <verb> --help" for any command's flags.`)
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

	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		output.SetFormat(gf.output)
		output.SetQuery(gf.query)
		output.SetColumns(gf.columns)
		output.SetCellMax(gf.cellMax)
		applyEmbed(cmd, gf.embed)
		// --schema on an api command → emit JSON schema instead of running.
		if gf.schema {
			r, v, ok := resourceVerbFor(cmd)
			if !ok {
				return nil
			}
			entry, ok := lookupEntry(r, v)
			if !ok {
				return fmt.Errorf("no schema found for `bron %s %s`", r, v)
			}
			doc, err := buildCommandHelpDoc(r, v, entry)
			if err != nil {
				return err
			}
			// `bron tx <type> --schema` should reflect the discriminator-specific
			// body shape, not the generic CreateTransaction wrapper. Swap the
			// free-form `params` blob with the matching <Type>Params schema and
			// pin transactionType in the body so machine consumers see the
			// concrete request shape.
			if parent := cmd.Parent(); parent != nil && parent.Name() == "tx" {
				if sc, ok := generated.TxShortcuts[cmd.Name()]; ok && sc.ParamsRef != "" {
					specializeTxBody(doc, cmd.Name(), sc.ParamsRef)
					doc["usage"] = "bron tx " + cmd.Name()
					doc["command"] = "tx " + cmd.Name()
				}
			}
			if err := output.Print(doc); err != nil {
				return err
			}
			os.Exit(0)
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

	root.InitDefaultCompletionCmd()
	for _, c := range root.Commands() {
		if c.Name() == "completion" {
			c.GroupID = "system"
			c.AddCommand(newCompletionInstallCmd())
		}
	}

	if err := root.Execute(); err != nil {
		var apiErr *sdkhttp.APIError
		if errors.As(err, &apiErr) {
			fmt.Fprintf(os.Stderr, "error: %s\n", apiErr.Message)
			fmt.Fprintf(os.Stderr, "  status:   %d\n", apiErr.Status)
			if apiErr.Code != "" {
				fmt.Fprintf(os.Stderr, "  code:     %s\n", apiErr.Code)
			}
			if apiErr.RequestID != "" {
				fmt.Fprintf(os.Stderr, "  trace:    %s\n", apiErr.RequestID)
				fmt.Fprintln(os.Stderr, "  (paste the trace ID into a support ticket — the backend logs it as Error ID)")
			}
			os.Exit(exitCodeForStatus(apiErr.Status))
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func buildClient(gf *globalFlags) (*client.Client, error) {
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
		profile.KeyFile = gf.keyFile
	}
	if gf.proxy != "" {
		profile.Proxy = gf.proxy
	}
	return client.New(profile)
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
					fmt.Fprintln(cmd.OutOrStdout(), blurb)
					return nil
				}
				if args[0] == "topics" {
					fmt.Fprintln(cmd.OutOrStdout(), "Available topics (run `bron help <topic>`):")
					for _, n := range topicNames() {
						fmt.Fprintln(cmd.OutOrStdout(), "  "+n)
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

	fmt.Fprintln(w, "Bron CLI — help navigation. Pick a depth:")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  bron --help                          full root help (flags + examples)")
	fmt.Fprintln(w, "  bron <resource> <verb> --help        per-command help (flags + body schema + return shape)")
	fmt.Fprintln(w, "  bron help <topic>                    topic blurb")
	fmt.Fprintln(w, "  bron help <resource> <verb> --schema per-command JSON schema (machine-readable)")
	fmt.Fprintln(w, "  bron help --schema                   full CLI schema (every command, every type)")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Topics (`bron help <topic>`):")
	fmt.Fprintln(w, "  "+strings.Join(topicNames(), ", "))
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Resources (`bron <resource> --help`):")
	fmt.Fprintln(w, "  "+strings.Join(resources, ", "))
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Tx shortcuts (`bron tx <type> --help`):")
	tx := make([]string, 0, len(generated.TxShortcuts))
	for k := range generated.TxShortcuts {
		tx = append(tx, k)
	}
	sort.Strings(tx)
	fmt.Fprintln(w, "  "+strings.Join(tx, ", "))
	return nil
}

// buildFullSchemaDoc returns the embedded OpenAPI 3.1 spec verbatim, decorated
// with bron-CLI specifics under the `x-bron-cli` extension. Verbatim because
// the spec IS the source of truth — reshaping it loses information, and any
// jq/swagger consumer already knows how to walk the standard structure.
//
// Under x-bron-cli we add:
//   - commands[]: each (resource verb) → cli usage line + raw OpenAPI path/method
//   - tx_shortcuts[]: synthetic `bron tx <type>` commands with their params class
func buildFullSchemaDoc() (map[string]interface{}, error) {
	var doc map[string]interface{}
	if err := json.Unmarshal(generated.Spec, &doc); err != nil {
		return nil, fmt.Errorf("decode embedded spec: %w", err)
	}

	type cmdDoc struct {
		Command  string `json:"command"`
		Usage    string `json:"usage"`
		Method   string `json:"method"`
		Path     string `json:"path"`
		PathArgs []string `json:"path_args,omitempty"`
	}
	type txDoc struct {
		Type      string   `json:"type"`
		Usage     string   `json:"usage"`
		ParamsRef string   `json:"params_ref"`
		Params    []string `json:"params"`
		TopFields []string `json:"top_fields"`
	}

	resources := make([]string, 0, len(generated.HelpEntries))
	for r := range generated.HelpEntries {
		resources = append(resources, r)
	}
	sort.Strings(resources)

	commands := make([]cmdDoc, 0, 64)
	for _, r := range resources {
		verbs := make([]string, 0, len(generated.HelpEntries[r]))
		for v := range generated.HelpEntries[r] {
			verbs = append(verbs, v)
		}
		sort.Strings(verbs)
		for _, v := range verbs {
			e := generated.HelpEntries[r][v]
			usage := "bron " + r + " " + v
			for _, p := range e.PathArgs {
				usage += " <" + p + ">"
			}
			commands = append(commands, cmdDoc{
				Command:  r + " " + v,
				Usage:    usage,
				Method:   e.Method,
				Path:     e.Path,
				PathArgs: e.PathArgs,
			})
		}
	}

	txTypes := make([]string, 0, len(generated.TxShortcuts))
	for t := range generated.TxShortcuts {
		txTypes = append(txTypes, t)
	}
	sort.Strings(txTypes)
	tx := make([]txDoc, 0, len(txTypes))
	for _, t := range txTypes {
		s := generated.TxShortcuts[t]
		tx = append(tx, txDoc{
			Type:      t,
			Usage:     "bron tx " + t,
			ParamsRef: s.ParamsRef,
			Params:    s.Params,
			TopFields: s.TopFields,
		})
	}

	doc["x-bron-cli"] = map[string]interface{}{
		"version":       Version,
		"commands":      commands,
		"tx_shortcuts":  tx,
	}
	return doc, nil
}

// appendReturnsHint prints a "Returns:" block at the bottom of help output for
// api verb commands. It lists top-level properties of the response schema with
// type and short description, expanding inline-arrays-of-refs by one level so
// list endpoints show the item shape directly. For `bron tx <type>` it also
// prints a `Body params: <Type>Params` block so the discriminator-specific
// shape is visible alongside the generic CreateTransaction wrapper.
func appendReturnsHint(cmd *cobra.Command) {
	r, v, ok := resourceVerbFor(cmd)
	if !ok {
		return
	}
	entry, ok := lookupEntry(r, v)
	if !ok {
		return
	}
	out := cmd.OutOrStdout()

	// `bron tx <type>` flag list already exposes --params.<...>, but a compact
	// Body block summarises the chosen Params class with types/descriptions/
	// examples in the same shape as Returns — handy reference next to the flags.
	if parent := cmd.Parent(); parent != nil && parent.Name() == "tx" {
		if sc, ok := generated.TxShortcuts[cmd.Name()]; ok && sc.ParamsRef != "" {
			fmt.Fprintf(out, "\nBody params: %s\n", sc.ParamsRef)
			printProps(out, topLevelProps(sc.ParamsRef, false), "  ")
		}
	}

	tokens := append([]string(nil), entry.EmbedTokens...)
	if r == "balances" && v == "list" {
		tokens = append(tokens, "prices")
	}
	if r == "tx" && v == "list" {
		tokens = append(tokens, "assets")
	}
	sort.Strings(tokens)
	if len(tokens) > 0 {
		fmt.Fprintf(out, "\nEmbed tokens (`--embed %s`): %s\n",
			tokens[0], strings.Join(tokens, ", "))
	}

	if entry.ResponseRef == "" {
		fmt.Fprintf(out, "\nFull schema: bron %s %s --schema\n", r, v)
		return
	}

	props := topLevelProps(entry.ResponseRef, true)
	fmt.Fprintf(out, "\nReturns: %s\n", entry.ResponseRef)
	printProps(out, props, "  ")
	fmt.Fprintf(out, "\nFull schema: bron %s %s --schema\n", r, v)
}

type schemaProp struct {
	name, typ, desc, example string
	sub                      []schemaProp // populated for `<Ref>[]` properties (one level deep)
}

func printProps(out io.Writer, props []schemaProp, indent string) {
	if len(props) == 0 {
		return
	}
	nameWidth, typeWidth := 0, 0
	for _, p := range props {
		if l := len(p.name); l > nameWidth {
			nameWidth = l
		}
		if l := len(p.typ); l > typeWidth {
			typeWidth = l
		}
	}
	for _, p := range props {
		desc := p.desc
		if p.example != "" {
			if desc == "" {
				desc = "(e.g. " + p.example + ")"
			} else {
				desc = desc + " (e.g. " + p.example + ")"
			}
		}
		if desc == "" {
			fmt.Fprintf(out, "%s%-*s  %s\n", indent, nameWidth, p.name, p.typ)
		} else {
			fmt.Fprintf(out, "%s%-*s  %-*s  %s\n", indent, nameWidth, p.name, typeWidth, p.typ, desc)
		}
		if len(p.sub) > 0 {
			printProps(out, p.sub, indent+"  ")
		}
	}
}

// topLevelProps returns the top-level properties of a named schema. When
// expandArrays is true, properties shaped `<Ref>[]` get their sub items'
// properties expanded one level deep (used to flesh out list endpoints
// like `transactions: Transaction[]` → show Transaction's fields).
func topLevelProps(name string, expandArrays bool) []schemaProp {
	if name == "" {
		return nil
	}
	v, err := resolveRef(name)
	if err != nil {
		return nil
	}
	m, ok := v.(map[string]interface{})
	if !ok {
		return nil
	}
	props, _ := m["properties"].(map[string]interface{})
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sortStrings(keys)
	out := make([]schemaProp, 0, len(keys))
	for _, k := range keys {
		pm, _ := props[k].(map[string]interface{})
		sp := schemaProp{
			name:    k,
			typ:     schemaTypeLabel(pm),
			desc:    firstLine(asString(pm["description"])),
			example: firstExample(pm),
		}
		if expandArrays {
			if t, _ := pm["type"].(string); t == "array" {
				if items, ok := pm["items"].(map[string]interface{}); ok {
					if ref, _ := items["$ref"].(string); ref != "" {
						inner := strings.TrimPrefix(ref, "#/components/schemas/")
						if inner != name {
							sp.sub = topLevelProps(inner, false)
						}
					}
				}
			}
		}
		out = append(out, sp)
	}
	return out
}

// firstExample picks one example from an OpenAPI schema property. Supports both
// `example: "..."` and `examples: ["...", ...]` shapes. Returns "" when neither
// is set. DocsMacro emits string examples only — the spec never carries
// non-string example shapes today, so we don't bother with json.Marshal fallbacks.
func firstExample(pm map[string]interface{}) string {
	if v, ok := pm["example"].(string); ok && v != "" {
		return v
	}
	if arr, ok := pm["examples"].([]interface{}); ok {
		for _, x := range arr {
			if s, ok := x.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

func schemaTypeLabel(m map[string]interface{}) string {
	if m == nil {
		return ""
	}
	if ref, ok := m["$ref"].(string); ok {
		return strings.TrimPrefix(ref, "#/components/schemas/")
	}
	t, _ := m["type"].(string)
	if t == "array" {
		if items, ok := m["items"].(map[string]interface{}); ok {
			return schemaTypeLabel(items) + "[]"
		}
		return "array"
	}
	if format, _ := m["format"].(string); format != "" && t != "" {
		return t + "(" + format + ")"
	}
	if t == "" {
		if _, ok := m["properties"]; ok {
			return "object"
		}
		return ""
	}
	return t
}

func asString(v interface{}) string {
	s, _ := v.(string)
	return s
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	const max = 120
	if len([]rune(s)) > max {
		r := []rune(s)
		return string(r[:max-1]) + "…"
	}
	return s
}

func sortStrings(s []string) {
	sort.Strings(s)
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
		// Either a real tx verb (`tx get`, `tx list`) or a transactionType
		// create-shortcut (`tx withdrawal`). Shortcuts collapse to `tx create`
		// so help and --schema reflect the underlying endpoint.
		if _, isShortcut := generated.TxShortcuts[cmd.Name()]; isShortcut {
			return "tx", "create", true
		}
		return "tx", cmd.Name(), true
	}
	if parent != nil && parent.GroupID == "api" {
		return parent.Name(), cmd.Name(), true
	}
	if cmd.GroupID == "api" {
		// Resource cmd (e.g. `bron accounts` aliased to list).
		return cmd.Name(), "list", true
	}
	return "", "", false
}

// applyEmbed routes each token from --embed to a matching includeXxx flag on
// the command. Tokens are kebab-case and get camelised: "permission-groups" →
// "includePermissionGroups". Unknown tokens print a stderr warning naming the
// missing flag — silent no-ops are hard to debug for typos like "settngs".
//
// `prices` is a CLI-only token (no backend includeXxx flag yet); on
// `bron balances list` it triggers a post-process that fetches asset prices
// and merges them into the response. Skipped silently for any other command.
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

func buildSchemaDoc(entry generated.HelpEntry) (map[string]interface{}, error) {
	body, err := resolveRef(entry.BodyRef)
	if err != nil {
		return nil, err
	}
	resp, err := resolveRef(entry.ResponseRef)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"method":   entry.Method,
		"path":     entry.Path,
		"body":     body,
		"response": resp,
		"query":    queryFlagDocs(entry.QueryParams),
	}, nil
}

// buildCommandHelpDoc emits a valid OpenAPI 3.1 fragment scoped to a single
// command. Strict spec shape so agents/tools (jq, swagger viewer, OpenAPI
// validators) can navigate it the same way they navigate the project's full
// `bron-open-api-public.json`. Bron-CLI extras (the human command, extracted
// permissions) live under the `x-bron-cli` extension namespace so the doc
// stays valid OpenAPI.
//
// The components.schemas map is the transitive closure of refs reachable from
// the operation — keeps the per-command payload small while still letting
// consumers resolve every $ref locally without a second fetch.
func buildCommandHelpDoc(resource, verb string, entry generated.HelpEntry) (map[string]interface{}, error) {
	usage := "bron " + resource + " " + verb
	for _, p := range entry.PathArgs {
		usage += " <" + p + ">"
	}

	op, err := lookupOperation(entry.Method, entry.Path)
	if err != nil {
		return nil, err
	}

	description := ""
	permissions := []string{}
	if d, ok := op["description"].(string); ok {
		description, permissions = stripPermissions(d)
	}
	if description != "" {
		op["description"] = description
	} else {
		delete(op, "description")
	}

	schemas, err := closureSchemas(op)
	if err != nil {
		return nil, err
	}

	doc := map[string]interface{}{
		"openapi": "3.1.0",
		"info": map[string]interface{}{
			"title":   usage,
			"version": Version,
		},
		"paths": map[string]interface{}{
			entry.Path: map[string]interface{}{
				strings.ToLower(entry.Method): op,
			},
		},
		"components": map[string]interface{}{
			"schemas": schemas,
		},
		"x-bron-cli": map[string]interface{}{
			"command": resource + " " + verb,
			"usage":   usage,
		},
	}
	if len(permissions) > 0 {
		doc["x-bron-cli"].(map[string]interface{})["permissions"] = permissions
	}
	return doc, nil
}

// closureSchemas walks every $ref reachable from root and returns the named
// component schemas it touches, transitively. Empty map is fine — used to
// keep the per-command openapi fragment self-contained without dumping the
// whole 250KB schema universe.
func closureSchemas(root interface{}) (map[string]interface{}, error) {
	var allSchemas struct {
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(generated.Spec, &allSchemas); err != nil {
		return nil, fmt.Errorf("decode spec components: %w", err)
	}
	out := map[string]interface{}{}
	queue := collectRefs(root)
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if _, seen := out[name]; seen {
			continue
		}
		raw, ok := allSchemas.Components.Schemas[name]
		if !ok {
			continue
		}
		var v interface{}
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("decode schema %q: %w", name, err)
		}
		out[name] = v
		queue = append(queue, collectRefs(v)...)
	}
	return out, nil
}

// collectRefs walks v and returns every component name reachable through
// `$ref: #/components/schemas/<Name>`. Order is breadth-first, duplicates
// allowed — caller dedupes via the visited map.
func collectRefs(v interface{}) []string {
	var out []string
	var walk func(node interface{})
	walk = func(node interface{}) {
		switch t := node.(type) {
		case map[string]interface{}:
			if ref, ok := t["$ref"].(string); ok {
				if name := strings.TrimPrefix(ref, "#/components/schemas/"); name != ref {
					out = append(out, name)
				}
			}
			for _, child := range t {
				walk(child)
			}
		case []interface{}:
			for _, item := range t {
				walk(item)
			}
		}
	}
	walk(v)
	return out
}

// collectDateKeysFromSpec scans the embedded OpenAPI spec for property names
// whose schema declares `format: "date-time-millis"` and returns them as a set.
// Output formatters use the set to humanize epoch-millis values to ISO-8601 UTC.
//
// One level of properties per top-level component schema is sufficient:
// `@EpochMillis` is applied directly on Long fields in datamodel DTOs, each
// DTO becomes its own component, and timestamp field names (createdAt,
// expiresAt, etc.) are unique enough across the API that name-only matching
// has no false positives. If a future change nests EpochMillis fields deeper
// (e.g. inside an inline object property), expand this scan.
func collectDateKeysFromSpec() map[string]bool {
	keys := map[string]bool{}
	var spec struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]struct {
					Format string `json:"format"`
					Items  struct {
						Format string `json:"format"`
					} `json:"items"`
				} `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(generated.Spec, &spec); err != nil {
		return keys
	}
	for _, schema := range spec.Components.Schemas {
		for name, prop := range schema.Properties {
			if prop.Format == "date-time-millis" || prop.Items.Format == "date-time-millis" {
				keys[name] = true
			}
		}
	}
	return keys
}

// specializeTxBody mutates the schema doc for `bron tx <type> --schema` so the
// body reflects the specific transactionType — pin the discriminator and
// replace the free-form `params: ObjectNode` blob with the matching
// <Type>Params schema. Best-effort; leaves doc untouched if anything is
// missing or shaped unexpectedly.
func specializeTxBody(doc map[string]interface{}, txType, paramsRef string) {
	body, ok := doc["body"].(map[string]interface{})
	if !ok {
		return
	}
	props, ok := body["properties"].(map[string]interface{})
	if !ok {
		return
	}
	props["transactionType"] = map[string]interface{}{
		"type":    "string",
		"enum":    []interface{}{txType},
		"example": txType,
	}
	paramsSchema, err := resolveRef(paramsRef)
	if err == nil && paramsSchema != nil {
		props["params"] = paramsSchema
	}
}

// lookupOperation pulls the operation node for a (method, path) pair from the
// embedded spec.
func lookupOperation(method, path string) (map[string]interface{}, error) {
	var spec struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(generated.Spec, &spec); err != nil {
		return nil, fmt.Errorf("parse spec: %w", err)
	}
	pathItem, ok := spec.Paths[path]
	if !ok {
		return nil, fmt.Errorf("path %q not found in spec", path)
	}
	raw, ok := pathItem[strings.ToLower(method)]
	if !ok {
		return nil, fmt.Errorf("operation %s %s not found in spec", method, path)
	}
	var op map[string]interface{}
	if err := json.Unmarshal(raw, &op); err != nil {
		return nil, fmt.Errorf("decode operation %s %s: %w", method, path, err)
	}
	return op, nil
}

// stripPermissions extracts trailing "<sup>API Key permissions: A, B</sup>"
// from a description and returns (cleanedDescription, permissionsList).
func stripPermissions(desc string) (string, []string) {
	const tagOpen = "<sup>API Key permissions: "
	const tagClose = "</sup>"
	i := strings.Index(desc, tagOpen)
	if i < 0 {
		return strings.TrimSpace(desc), nil
	}
	j := strings.Index(desc[i:], tagClose)
	if j < 0 {
		return strings.TrimSpace(desc), nil
	}
	perms := desc[i+len(tagOpen) : i+j]
	clean := strings.TrimSpace(desc[:i])
	parts := strings.Split(perms, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return clean, out
}

// splitParams classifies an OpenAPI parameters array into (path, query) and
// drops the implicit `workspaceId` (always set by the CLI from the profile).
func splitParams(raw interface{}) (path, query []map[string]interface{}) {
	arr, ok := raw.([]interface{})
	if !ok {
		return nil, nil
	}
	for _, p := range arr {
		m, ok := p.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := m["name"].(string)
		in, _ := m["in"].(string)
		if in == "path" && name == "workspaceId" {
			continue
		}
		entry := map[string]interface{}{"name": name, "required": m["required"] == true}
		if d, ok := m["description"].(string); ok && d != "" {
			entry["description"] = d
		}
		if s, ok := m["schema"]; ok {
			entry["schema"] = s
		}
		switch in {
		case "path":
			path = append(path, entry)
		case "query":
			query = append(query, entry)
		}
	}
	return path, query
}

// buildResponses turns the OpenAPI responses map into {status -> {description, schema}}.
func buildResponses(raw interface{}) (map[string]interface{}, error) {
	out := map[string]interface{}{}
	m, ok := raw.(map[string]interface{})
	if !ok {
		return out, nil
	}
	for status, node := range m {
		nm, ok := node.(map[string]interface{})
		if !ok {
			continue
		}
		entry := map[string]interface{}{}
		if d, ok := nm["description"].(string); ok && d != "" {
			entry["description"] = d
		}
		if content, ok := nm["content"].(map[string]interface{}); ok {
			if appJSON, ok := content["application/json"].(map[string]interface{}); ok {
				if schema, ok := appJSON["schema"]; ok {
					entry["schema"] = schema
				}
			}
		}
		out[status] = entry
	}
	return out, nil
}

func resolveRef(name string) (interface{}, error) {
	if name == "" {
		return nil, nil
	}
	var spec struct {
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(generated.Spec, &spec); err != nil {
		return nil, fmt.Errorf("parse spec: %w", err)
	}
	raw, ok := spec.Components.Schemas[name]
	if !ok {
		return nil, fmt.Errorf("schema %q not found in spec", name)
	}
	var v interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("decode schema %q: %w", name, err)
	}
	return v, nil
}

func queryFlagDocs(params []generated.HelpQueryParam) []map[string]interface{} {
	if len(params) == 0 {
		return nil
	}
	out := make([]map[string]interface{}, len(params))
	for i, p := range params {
		out[i] = map[string]interface{}{"name": p.Name, "required": p.Required}
	}
	return out
}

// exitCodeForStatus maps HTTP status onto the CLI's stable exit-code contract:
// 3 = unauthorised (401/403), 4 = not found (404), 5 = bad request (400),
// 6 = conflict (409), 7 = rate limited (429), 8 = server (5xx), 1 = anything
// else. Documented in `bron help errors`.
func exitCodeForStatus(status int) int {
	switch {
	case status == 401, status == 403:
		return 3
	case status == 404:
		return 4
	case status == 400:
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
