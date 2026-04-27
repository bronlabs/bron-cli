package main

import (
	"encoding/json"
	"errors"
	"fmt"
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

  bron accounts list --accountTypes vault --limit 50
  bron accounts get <accountId>

  bron balances list --accountId <accountId> --assetId 5000 --networkId ETH --nonEmpty true

  bron transactions list --transactionStatuses waiting-approval,signing --limit 50
  bron transactions list --transactionTypes withdrawal,allowance --createdAtFrom 2026-04-01
  bron transactions get    <transactionId>
  bron transactions events <transactionId>

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

  bron transactions approve                <transactionId>
  bron transactions decline                <transactionId>
  bron transactions cancel                 <transactionId>
  bron transactions create-signing-request <transactionId>
  bron transactions accept-deposit-offer   <transactionId>
  bron transactions reject-outgoing-offer  <transactionId>

  # Lower-level — when no "tx <type>" shortcut fits or you want full control:
  bron transactions create \
    --transactionType withdrawal \
    --accountId <accountId> \
    --externalId <idempotencyKey> \
    --params.amount=100 \
    --params.assetId=5000 \
    --params.networkId=ETH \
    --params.toAddress=<address>

  bron transactions dry-run     --file ./tx.json
  bron transactions bulk-create --file ./batch.json

  bron transactions list --output yaml
  bron transactions list --output table --columns transactionId,status,transactionType,createdAt
  bron transactions list --output table --query '.transactions[*]'
  bron transactions get <transactionId> --query '.status'

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

	root.PersistentPreRun = func(cmd *cobra.Command, args []string) {
		output.SetFormat(gf.output)
		output.SetQuery(gf.query)
		output.SetColumns(gf.columns)
	}

	generated.Register(root, func() (*client.Client, error) { return buildClient(gf) })

	helpCmd := newHelpCmd()
	helpCmd.GroupID = "system"
	root.SetHelpCommand(helpCmd)

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
			fmt.Fprintf(os.Stderr, "error: %s (status=%d code=%s requestID=%s)\n",
				apiErr.Message, apiErr.Status, apiErr.Code, apiErr.RequestID)
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
//	bron help                          — print root usage (cobra default behavior)
//	bron help <resource>               — print resource subcommands (cobra default)
//	bron help <resource> <verb>        — agent-friendly help for one command: usage + flags +
//	                                     body schema + response schema (--output json|yaml).
//	bron help --schema                 — dump the full CLI schema (every command + tx shortcut +
//	                                     referenced types) as a single document for agents.
func newHelpCmd() *cobra.Command {
	var dumpSpec bool
	cmd := &cobra.Command{
		Use:   "help [resource] [verb]",
		Short: "Help about any command (with agent-friendly schema dump for `help <resource> <verb>`)",
		Long: "Without arguments — prints CLI usage.\n" +
			"With <resource> <verb> — prints usage signature, flags, body schema, response schema\n" +
			"in the format selected by --output (json default, yaml supported).\n" +
			"With --schema — dumps the full CLI schema (commands, tx shortcuts, referenced types).",
		DisableFlagParsing: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			if dumpSpec {
				doc, err := buildFullSchemaDoc()
				if err != nil {
					return err
				}
				return output.Print(doc)
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
			if len(args) < 2 {
				target, _, err := cmd.Root().Find(args)
				if err != nil || target == nil {
					return cmd.Root().Help()
				}
				return target.Help()
			}
			entry, ok := lookupEntry(args[0], args[1])
			if !ok {
				return fmt.Errorf("unknown command: bron %s %s", args[0], args[1])
			}
			doc, err := buildCommandHelpDoc(args[0], args[1], entry)
			if err != nil {
				return err
			}
			return output.Print(doc)
		},
	}
	cmd.Flags().BoolVar(&dumpSpec, "schema", false, "dump the full CLI schema (every command + tx shortcut + types) — machine-readable")
	return cmd
}

// buildFullSchemaDoc produces a CLI-shaped schema document for agent consumption.
// Layout:
//
//	{
//	  "version":  "0.1.1",
//	  "commands": [{usage, command, method, path, path_args, query_params, body_ref, response_ref}, ...],
//	  "tx":       [{type, usage, params_ref, params, top_fields}, ...],
//	  "schemas":  { components.schemas from the embedded OpenAPI }
//	}
func buildFullSchemaDoc() (map[string]interface{}, error) {
	type cmdDoc struct {
		Command     string                       `json:"command"`
		Usage       string                       `json:"usage"`
		Method      string                       `json:"method"`
		Path        string                       `json:"path"`
		PathArgs    []string                     `json:"path_args,omitempty"`
		QueryParams []generated.HelpQueryParam   `json:"query_params,omitempty"`
		BodyRef     string                       `json:"body_ref,omitempty"`
		ResponseRef string                       `json:"response_ref,omitempty"`
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
				Command:     r + " " + v,
				Usage:       usage,
				Method:      e.Method,
				Path:        e.Path,
				PathArgs:    e.PathArgs,
				QueryParams: e.QueryParams,
				BodyRef:     e.BodyRef,
				ResponseRef: e.ResponseRef,
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

	var spec struct {
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(generated.Spec, &spec); err != nil {
		return nil, fmt.Errorf("decode embedded spec: %w", err)
	}
	schemas := make(map[string]interface{}, len(spec.Components.Schemas))
	for name, raw := range spec.Components.Schemas {
		var v interface{}
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("decode schema %q: %w", name, err)
		}
		schemas[name] = v
	}

	return map[string]interface{}{
		"version":  Version,
		"commands": commands,
		"tx":       tx,
		"schemas":  schemas,
	}, nil
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

// buildCommandHelpDoc returns a comprehensive per-command document for agents:
// usage, description, permissions, all path/query parameters with their schemas,
// the body schema, and every response (status -> schema). Resolved on the fly
// from the embedded OpenAPI spec.
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

	pathParams, queryParams := splitParams(op["parameters"])

	body, err := resolveRef(entry.BodyRef)
	if err != nil {
		return nil, err
	}

	responses, err := buildResponses(op["responses"])
	if err != nil {
		return nil, err
	}

	doc := map[string]interface{}{
		"usage":     usage,
		"command":   resource + " " + verb,
		"method":    entry.Method,
		"path":      entry.Path,
		"responses": responses,
	}
	if description != "" {
		doc["description"] = description
	}
	if len(permissions) > 0 {
		doc["permissions"] = permissions
	}
	if len(pathParams) > 0 {
		doc["path_params"] = pathParams
	}
	if len(queryParams) > 0 {
		doc["query_params"] = queryParams
	}
	if body != nil {
		doc["body"] = body
	}
	return doc, nil
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

// exitCodeForStatus maps HTTP status to the exit-code contract from BRO-486.
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
