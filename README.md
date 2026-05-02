# Bron CLI

Public CLI for the [Bron](https://bron.org) API — a non-custodial treasury management platform for digital assets. Single static binary, regenerated from the OpenAPI spec on every API release. Designed to be a first-class surface for both humans and LLM agents.

Use it to script everything you do in the Bron UI: list accounts and balances, create and approve withdrawals, manage address books, query transaction history, set up automation. If you don't have a Bron account yet, [start here](https://bron.org).

## Prerequisites

- An active Bron workspace (sign up at [bron.org](https://bron.org)).
- A workspace member with **API key** permissions in that workspace.
- Access to the Bron UI to register the public half of an API key (Settings → API keys).

If you only need a UI client, you don't need the CLI. It targets API consumers — automations, treasury scripts, on-call dashboards, and agent integrations.

## Install

### MacOS / Linux (Homebrew)

```bash
brew install bronlabs/apps/bron
```

### Pre-built binary (any UNIX)

Picks the right artifact for your OS/arch automatically. Available builds: `darwin-arm64`, `darwin-amd64`, `linux-arm64`, `linux-amd64`, `windows-amd64.exe`.

```bash
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
curl -L "https://github.com/bronlabs/bron-cli/releases/latest/download/bron-${OS}-${ARCH}" -o /usr/local/bin/bron
chmod +x /usr/local/bin/bron
```

Windows: download `bron-windows-amd64.exe` from the [releases page](https://github.com/bronlabs/bron-cli/releases/latest) and put it on `%PATH%`.

### From source

```bash
go install github.com/bronlabs/bron-cli/cmd/bron@latest
```

## Authentication

The CLI talks to the Bron API with a per-key JWT signature. You generate a P-256 keypair locally, register the **public** half with Bron (Settings → API keys), and keep the **private** half on disk in a `0600` file (the CLI refuses to load anything more permissive).

```bash
# 1. one shot: generate the keypair, print the public JWK, save the profile
bron config init --key-file ~/.config/bron/keys/me.jwk

# 2. The CLI prints the public JWK and waits. Paste the JWK into the Bron UI:
open https://app.bron.org/settings/api-keys     # Settings → API keys → ✓ "Input public key (JWK)"

# 3. Press Enter in the CLI. It calls GET /workspaces with your fresh key,
#    auto-discovers the workspace ID, validates the registration in one step,
#    and writes the profile to ~/.config/bron/config.yaml.

# 4. Sanity-check.
bron config show
bron workspace info
```

If you already know the workspace ID (CI / scripts / the impatient), pass it explicitly and the CLI skips the auto-discovery step:

```bash
bron config init --name default --workspace <workspaceId> --key-file ~/.config/bron/keys/me.jwk
```

In **non-interactive runs (no TTY)** `--workspace` is required — the auto-discovery prompt has no scripted equivalent. Failing fast beats silently saving an unverified profile.

You can have multiple profiles (`--name staging` etc.) and switch with `bron config use-profile <name>`. Per-call overrides via `--profile`, `--workspace`, `--key-file`, `--proxy` or env vars (`BRON_PROFILE`, `BRON_WORKSPACE_ID`, `BRON_API_KEY` / `BRON_API_KEY_FILE`, `BRON_PROXY`). `BRON_API_KEY` carries the raw JWK bytes, intended for secret-store wrappers (`op run`, `vault`, …) that pipe the value at startup so it never lives on disk.

If you sit behind an HTTP/HTTPS proxy: persist it once with `bron config set proxy=http://user:pass@host:8080`, or set `HTTPS_PROXY` / `HTTP_PROXY` in the environment — both are honored automatically.

---

## Command reference

Compact list of every command — placeholders in `<angle brackets>`, real-looking values where they matter (enums, asset/network ids).

```
Bron CLI — public API client.

Resources follow the URL: bron <resource> <verb>. The <workspaceId> is implicit (from the active profile config).

Examples:
  bron help
  bron help <resource> <verb> [--output yaml]
  bron help <topic>                   # addresses | agents | body | errors | idempotency | mcp | output | profiles | signing
  bron help --schema                  # full CLI schema (every command + types) as one JSON

  bron config show
  bron config list
  bron config init --key-file ~/.config/bron/keys/me.jwk                             # interactive: gen keypair, print public JWK, auto-resolve workspaceId via GET /workspaces
  bron config init --name staging --workspace <workspaceId> --key-file ~/.config/bron/keys/staging.jwk   # explicit (CI / scripts)
  bron config use-profile production
  bron config set workspace=<workspaceId> keyFile=~/.config/bron/keys/me.jwk

  bron mcp                              # stdio MCP server, foreground
  bron mcp --read-only                  # GET endpoints + tx dry-run only — no withdrawals/approvals
  bron mcp install --target claude-desktop      # register `bron mcp` in Claude Desktop's mcp.json
  bron mcp install --target cursor              # ~/.cursor/mcp.json
  bron mcp install --target cline               # Cline (VS Code) globalStorage
  bron mcp install --target claude-code         # delegates to `claude mcp add`

  bron accounts list --accountTypes vault --limit 50
  bron accounts get <accountId>

  bron balances list --accountId <accountId> --assetId 5000 --networkId ETH --nonEmpty true

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

  bron tx withdrawal --accountId <accId> --externalId <idem> \
    --params.amount=100 --params.assetId=5000 --params.networkId=ETH \
    --params.toAddressBookRecordId=<recordId>
    # --params.toAddress=<rawAddress>            on-chain address (raw)
    # --params.toAddressBookRecordId=<recordId>  pre-saved address-book entry (validated by Bron)
    # --params.toAccountId=<accountId>           internal transfer (instant, no fee path)
    # --params.toWorkspaceTag=<tag>              route to another Bron workspace

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

  bron tx list --output yaml --includeEvents true
  bron tx list --output table --columns transactionId,status,transactionType,createdAt
  bron tx list --output jsonl | jq '.[] | select(.status=="signed")'    # heavier transformations: pipe to jq

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

  bron completion install              # auto-detects $SHELL (zsh|bash|fish)

Flags:
      --cell-max int       max chars per table cell; 0 disables truncation (default 28)
      --columns string     comma-separated keys to keep, e.g. transactionId,status,createdAt (works for json/yaml/jsonl/table)
      --embed string       comma list of related entities to embed in the response (e.g. prices,assets,events,permission-groups)
  -h, --help               help for bron
      --key-file string    path to JWK private key (overrides profile)
      --output string      output format: table|json|yaml|jsonl (default json)
      --profile string     config profile name
      --proxy string       HTTP/HTTPS proxy URL (overrides profile)
      --schema             print JSON schema (request + response) for the command instead of running it
  -v, --version            version for bron
      --workspace string   workspace id (overrides profile)

Use "bron <resource> <verb> --help" for any command's flags.
```

### env overrides

```
BRON_PROFILE=staging                                 bron tx list
BRON_WORKSPACE_ID=<workspaceId>                      bron tx list
BRON_API_KEY="$(op read 'op://Personal/Bron/jwk')"   bron tx list   # raw JWK bytes; never lands on disk
BRON_API_KEY_FILE=~/.config/bron/keys/other.jwk      bron tx list
BRON_BASE_URL=https://api-staging.bron.org           bron tx list
BRON_PROXY=http://user:pass@proxy:8080               bron tx list
HTTPS_PROXY=http://proxy:8080                        bron tx list   # standard env vars are honored too
BRON_CONFIG=/tmp/cli.yaml                            bron config show
```

`BRON_API_KEY` (raw JWK bytes) wins over `BRON_API_KEY_FILE` and the YAML's `key_file:` field when both are set — pair it with a secret store (1Password, Vault, sops) so the private JWK never lives on disk. The CLI strips the var from its environment after reading, so child processes don't inherit it.

---

## How commands are shaped

Every endpoint in the OpenAPI spec becomes a command:

```
bron <resource> <verb> [<positional-id>...] [--<field>...] [--file <path> | --json '{...}']
```

- `<resource>` — first URL segment, lowercased and shortened where useful (`tx` for `transactions`, `address-book` for `address-book-records`, …)
- `<verb>` — remaining path segments verbatim (`list`, `get`, `create`, `accept-deposit-offer`, …)
- `{workspaceId}` is implicit (from profile or `--workspace`); other path params are positional in URL order
- query parameters become `--<name>` flags; body fields become `--<field>` / `--<a>.<b>` flags

Special case — `bron tx <type>` (e.g. `bron tx withdrawal`, `bron tx allowance`, `bron tx stake-delegation`): shortcut for `bron tx create --transactionType <type>`, with the type-specific body fields exposed as `--params.<field>`. List the available types and verbs with `bron tx --help`.

## MCP server (`bron mcp`)

`bron` doubles as a Model Context Protocol stdio server when invoked with the `mcp` subcommand — same pattern as `gh mcp` / `stripe mcp`. Every public-API endpoint the CLI knows about is exposed as a typed MCP tool (`bron_tx_list`, `bron_balances_list`, `bron_tx_withdrawal`, `bron_tx_approve`, …), plus a long-poll `bron_tx_wait_for_state` for waiting on a transaction's terminal state without polling.

Auth follows the same precedence as the rest of the CLI: active profile, then env (`BRON_PROFILE`, `BRON_API_KEY` / `BRON_API_KEY_FILE`, `BRON_WORKSPACE_ID`, `BRON_BASE_URL`, `BRON_PROXY`).

The fastest way to wire it into a host: `bron mcp install --target <host>` edits the host's `mcp.json` (or runs `claude mcp add` for Claude Code). Idempotent, registers the absolute path of the running binary so future `brew upgrade` swaps stay live.

```bash
bron mcp install --target claude-desktop          # ~/Library/Application Support/Claude/claude_desktop_config.json
bron mcp install --target cursor                  # ~/.cursor/mcp.json
bron mcp install --target cline                   # Cline (VS Code) globalStorage
bron mcp install --target claude-code             # shells out to `claude mcp add`
bron mcp install --target cursor --read-only      # register with --read-only baked in
bron mcp install --target cursor --name bron-qa   # multiple profiles in the same host
bron mcp install --target claude-desktop --dry-run    # print planned change, don't write
```

If you'd rather hand-edit:

```bash
# Claude Code
claude mcp add bron -- bron mcp

# Claude Code, with the JWK fetched on-demand from 1Password (never lands on disk)
claude mcp add bron --env BRON_API_KEY='op://Personal/Bron/private-jwk' -- op run -- bron mcp

# Cursor / Claude Desktop / VS Code — drop into your mcp.json:
{
  "mcpServers": {
    "bron": { "command": "bron", "args": ["mcp"] }
  }
}
```

Pass `--read-only` to register only GET endpoints + `tx dry-run`. Use this for agents that should observe a workspace without being able to move funds (audit pipelines, untrusted prompt sources, CI runs):

```bash
bron mcp install --target claude-code --read-only
# or, hand-edited:
claude mcp add bron-readonly -- bron mcp --read-only
```

Bron Desktop has its own built-in MCP server when installed — use that for operator-at-their-desk workflows. Use `bron mcp` for headless / CI / API-key automations where Desktop isn't running.

## Live updates (`bron tx subscribe`)

`bron tx subscribe` is "GET extended" — same filters as `bron tx list`, but the connection stays open. The server replays the historical match as the first batch, then streams live updates as they happen. Output is JSONL only (`--output` is ignored for this command); pipe to `jq` for any reshaping.

```bash
# Live-only, no history (recommended for long-running watchers).
bron tx subscribe

# Filter the same way you would on `bron tx list`.
bron tx subscribe --transactionStatuses signing-required,waiting-approval
bron tx subscribe --accountId <accountId> --transactionTypes withdrawal,bridge

# Pipe to jq for any extra filtering / reshaping.
bron tx subscribe | jq 'select(.status == "completed")'
```

By default `bron tx subscribe` is live-only: no replay on connect, just the stream. Pass `--with-history` if you also want every currently-matching transaction replayed first — useful for "snapshot + tail" scripts, but can be a lot on a busy workspace.

## Output formats and queries

```bash
bron tx list --output json     # default — pretty-printed JSON, colored on TTY
bron tx list --output yaml
bron tx list --output jsonl
bron tx list --output table    # nested objects collapse to {…}/[…N], *At fields render as ISO UTC

# --columns picks fields (works for json / yaml / jsonl / table) in the listed order.
# Supports dot-paths: table shows flat headers, json/yaml emit nested objects.
bron tx list --output table --columns transactionId,transactionType,params.amount,params.assetId,createdAt
bron tx list --output json  --columns transactionId,status,params.amount
bron tx get <id>            --columns transactionId,status,params

# Heavier transformations (filter / select / aggregate) — pipe to jq.
bron tx list --output jsonl | jq '.[] | select(.status=="signed") | .transactionId'
```

JSON output is colored when stdout is a TTY. Disable with `NO_COLOR=1`, force on with `FORCE_COLOR=1`.

Table cells are truncated at 28 characters by default (long IDs/addresses get `…`). Override with `--cell-max <N>`; pass `--cell-max 0` to disable truncation entirely (useful when piping to scripts that need full values).

`bron <resource> <verb> --schema` (or `bron help <resource> <verb> --schema`) prints the JSON schema (path/query params, body, every response status) — handy for AI agents or quick API exploration.

The `--schema` output is OpenAPI 3.1; the wire shape is stable across the 0.x line. Major format migrations (e.g. OpenAPI 4) ship with a CLI major-version bump.

For heavier transformations, pipe to `jq`:

```bash
bron tx list --output json | jq '.transactions[] | select(.status=="signed")'
```

Date-shaped query parameters and body fields (anything the OpenAPI spec marks as `format: "date-time-millis"`) accept both ISO-8601 (`2026-04-01T00:00:00Z`, `2026-04-01`) and millisecond-epoch integers — the CLI normalizes to millis before sending. The set of recognized names is auto-derived from the embedded spec at startup, so any new `@EpochMillis` field added on the backend picks up coercion as soon as `make spec` regenerates the SDK — no hardcoded suffix list to drift.

## Body composition (write operations)

Two baseline sources (mutually exclusive), then field-flag overlay:

1. `--file <path>` — read body from a file (`-` = stdin), **or**
2. `--json '{...}'` — inline JSON string.
3. `--<field>` / `--<a>.<b>` — structured field flags. Override matching paths in the baseline.

```bash
# baseline from file
bron tx withdrawal --file ./tx.json

# baseline from stdin
cat tx.json | bron tx withdrawal --file -

# baseline as inline JSON
bron tx withdrawal --json '{"accountId":"acc_xxx","params":{"amount":100,"assetId":20145}}'

# baseline + per-field overrides
bron tx withdrawal --file ./tx.json \
    --externalId <idempotencyKey> \
    --params.amount=250 \
    --params.feeLevel=HIGH
```

## Errors & exit codes

On any 4xx / 5xx the CLI prints a structured envelope to **stderr** and sets the exit code from the HTTP status:

```
error: Transaction `i2rvrwzgzj…` not found!
  status: 404
  code:   not-found
  id:     d7d09dd2ee76…
```

- `code` is the stable error-code slug — branch on it in scripts; never on the human message.
- `id` is the per-request correlation ID (header `Correlation-Id`). Quote it when reporting issues; SDKs expose the same value as `RequestID` / MCP error payload `requestId`.
- Enum-typed query flags (`--statuses`, `--transactionTypes`, …) are validated client-side before the request hits the wire — bad values fail with `error: --<flag>: invalid value "<bad>" (allowed: …)`, exit 1.

| Status | Exit | |
|---|---|---|
| `401` / `403` | `3` | not authorized |
| `404`         | `4` | not found |
| `400`         | `5` | bad request |
| `409`         | `6` | conflict |
| `429`         | `7` | rate limited |
| `5xx`         | `8` | server error |
| other / non-API | `1` | |

## Agent-friendly help

```
bron help                                       # navigation page (topics + entry points)
bron --help                                     # full root help (flags + examples)
bron help <topic>                               # topic blurb (signing | profiles | output | body | errors | idempotency | agents | mcp)
bron help <resource>                            # list verbs for the resource
bron help <resource> <verb>                     # human help (same as `bron <r> <v> --help`)
bron help <resource> <verb> --schema            # per-command JSON schema (machine-readable)
bron help --schema                              # full CLI schema as one OpenAPI 3.1 document
bron --output yaml help <r> <v> --schema        # same per-command dump in YAML
```

`--schema` is the entry point for any tooling that wants to introspect the CLI without parsing freeform `--help` text. Output is a strict OpenAPI 3.1 fragment; pipe to `jq` / `swagger-cli` / any spec consumer.

---

## For contributors

### Build & test

```bash
make build              # incremental: regen if spec/cligen changed, then go build
make generate           # force-run cligen against the OpenAPI spec
make dist               # cross-compile darwin/linux × amd64/arm64 into bin/
make test
```

`VERSION=<tag> make build` stamps the binary with `bron --version`.

Generated files (`generated/commands.go`, `helpdoc.go`, `spec.go`, `spec.json`) are committed alongside the spec so `go install github.com/bronlabs/bron-cli/cmd/bron@latest` works on a fresh clone. `make build` regenerates them via `cligen`; CI verifies they stay in sync with the spec. `bin/` is gitignored.

### Built on

[`bron-sdk-go`](https://github.com/bronlabs/bron-sdk-go) provides JWT signing, the HTTP client (retries, `APIError`), and shared types. The CLI adds dynamic command dispatch, profile config, output formatting.

## Versioning

While on `0.x`, breaking changes may land in any minor bump. We treat the
exit-code contract, flag/verb names, and the `--schema` output shape as the
stable surface — best-effort across `0.x`, fully versioned across `1.x+`. Any
intentional breaking change is called out in the release notes for the
relevant tag on the [releases page](https://github.com/bronlabs/bron-cli/releases).

## License

MIT License - see LICENSE file for details.
