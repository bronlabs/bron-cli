package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

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
	output    string
	query     string
}

const rootLong = `Bron CLI — public API client. Generated from the OpenAPI spec, talks to the API
over JWT-signed HTTPS.

Quick start:
  bron auth keygen --out ~/.config/bron/keys/me.jwk
  bron config init --workspace <wsId> --key-file ~/.config/bron/keys/me.jwk --set-active
  bron transactions list --limit 5 --output table

Resources follow the URL: ` + "`bron <resource> <verb>`" + ` (e.g. ` + "`bron transactions list`" + `,
` + "`bron address-book create`" + `). Use ` + "`bron tx <type>`" + ` as a shortcut for creating a
transaction of a given type. Workspace is implicit (from the active profile).

Output formats: --output json|yaml|jsonl|table. Filter with --query '.path[*].field'.
Body composition: --file <path|->, --json '{...}', or per-field --<a>.<b> flags.

See ` + "`bron help <resource> <verb>`" + ` for the schema dump of any command.`

func main() {
	gf := &globalFlags{}

	root := &cobra.Command{
		Use:           "bron <resource> <verb> [<id>...] [flags]",
		Short:         "Bron CLI — public API client",
		Long:          rootLong,
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddGroup(
		&cobra.Group{ID: "api", Title: "API commands:"},
		&cobra.Group{ID: "system", Title: "System commands:"},
	)
	root.PersistentFlags().StringVar(&gf.profile, "profile", "", "config profile name")
	root.PersistentFlags().StringVar(&gf.workspace, "workspace", "", "workspace id (overrides profile)")
	root.PersistentFlags().StringVar(&gf.baseURL, "base-url", "", "API base URL (overrides profile)")
	root.PersistentFlags().StringVar(&gf.keyFile, "key-file", "", "path to JWK private key (overrides profile)")
	root.PersistentFlags().StringVar(&gf.output, "output", "", "output format: table|json|yaml|jsonl (default json)")
	root.PersistentFlags().StringVar(&gf.query, "query", "", "JSONPath subset filter, e.g. .transactions[*].transactionId")

	root.PersistentPreRun = func(cmd *cobra.Command, args []string) {
		output.SetFormat(gf.output)
		output.SetQuery(gf.query)
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
	return client.New(profile)
}

// newHelpCmd replaces cobra's default help command. Two modes:
//
//	bron help                          — print root usage (cobra default behavior)
//	bron help <resource>               — print resource subcommands (cobra default)
//	bron help <resource> <verb>        — agent-friendly help: usage + flags + body/response
//	                                     schemas in the active --output format (default json).
func newHelpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "help [resource] [verb]",
		Short: "Help about any command (with agent-friendly schema dump for `help <resource> <verb>`)",
		Long: "Without arguments — prints CLI usage.\n" +
			"With <resource> <verb> — prints usage signature, flags, body schema, response schema\n" +
			"in the format selected by --output (json default, yaml supported).",
		DisableFlagParsing: false,
		RunE: func(cmd *cobra.Command, args []string) error {
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
			schemas, err := buildSchemaDoc(entry)
			if err != nil {
				return err
			}
			usage := "bron " + args[0] + " " + args[1]
			for _, p := range entry.PathArgs {
				usage += " <" + p + ">"
			}
			return output.Print(map[string]interface{}{
				"usage":   usage,
				"method":  entry.Method,
				"path":    entry.Path,
				"flags":   queryFlagDocs(entry.QueryParams),
				"schemas": schemas,
			})
		},
	}
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
