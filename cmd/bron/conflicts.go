package main

import (
	"fmt"
	"strings"

	"github.com/bronlabs/bron-api-toolkit/catalog"
)

// reservedTxNames are hand-written children of `bron tx` (cmd/bron/subscribe.go).
// cobra's AddCommand does not error on a duplicate name, so a future spec adding
// a transactionType that collides with one would silently shadow the hand
// command — checkTxShortcutConflicts fails the build (via test) instead.
var reservedTxNames = []string{"subscribe"}

// reservedEmbedTokens are the hand-wired CLI-side `--embed` orchestrators in
// cmd/bron/{balances_prices,tx_assets}.go. If the spec ever exposes an
// includeXxx query param that camelises to one of these tokens, applyEmbed
// would route the same token through both the orchestrator and the built
// --includeXxx flag, double-firing — checkEmbedTokenConflicts guards against it.
var reservedEmbedTokens = []struct{ resource, verb, token string }{
	{"balances", "list", "prices"},
	{"tx", "list", "assets"},
}

// checkTxShortcutConflicts fails when a tx-shortcut key collides with a
// `bron tx <verb>` command or a hand-written reserved name.
func checkTxShortcutConflicts(entries map[string]map[string]catalog.HelpEntry, shortcuts map[string]catalog.TxShortcut, reserved []string) error {
	taken := map[string]bool{}
	for verb := range entries["tx"] {
		taken[verb] = true
	}
	for _, name := range reserved {
		taken[name] = true
	}
	for key := range shortcuts {
		if taken[key] {
			return fmt.Errorf("transactionType %q collides with a `bron tx %s` command — a spec bump would silently shadow the hand command; rename the verb or the transactionType", key, key)
		}
	}
	return nil
}

// checkEmbedTokenConflicts fails when an includeXxx query param on a hand-wired
// embed endpoint camelises to the orchestrator's token.
func checkEmbedTokenConflicts(entries map[string]map[string]catalog.HelpEntry, reserved []struct{ resource, verb, token string }) error {
	for _, rsv := range reserved {
		entry, ok := entries[rsv.resource][rsv.verb]
		if !ok {
			continue
		}
		for _, q := range entry.QueryParams {
			if tok, ok := embedTokenFromInclude(q.Name); ok && tok == rsv.token {
				return fmt.Errorf("query param --%s on `bron %s %s` camelises to embed token %q which is hand-wired in cmd/bron/ — the built flag and the orchestrator would double-fire; drop the orchestrator or rename one",
					q.Name, rsv.resource, rsv.verb, rsv.token)
			}
		}
	}
	return nil
}

// embedTokenFromInclude converts an `includeXxx` query-param name to the
// kebab-case token the global `--embed` flag accepts ("includePermissionGroups"
// → "permission-groups"). Returns false for names that aren't include-prefixed.
func embedTokenFromInclude(flag string) (string, bool) {
	const prefix = "include"
	if !strings.HasPrefix(flag, prefix) || len(flag) == len(prefix) {
		return "", false
	}
	rest := flag[len(prefix):]
	if rest[0] < 'A' || rest[0] > 'Z' {
		return "", false
	}
	var b strings.Builder
	for i, r := range rest {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r - 'A' + 'a')
		} else {
			b.WriteRune(r)
		}
	}
	return b.String(), true
}
