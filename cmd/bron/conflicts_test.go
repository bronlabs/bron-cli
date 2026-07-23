package main

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/bronlabs/bron-api-toolkit/catalog"
	"github.com/bronlabs/bron-cli/internal/client"
)

func TestRealCatalogHasNoConflicts(t *testing.T) {
	if err := checkTxShortcutConflicts(catalog.HelpEntries, catalog.TxShortcuts, reservedTxNames); err != nil {
		t.Fatalf("real catalog tx-shortcut conflict: %v", err)
	}
	if err := checkEmbedTokenConflicts(catalog.HelpEntries, reservedEmbedTokens); err != nil {
		t.Fatalf("real catalog embed-token conflict: %v", err)
	}
}

func TestTxShortcutConflictDetected(t *testing.T) {
	entries := map[string]map[string]catalog.HelpEntry{
		"tx": {"list": {Method: "GET"}},
	}
	shortcuts := map[string]catalog.TxShortcut{"subscribe": {}}
	err := checkTxShortcutConflicts(entries, shortcuts, reservedTxNames)
	if err == nil || !strings.Contains(err.Error(), "subscribe") {
		t.Fatalf("expected a subscribe collision error, got: %v", err)
	}
}

func TestTxShortcutCollidesWithGeneratedVerb(t *testing.T) {
	entries := map[string]map[string]catalog.HelpEntry{
		"tx": {"cancel": {Method: "POST"}},
	}
	shortcuts := map[string]catalog.TxShortcut{"cancel": {}}
	if err := checkTxShortcutConflicts(entries, shortcuts, reservedTxNames); err == nil {
		t.Fatal("expected a collision between the `cancel` verb and a `cancel` transactionType")
	}
}

func TestEmbedTokenConflictDetected(t *testing.T) {
	entries := map[string]map[string]catalog.HelpEntry{
		"balances": {"list": {Method: "GET", QueryParams: []catalog.HelpQueryParam{{Name: "includePrices"}}}},
	}
	err := checkEmbedTokenConflicts(entries, reservedEmbedTokens)
	if err == nil || !strings.Contains(err.Error(), "prices") {
		t.Fatalf("expected a prices embed-token collision error, got: %v", err)
	}
}

func TestBuiltTreeAttachesTxShortcuts(t *testing.T) {
	root := &cobra.Command{Use: "bron"}
	root.AddGroup(&cobra.Group{ID: "api", Title: "API commands:"})
	registerAPICommands(root, func() (*client.Client, error) { return nil, nil })

	tx := childByName(root, "tx")
	if tx == nil {
		t.Fatal("tx resource not attached")
	}
	if childByName(tx, "withdrawal") == nil {
		t.Fatal("tx withdrawal shortcut not attached")
	}
	dryRun := childByName(tx, "dry-run")
	if dryRun == nil {
		t.Fatal("tx dry-run not attached")
	}
	if childByName(dryRun, "withdrawal") == nil {
		t.Fatal("tx dry-run withdrawal shortcut not attached")
	}
}

func childByName(parent *cobra.Command, name string) *cobra.Command {
	for _, c := range parent.Commands() {
		if c.Name() == name {
			return c
		}
	}
	return nil
}
