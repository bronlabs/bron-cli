package main

import "sort"

// topics maps `bron help <topic>` queries to short conceptual blurbs.
// Each blurb is one paragraph + a few bullet points + a developer.bron.org link.
// Keep them short — agents want fast orientation, humans follow the link for depth.
var topics = map[string]string{
	"signing": `Signing — how the CLI authenticates to the Bron API

Every request carries a short-lived JWT signed with your ES256 (P-256) private key.
The signature covers the HTTP method, path, query, body hash, and a fresh timestamp,
so it is bound to that exact call and cannot be replayed.

  • One-shot, interactive (recommended):  bron config init --key-file ~/.config/bron/keys/me.jwk
                                          (CLI generates the JWK if --key-file points to a non-existent path,
                                          writes the private half mode 0600, prints the public half to stdout,
                                          waits for you to paste it into Bron Settings → API keys, then calls
                                          GET /workspaces with the fresh key to auto-resolve workspaceId and
                                          validate the registration in one round-trip.)
  • Explicit (CI / scripts):              bron config init --name <profile> --workspace <wsId> --key-file …
                                          (saves as-is, no auto-discovery — required when stdin is non-TTY.)
  • Register the public JWK in Bron UI    (the binding is by "kid" inside the JWK).
  • Re-print the public JWK any time:     bron config init --name <profile> --key-file <path>   (re-derives + reprints
                                          public half from the existing private JWK on disk, then walks the same
                                          auto-discovery flow — pass --workspace to skip the /workspaces round-trip).
  • The CLI re-signs every request — no token caching, no revocation flow needed.

Key sources, highest precedence first:
  • $BRON_API_KEY        raw JWK bytes — wins over the file paths. Pair with a
                         secret store so the key never lands on disk:
                         BRON_API_KEY=$(op read op://Personal/Bron/private-jwk) bron tx list
                         For MCP: claude mcp add bron --env BRON_API_KEY=op://… -- op run -- bron mcp
  • $BRON_API_KEY_FILE   path override (for CI when you ship a managed file)
  • profile.key_file     ~/.config/bron/config.yaml — interactive default

Reference: https://developer.bron.org/sdk/cli/auth`,

	"profiles": `Profiles — config + env overrides

A profile is a named tuple of (workspace, key_file, proxy, base_url). Config lives at
~/.config/bron/config.yaml (or $XDG_CONFIG_HOME/bron/config.yaml, or $BRON_CONFIG).
Multiple profiles let you switch between environments without retyping flags.

Resolution order (highest precedence first):
  1. --profile / --workspace / --key-file / --proxy / --base-url flags
  2. BRON_PROFILE / BRON_WORKSPACE_ID / BRON_API_KEY / BRON_API_KEY_FILE / BRON_BASE_URL / BRON_PROXY / BRON_CONFIG env vars
     (BRON_API_KEY carries raw JWK bytes and wins over BRON_API_KEY_FILE / key_file —
      pair with "op run" / Vault / sops so the key never lands on disk)
  3. active_profile from YAML
  4. profile named "default"

Common workflow:
  bron config init --key-file ~/.config/bron/keys/me.jwk    # interactive: auto-resolves workspaceId
  bron config use-profile production
  bron config show              # what the HTTP client will see (env applied)
  bron config show --raw        # what is actually written in YAML
  bron config list              # all profiles, with the active one annotated

Behind a corporate proxy?
  bron config set proxy=http://user:pass@host:8080
  # or set HTTPS_PROXY / HTTP_PROXY in the environment — both are honored.

base_url defaults to https://api.bron.org and is hidden from help; override
with --base-url per call, or BRON_BASE_URL=... in the environment.

Reference: https://developer.bron.org/sdk/cli/auth`,

	"output": `Output — formats, queries, columns

Every command prints the API response in one of four formats, optionally filtered
by a small JSONPath subset and/or a column list.

  --output json   (default)   pretty-printed JSON, colored on TTY
  --output yaml               YAML
  --output jsonl              one JSON document per line (good for piping)
  --output table              aligned columns; nested objects collapse to {…} / […N], cells trimmed.
                              *At / *Time fields with epoch-millis values render as ISO UTC.

  --columns id,status,...     comma-separated key list to keep, in the listed order.
                              Works for json / yaml / jsonl / table. For list-shape responses
                              (e.g. {"transactions":[…]}) it narrows each element.
                              Supports dot-paths: --columns transactionId,params.amount,params.assetId
                                — table renders as flat headers ("params.amount"),
                                  json/yaml emit nested objects ({"params":{"amount":…}}).

JSON colors: NO_COLOR=1 disables, FORCE_COLOR=1 forces on.

Heavier transformations: pipe to jq.

  bron tx list --output json | jq '.transactions[] | select(.status=="signed")'

Examples:
  bron tx list --output table --columns transactionId,transactionType,params.amount,params.assetId,createdAt
  bron tx list --output json  --columns transactionId,status,params.amount
  bron tx get <id>            --columns transactionId,status,params

Reference: https://developer.bron.org/sdk/cli/output`,

	"body": `Body composition — for write operations

Two baseline sources (mutually exclusive), then field-flag overlay.

  --file <path>       read body JSON from a file ("-" = stdin)
  --json '{...}'      inline body JSON
  --<field> <value>   per-field flag, e.g. --params.amount=100

Field flags override matching dot-paths in the baseline. Values are JSON-parsed
when possible (numbers, booleans, arrays); otherwise sent as strings.

Examples:
  bron tx withdrawal --file ./tx.json --params.amount=250 --externalId=<idempotencyKey>
  bron tx withdrawal --json '{"accountId":"<accountId>","params":{"amount":100}}'
  cat tx.json | bron tx withdrawal --file -

Reference: https://developer.bron.org/sdk/cli`,

	"errors": `Errors — exit codes and error shape

API errors print a structured envelope on stderr. The HTTP status maps to a
stable exit code:

  error: <human-readable message>
    status: <http-status>
    code:   <STABLE_CODE>
    id:     <correlation-id>

  401 / 403   → exit 3   (not authorized)
  404         → exit 4   (not found)
  400         → exit 5   (bad request)
  409         → exit 6   (conflict)
  429         → exit 7   (rate limited)
  5xx         → exit 8   (server error)
  other       → exit 1

Branch on "code" (stable slug) — never on the human message. Quote "id" when
reporting issues; the SDKs expose the same value as APIError.RequestID, and
the MCP error payload as "requestId".

Enum-typed query flags (--statuses, --transactionTypes, …) are validated
client-side before the request hits the wire. Invalid values fail with
"error: --<flag>: invalid value \"<bad>\" (allowed: …)" and exit 1, no API
call is made.

Reference: https://developer.bron.org/sdk/cli/errors`,

	"idempotency": `Idempotency — externalId for safe retries

Every transaction-creation call accepts an "externalId". It is your client-side
unique key for a single business action. Bron de-duplicates by (workspace, externalId):
retrying the same call with the same externalId returns the existing transaction
instead of creating a new one.

  • Generate it on YOUR side, before the call:   --externalId <idempotencyKey>
  • Persist it before sending the request — survive crashes, network blips, retries.
  • Different attempts of the same logical action: SAME externalId.
  • Different logical actions: DIFFERENT externalId, even on the same account.

Without externalId, retries can double-spend. Always set it for write operations.

Reference: https://developer.bron.org/sdk/cli/cookbook`,

	"addresses": `Addresses — picking a recipient on a withdrawal

Withdrawal-style transactions accept four mutually-exclusive recipient fields under
` + "`params`" + `. Pick the one that fits your use case:

  --params.toAddress=<rawAddress>           on-chain address; you take all responsibility
                                             for the format and the receiving network.
  --params.toAddressBookRecordId=<recordId> use a saved address-book entry. Bron resolves
                                             the actual address + memo + network from the
                                             record, so a typo in --params.toAddress is no
                                             longer possible. List entries with
                                             "bron address-book list".
  --params.toAccountId=<accountId>          internal transfer between two of your accounts.
                                             No on-chain fee path; instant.
  --params.toWorkspaceTag=<tag>             route to another Bron workspace by its tag
                                             (e.g. "treasury-ops" → routes to that workspace).

Examples:
  bron tx withdrawal --accountId <accountId> --externalId <idempotencyKey> \
    --params.amount=100 --params.assetId=5000 --params.networkId=ETH \
    --params.toAddressBookRecordId=<recordId>

  bron tx withdrawal --accountId <accountId> --externalId <idempotencyKey> \
    --params.amount=10  --params.assetId=5000 --params.networkId=ETH \
    --params.toAccountId=<destAccountId>

  bron tx withdrawal --accountId <accountId> --externalId <idempotencyKey> \
    --params.amount=5   --params.assetId=5000 --params.networkId=ETH \
    --params.toWorkspaceTag=<workspaceTag>

Reference: https://developer.bron.org/api-reference/transactions`,

	"agents": `Agents — using bron from LLMs and scripts

Two surfaces:

  • bron mcp           — Model Context Protocol stdio server. The agent calls
                         typed MCP tools directly (bron_tx_list, bron_tx_withdrawal,
                         bron_tx_wait_for_state, …) — same data path as the CLI but
                         no shell quoting, structured errors, native types
                         (booleans / integers / arrays). See "bron help mcp".
  • bash bron <verb>   — classic CLI invocation. Pipeable, JSON output, stable
                         exit codes. Right when there's no MCP host or the
                         agent prefers shelling out.

Machine-friendly switches that work in both modes:

  • bron help --schema                 — full CLI schema (every command, every body/response
                                         type) as JSON. One call, no follow-ups.
  • bron help <resource> <verb>        — per-command JSON: usage + flags + body + response.
  • --output json | yaml | jsonl       — structured output for parsing.
  • --columns dotted.path,…            — projection without piping to jq.
  • Pipe to jq for transformations     — bron tx list --output jsonl | jq ...
  • Stable exit codes                  — see "bron help errors".
  • Idempotent writes via externalId   — see "bron help idempotency".

Recommended agent flow (CLI mode):
  1. Read "bron help --schema" once at session start.
  2. Pick the command + flags you need.
  3. Always supply --externalId for write operations.
  4. Parse --output json; rely on exit codes for branching.

For MCP mode, "tools/list" returns the same surface as --schema; the long-poll
"bron_tx_wait_for_state" tool replaces the bash subscribe + Monitor pattern
for single-tx waits. Pair with the bron-skills (https://github.com/bronlabs/bron-skills)
package for vetted Claude/Cursor/etc. skill packs.

Reference: https://developer.bron.org/sdk/cli/agents`,

	"mcp": `MCP — Model Context Protocol server

The "bron" binary doubles as a stdio MCP server when invoked with the "mcp"
subcommand. Same pattern as "gh mcp" / "stripe mcp": every public-API endpoint
becomes a typed MCP tool the agent can call directly, no shell quoting.

  bron mcp                  # stdio server, foreground (run from your MCP host)
  bron mcp --read-only      # GET endpoints + tx dry-run only — no withdraws

Wire it into your agent host (one command, no hand-editing JSON):

  bron mcp install --target claude-desktop      # edits claude_desktop_config.json
  bron mcp install --target cursor              # edits ~/.cursor/mcp.json
  bron mcp install --target cline               # edits Cline's mcp_settings.json
  bron mcp install --target claude-code         # delegates to "claude mcp add"
  bron mcp install --target cursor --read-only  # register with --read-only baked in
  bron mcp install --target cursor --dry-run    # print planned change, don't write

Or hand-edit the host config:

  Claude Code:        claude mcp add bron -- bron mcp
  Claude Code + 1P:   claude mcp add bron --env BRON_API_KEY='op://Personal/Bron/private-jwk' \
                        -- op run -- bron mcp
  Cursor / Desktop:   add to ~/.cursor/mcp.json or claude_desktop_config.json:
                      {"mcpServers": {"bron": {"command": "bron", "args": ["mcp"]}}}

Authentication is the same as the rest of the CLI — see "bron help signing".

What's exposed:

  • Read endpoints       bron_tx_list, bron_balances_list, bron_workspace_info, …
  • Write endpoints      bron_tx_withdrawal, bron_tx_approve, bron_address_book_create, …
  • Tx shortcuts         bron_tx_<type> for every transactionType (withdrawal, allowance, bridge, …)
  • Long-poll wait       bron_tx_wait_for_state — subscribe to one transactionId,
                         return on first match in expectedStates, or timeout with
                         a continuation hint. Universal across MCP clients.
  • _embedded extras     pass embed: "prices" on bron_balances_list or embed: "assets"
                         on bron_tx_list to fold related entities into _embedded
                         (mirror of the CLI's --embed flag).

Security controls:

  • --read-only          drops every state-changing tool. Right for audit /
                         observation agents, untrusted prompt sources, CI runs.
  • Untrusted-data       free-form fields written by humans (description, memo,
                         note, comment, reason) are wrapped in
                         <untrusted source="…">…</untrusted> envelopes in tool
                         results. The server's initialize-time instructions tell
                         the agent to treat the wrapped content as inert.
  • Bulk cap             bron_tx_bulk_create rejects payloads with more than 50
                         transactions client-side, on top of backend approval
                         policies and rate limits.

Bron Desktop ships its own MCP server bundled — use that for operator-at-their-
desk workflows. Use "bron mcp" for headless / CI / API-key automations where
Desktop isn't running.

Reference: https://developer.bron.org/sdk/cli/mcp`,
}

// topicNames returns the sorted list of available topic names.
func topicNames() []string {
	out := make([]string, 0, len(topics))
	for n := range topics {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
