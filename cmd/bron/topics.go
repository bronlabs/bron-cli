package main

import "sort"

// topics maps `bron help <topic>` queries to short conceptual blurbs.
// Each blurb is one paragraph + a few bullet points + a docs.bron.org link.
// Keep them short — agents want fast orientation, humans follow the link for depth.
var topics = map[string]string{
	"signing": `Signing — how the CLI authenticates to the Bron API

Every request carries a short-lived JWT signed with your ES256 (P-256) private key.
The signature covers the HTTP method, path, query, body hash, and a fresh timestamp,
so it is bound to that exact call and cannot be replayed.

  • Generate a keypair locally:           bron auth keygen --out ~/.config/bron/keys/me.jwk
  • Register the public JWK in Bron UI    (the binding is by "kid" inside the JWK).
  • Keep the private JWK on disk (0600);  point your profile at it via "key_file".
  • The CLI re-signs every request — no token caching, no revocation flow needed.

Reference: https://docs.bron.org/api/auth`,

	"profiles": `Profiles — config + env overrides

A profile is a named tuple of (workspace, key_file, proxy, base_url). Config lives at
~/.config/bron/config.yaml (or $XDG_CONFIG_HOME/bron/config.yaml, or $BRON_CONFIG).
Multiple profiles let you switch between environments without retyping flags.

Resolution order (highest precedence first):
  1. --profile / --workspace / --key-file / --proxy / --base-url flags
  2. BRON_PROFILE / BRON_WORKSPACE_ID / BRON_API_KEY_FILE / BRON_PROXY / BRON_BASE_URL env vars
  3. active_profile from YAML
  4. profile named "default"

Common workflow:
  bron config init --name dev --workspace <wsId> --key-file ~/.config/bron/keys/me.jwk
  bron config use-profile production
  bron config show              # what the HTTP client will see (env applied)
  bron config show --raw        # what is actually written in YAML

Behind a corporate proxy?
  bron config set proxy=http://user:pass@host:8080
  # or set HTTPS_PROXY / HTTP_PROXY in the environment — both are honored.

base_url defaults to https://api.bron.org and is hidden from help; override with
  bron config set base_url=https://api.qa.bron.io   # mostly for QA / staging
  # or pass --base-url per call, or BRON_BASE_URL=... in the environment.

Reference: https://docs.bron.org/cli/profiles`,

	"output": `Output — formats, queries, columns

Every command prints the API response in one of four formats, optionally filtered
by a small JSONPath subset and/or a column list.

  --output json   (default)   pretty-printed JSON, colored on TTY
  --output yaml               YAML
  --output jsonl              one JSON document per line (good for piping)
  --output table              aligned columns; nested objects collapse to {…} / […N], cells trimmed.
                              *At / *Time fields with epoch-millis values render as ISO UTC.

  --query .path[*].field      JSONPath subset:
      .foo.bar          object key (nested)
      .foo[0]           array index
      .foo[*]           map over every element
      .foo[*].bar       map + pick a sub-key

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

Reference: https://docs.bron.org/cli/output`,

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

Reference: https://docs.bron.org/cli/body`,

	"errors": `Errors — exit codes and error shape

API errors are returned as APIError with a status, code, message, and request ID.
The CLI prints them on stderr and maps the HTTP status to a stable exit code.

  401 / 403   → exit 3   (not authorized)
  404         → exit 4   (not found)
  400         → exit 5   (bad request)
  409         → exit 6   (conflict)
  429         → exit 7   (rate limited)
  5xx         → exit 8   (server error)
  other       → exit 1

The request ID is logged in API logs; quote it when reporting issues.

Reference: https://docs.bron.org/api/errors`,

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

Reference: https://docs.bron.org/api/idempotency`,

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
                                             (e.g. "tesla" → routes to that workspace).

Examples:
  bron tx withdrawal --accountId <accId> --externalId <idem> \
    --params.amount=100 --params.assetId=20145 --params.networkId=ethereum \
    --params.toAddressBookRecordId=a30lin1p2zr1wzqqt1l8n652

  bron tx withdrawal --accountId <accId> --externalId <idem> \
    --params.amount=10  --params.assetId=20145 --params.networkId=ethereum \
    --params.toAccountId=w9jh0gf3w9qaxlre7enezt17

  bron tx withdrawal --accountId <accId> --externalId <idem> \
    --params.amount=5   --params.assetId=20145 --params.networkId=ethereum \
    --params.toWorkspaceTag=tesla

Reference: https://docs.bron.org/api/withdrawals`,

	"agents": `Agents — using bron from LLMs and scripts

The CLI is designed to be machine-friendly:

  • bron help --schema                 — full CLI schema (every command, every body/response
                                         type) as JSON. One call, no follow-ups.
  • bron help <resource> <verb>        — per-command JSON: usage + flags + body + response.
  • --output json | yaml | jsonl       — structured output for parsing.
  • --query .path[*].field             — extract a single value without piping to jq.
  • Stable exit codes                  — see "bron help errors".
  • Idempotent writes via externalId   — see "bron help idempotency".

Recommended agent flow:
  1. Read "bron help --schema" once at session start.
  2. Pick the command + flags you need.
  3. Always supply --externalId for write operations.
  4. Parse --output json; rely on exit codes for branching.

Reference: https://docs.bron.org/cli/agents`,
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
