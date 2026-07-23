package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bronlabs/bron-api-toolkit/body"
	"github.com/bronlabs/bron-api-toolkit/catalog"
	"github.com/bronlabs/bron-api-toolkit/output"
	"github.com/bronlabs/bron-api-toolkit/qparam"
	"github.com/bronlabs/bron-cli/internal/client"
)

// clientProvider builds a configured client on demand.
type clientProvider func() (*client.Client, error)

// registerAPICommands builds the cobra command tree at runtime by walking the
// catalog — the same spec-driven data the MCP tool layer walks — instead of a
// generated source file. One resource group per catalog resource; every verb
// becomes a subcommand, `list` is promoted as the resource's default, and `tx`
// additionally carries the per-transactionType create + dry-run shortcuts.
func registerAPICommands(root *cobra.Command, provide clientProvider) {
	for _, r := range sortedResources() {
		verbs := catalog.HelpEntries[r]
		res := &cobra.Command{Use: r, Short: resourceShort(r, verbs), GroupID: "api"}
		for _, v := range sortedVerbs(verbs) {
			res.AddCommand(newEndpointCmd(v, verbs[v], provide))
		}
		if r == "tx" {
			addTxShortcuts(res, provide)
		}
		promoteListOrError(res, r, verbs)
		root.AddCommand(res)
	}
}

// resourceShort mirrors the resource one-liner: the verb list keeps zsh
// completion rows unique across resources ("get, list — accounts"), and `tx`
// appends its create-shortcut keys.
func resourceShort(r string, verbs map[string]catalog.HelpEntry) string {
	short := strings.Join(sortedVerbs(verbs), ", ") + " — " + r
	if r == "tx" {
		short += "; create-shortcut: " + strings.Join(sortedTxKeys(), ", ")
	}
	return short
}

func newEndpointCmd(verb string, e catalog.HelpEntry, provide clientProvider) *cobra.Command {
	qvals := map[string]*string{}
	fvals := map[string]*string{}
	var fileFlag, jsonFlag string
	hasBody := e.BodyRef != "" || len(e.BodyFields) > 0

	use := verb
	for _, id := range e.PathArgs {
		use += " <" + id + ">"
	}
	short := e.Summary
	if short == "" {
		short = e.Method
	}

	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			cli, err := provide()
			if err != nil {
				return err
			}
			pathParams := pathParamsFromArgs(e.PathArgs, args)
			var payload interface{}
			if hasBody {
				payload, err = composeBody(e, fvals, fileFlag, jsonFlag)
				if err != nil {
					return err
				}
			}
			query, err := buildQuery(e, qvals)
			if err != nil {
				return err
			}
			var result interface{}
			if err := cli.Do(cmd.Context(), e.Method, e.Path, pathParams, payload, query, &result); err != nil {
				return err
			}
			return output.Print(result)
		},
	}
	if n := len(e.PathArgs); n > 0 {
		cmd.Args = cobra.ExactArgs(n)
	}

	for _, q := range e.QueryParams {
		p := new(string)
		qvals[q.Name] = p
		cmd.Flags().StringVar(p, q.Name, "", flagHelp(q.Type, q.Format, q.Description, q.Enum, q.Required))
	}
	for _, f := range e.BodyFields {
		p := new(string)
		fvals[f.DotPath] = p
		cmd.Flags().StringVar(p, f.DotPath, "", flagHelp(f.Type, f.Format, f.Description, f.Enum, false))
	}
	if hasBody {
		addBodyFileFlags(cmd, &fileFlag, &jsonFlag)
	}
	return cmd
}

// addTxShortcuts folds the per-transactionType create shortcuts into `bron tx`
// (`bron tx withdrawal`, ...) and mirrors them under `bron tx dry-run <type>`,
// which POSTs the same CreateTransaction shape to /transactions/dry-run.
func addTxShortcuts(res *cobra.Command, provide clientProvider) {
	for _, key := range sortedTxKeys() {
		res.AddCommand(newTxShortcutCmd(key, catalog.TxShortcuts[key], provide,
			"create "+key+" transaction", "/workspaces/{workspaceId}/transactions"))
	}
	var dryRun *cobra.Command
	for _, c := range res.Commands() {
		if c.Name() == "dry-run" {
			dryRun = c
			break
		}
	}
	if dryRun == nil {
		return
	}
	for _, key := range sortedTxKeys() {
		dryRun.AddCommand(newTxShortcutCmd(key, catalog.TxShortcuts[key], provide,
			"dry-run "+key+" transaction", "/workspaces/{workspaceId}/transactions/dry-run"))
	}
}

func newTxShortcutCmd(key string, sc catalog.TxShortcut, provide clientProvider, short, path string) *cobra.Command {
	topVals := map[string]*string{}
	paramVals := map[string]*string{}
	var fileFlag, jsonFlag string

	cmd := &cobra.Command{
		Use:   key,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			cli, err := provide()
			if err != nil {
				return err
			}
			baseline, err := body.Parse(fileFlag, jsonFlag)
			if err != nil {
				return err
			}
			fields := map[string]string{"transactionType": key}
			for _, f := range sc.TopFieldDefs {
				fields[f.DotPath] = *topVals[f.DotPath]
			}
			for _, p := range sc.ParamDefs {
				fields["params."+p.DotPath] = *paramVals[p.DotPath]
			}
			payload, err := body.Compose(baseline, fields)
			if err != nil {
				return err
			}
			if err := qparam.CoerceBodyDates(payload); err != nil {
				return err
			}
			var pathParams map[string]string
			var query interface{}
			var result interface{}
			if err := cli.Do(cmd.Context(), "POST", path, pathParams, payload, query, &result); err != nil {
				return err
			}
			return output.Print(result)
		},
	}

	for _, f := range sc.TopFieldDefs {
		p := new(string)
		topVals[f.DotPath] = p
		cmd.Flags().StringVar(p, f.DotPath, "", flagHelp(f.Type, f.Format, f.Description, f.Enum, false))
	}
	for _, pf := range sc.ParamDefs {
		p := new(string)
		paramVals[pf.DotPath] = p
		cmd.Flags().StringVar(p, "params."+pf.DotPath, "", flagHelp(pf.Type, pf.Format, pf.Description, pf.Enum, false))
	}
	addBodyFileFlags(cmd, &fileFlag, &jsonFlag)
	return cmd
}

func addBodyFileFlags(cmd *cobra.Command, fileFlag, jsonFlag *string) {
	cmd.Flags().StringVar(fileFlag, "file", "", "read request body from file path ('-' for stdin)")
	cmd.Flags().StringVar(jsonFlag, "json", "", "inline request body as a JSON string")
	cmd.MarkFlagsMutuallyExclusive("file", "json")
}

// promoteListOrError makes `bron <resource>` (no verb) default to the `list`
// verb when present, otherwise error with the available actions.
func promoteListOrError(res *cobra.Command, r string, verbs map[string]catalog.HelpEntry) {
	for _, c := range res.Commands() {
		if c.Name() == "list" {
			res.RunE = c.RunE
			res.Args = c.Args
			res.Flags().AddFlagSet(c.Flags())
			return
		}
	}
	names := sortedVerbs(verbs)
	res.RunE = func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf("specify an action: %s (available: %s)", r, strings.Join(names, ", "))
	}
}

func pathParamsFromArgs(names, args []string) map[string]string {
	if len(names) == 0 {
		return nil
	}
	out := make(map[string]string, len(names))
	for i, n := range names {
		out[n] = args[i]
	}
	return out
}

func composeBody(e catalog.HelpEntry, fvals map[string]*string, fileFlag, jsonFlag string) (interface{}, error) {
	baseline, err := body.Parse(fileFlag, jsonFlag)
	if err != nil {
		return nil, err
	}
	fields := make(map[string]string, len(e.BodyFields))
	for _, f := range e.BodyFields {
		fields[f.DotPath] = *fvals[f.DotPath]
	}
	payload, err := body.Compose(baseline, fields)
	if err != nil {
		return nil, err
	}
	if err := qparam.CoerceBodyDates(payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func buildQuery(e catalog.HelpEntry, qvals map[string]*string) (interface{}, error) {
	if len(e.QueryParams) == 0 {
		return nil, nil
	}
	for _, q := range e.QueryParams {
		if len(q.Enum) == 0 {
			continue
		}
		repeat := strings.HasSuffix(q.Type, "[]") || q.Type == "array"
		if err := qparam.ValidateEnum(q.Name, *qvals[q.Name], q.Enum, repeat); err != nil {
			return nil, err
		}
	}
	m := make(map[string]interface{}, len(e.QueryParams))
	for _, q := range e.QueryParams {
		m[q.Name] = *qvals[q.Name]
	}
	return compactQuery(m)
}

// compactQuery drops empty string values and coerces date-shaped parameters
// (createdAtFrom, updatedSince, ...) from ISO-8601 to epoch-millis if needed.
func compactQuery(m map[string]interface{}) (interface{}, error) {
	out := map[string]interface{}{}
	for k, v := range m {
		s, ok := v.(string)
		if !ok || s == "" {
			continue
		}
		coerced, err := qparam.MaybeDate(k, s)
		if err != nil {
			return nil, err
		}
		out[k] = coerced
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// seeDetailsLink matches the redundant `[See details](/enums/Foo)` markdown
// suffix the spec attaches to enum-typed fields; dropped once the actual enum
// values are inlined into the flag help.
var seeDetailsLink = regexp.MustCompile(`\s*\[See details\]\([^)]*\)\s*\.?\s*$`)

// flagHelp builds the cobra usage string:
// "[type<format>] description (enum: a|b|c) (required)". Empty type/format/
// description segments are dropped so trivial cases stay clean.
func flagHelp(t, format, description string, enum []string, required bool) string {
	var prefix string
	switch {
	case t != "" && format != "":
		prefix = "[" + t + "<" + format + ">] "
	case t != "":
		prefix = "[" + t + "] "
	}
	if len(enum) > 0 {
		description = strings.TrimRight(seeDetailsLink.ReplaceAllString(description, ""), " .")
		if description != "" {
			description += " "
		}
		description += "(enum: " + strings.Join(enum, "|") + ")"
	}
	suffix := ""
	if required {
		suffix = " (required)"
	}
	return prefix + description + suffix
}

func sortedResources() []string {
	out := make([]string, 0, len(catalog.HelpEntries))
	for r := range catalog.HelpEntries {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}

func sortedVerbs(verbs map[string]catalog.HelpEntry) []string {
	out := make([]string, 0, len(verbs))
	for v := range verbs {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func sortedTxKeys() []string {
	out := make([]string, 0, len(catalog.TxShortcuts))
	for k := range catalog.TxShortcuts {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
