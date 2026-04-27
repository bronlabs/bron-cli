# bron CLI

Public CLI for the [Bron](https://bron.org) API. Single static binary, generated from the OpenAPI spec.

```
bron transactions list --limit 5 --output table
bron tx withdrawal --accountId acc_xxx --params.amount=10 --params.toAddress=0xabc ...
bron help transactions create
```

## Install

### macOS / Linux (Homebrew)

```bash
brew install bronlabs/tap/bron-cli
```

### Pre-built binaries

Download the latest release from [github.com/bronlabs/bron-cli/releases](https://github.com/bronlabs/bron-cli/releases) and put it on your `PATH`:

```bash
# darwin-arm64 example
curl -L https://github.com/bronlabs/bron-cli/releases/latest/download/bron-darwin-arm64 -o /usr/local/bin/bron
chmod +x /usr/local/bin/bron
bron --version
```

### From source

```bash
go install github.com/bronlabs/bron-cli/cmd/bron@latest
```

## Set up authentication

The CLI talks to the Bron API with a per-key JWT signature. You generate a P-256 keypair locally, register the **public** half with Bron (UI → API keys), and keep the **private** half on disk.

```bash
# 1. generate a keypair (private goes to a 0600 file, public is printed to stdout)
bron auth keygen --out ~/.config/bron/keys/me.jwk

# 2. paste the printed public JWK into the Bron UI to authorize this key
#    (Bron returns nothing — the binding is by `kid`)

# 3. wire it into a profile and set it active
bron config init --name default \
                 --workspace <your-workspace-id> \
                 --base-url https://api.bron.org \
                 --key-file ~/.config/bron/keys/me.jwk \
                 --set-active

# 4. sanity-check
bron config            # = bron config show, with env overrides applied
bron transactions list --limit 1
```

You can have multiple profiles (`--name staging` etc.) and switch with `bron config use-profile <name>`. Per-call overrides via `--profile`, `--workspace`, `--base-url`, `--key-file` or env vars (`BRON_PROFILE`, `BRON_WORKSPACE_ID`, `BRON_BASE_URL`, `BRON_API_KEY_FILE`).

## How commands are shaped

Every endpoint in the OpenAPI spec becomes a command:

```
bron <resource> <verb> [<positional-id>...] [--<field>...] [--file <path> | --json '{...}']
```

- `<resource>` — first URL segment (`transactions`, `accounts`, `address-book`, …)
- `<verb>` — remaining path segments verbatim (`list`, `get`, `create`, `accept-deposit-offer`, …)
- `{workspaceId}` is implicit (from profile or `--workspace`); other path params are positional in URL order
- query parameters become `--<name>` flags; body fields become `--<field>` / `--<a>.<b>` flags

Special case — `bron tx <type>`: shortcut for `transactions create --transactionType <type>`, with the type-specific body fields exposed as `--params.<field>`. List the available types with `bron tx types`.

## Output formats and queries

```bash
bron transactions list --output json     # default
bron transactions list --output yaml
bron transactions list --output jsonl
bron transactions list --output table

# JSONPath subset filter (no jq, no select — just navigation)
bron transactions list      --query '.transactions[*].transactionId'
bron transactions get <id>  --query '.status'
bron accounts list          --output table --query '.accounts[*]'
```

For heavier transformations, pipe to `jq`:

```bash
bron transactions list --output json | jq '.transactions[] | select(.status=="SIGNED")'
```

Date-shaped query parameters (names ending in `AtFrom`, `AtTo`, `Since`, `Before`, `After`) accept both ISO-8601 (`2026-04-01T00:00:00Z`, `2026-04-01`) and millisecond-epoch integers — the CLI normalizes to millis before sending.

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
    --externalId $(uuidgen) \
    --params.amount=250 \
    --params.feeLevel=HIGH
```

## Exit codes

Mapped from HTTP status:

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
bron help                             # = bron --help
bron help <resource>                  # list verbs for the resource
bron help <resource> <verb>           # JSON schema dump (usage + flags + body + response)
bron --output yaml help <r> <v>       # same dump in YAML
```

`bron help <resource> <verb>` is the entry point for any tooling that wants to introspect the CLI without parsing freeform `--help` text.

---

## Command reference

`list` is shown once with the full set of filters per resource — pick the subset you need. `<id>` placeholders are positional path arguments.

### auth / config / completion

```
bron auth keygen
bron auth keygen --out ~/.config/bron/keys/me.jwk

bron config                                      # = bron config show (env overrides applied)
bron config show
bron config show --raw                           # YAML entry without env overrides
bron config show --profile staging
bron config list
bron config path
bron config init                                                       # interactive
bron config init --name dev \
                 --workspace 70000 \
                 --base-url https://api.bron.org \
                 --key-file ~/.config/bron/keys/me.jwk \
                 --set-active
bron config use-profile production
bron config set workspace=70000
bron config set base_url=https://api.bron.org key_file=~/.config/bron/keys/me.jwk
bron config set --profile staging workspace=80000

bron completion zsh > ~/.zsh/completions/_bron
bron completion bash
bron completion fish
```

### accounts / activities / address-book / balances / deposit-addresses / intents / members / stakes / transaction-limits / workspace

```
bron accounts list --accountIds acc_x,acc_y \
                   --accountTypes vault,trading \
                   --excludedAccountTypes external \
                   --statuses ACTIVE \
                   --isTestnet false \
                   --isDefiVault false \
                   --limit 50 --offset 0
bron accounts get <accountId>

bron activities list --accountIds acc_x \
                     --userIds u1,u2 \
                     --activityTypes TRANSACTION_CREATED,TRANSACTION_SIGNED \
                     --excludedActivityTypes LOGIN \
                     --search "deposit" \
                     --limit 50 --offset 0

bron address-book list --recordIds rec_x \
                       --networkIds ethereum,tron \
                       --addresses 0xA,0xB \
                       --memo "..." \
                       --tag "exchange" \
                       --recordType address \
                       --recordTypes address,tag \
                       --statuses ACTIVE \
                       --limit 100 --offset 0
bron address-book get    <recordId>
bron address-book create --name "Alice" --address 0xA --networkId ethereum
bron address-book delete <recordId>

bron balances list --accountId acc_x \
                   --accountIds acc_x,acc_y \
                   --balanceIds bal_x \
                   --assetId 20145 --assetIds 20145,20146 \
                   --assetNotIn 99999 \
                   --networkId ethereum --networkIds ethereum,tron \
                   --accountTypes vault \
                   --excludedAccountTypes external \
                   --updatedSince 2026-04-01T00:00:00Z \
                   --nonEmpty true \
                   --limit 100 --offset 0
bron balances get <balanceId>

bron deposit-addresses list --accountId acc_x \
                            --addressIds addr_x \
                            --externalId my-ref \
                            --accountTypes vault \
                            --networkId ethereum \
                            --address 0xA \
                            --statuses ACTIVE \
                            --sortDirection DESC \
                            --limit 100 --offset 0

bron intents create --json '{"params":{...}}'
bron intents get <intentId>

bron members list --includePermissionGroups true \
                  --includeUsersProfiles true \
                  --includeEmails true

bron stakes list --accountId acc_x --assetId 20145 --rewardPeriod MONTH

bron transaction-limits list --statuses ACTIVE \
                             --fromAccountIds acc_x \
                             --toAddressBookRecordIds rec_x \
                             --toAccountIds acc_y \
                             --appliesToUserIds u1,u2 \
                             --appliesToGroupIds g1 \
                             --limit 100 --offset 0
bron transaction-limits get <limitId>

bron workspace info --includeSettings true
```

### assets / networks / symbols (dictionary, no workspace)

```
bron assets list --search btc \
                 --assetIds 20145,20146 \
                 --networkIds ethereum \
                 --symbolIds s1,s2 \
                 --contractAddress 0xC \
                 --contractIssuer "..." \
                 --assetType native,token \
                 --limit 100 --offset 0
bron assets get <assetId>
bron assets prices                                # no filters in API

bron networks list --networkIds ethereum,tron
bron networks get <networkId>

bron symbols list --symbolIds s1,s2 --assetIds 20145,20146 --limit 100 --offset 0
bron symbols get <symbolId>
bron symbols prices --baseSymbolIds s1,s2 --baseAssetIds 20145
```

### transactions — query

```
bron transactions list --transactionIds tx_x \
                       --transactionTypes withdrawal,allowance \
                       --transactionStatuses PENDING_APPROVAL,SIGNING \
                       --transactionStatusNotIn FAILED \
                       --accountTypes vault \
                       --accountId acc_x --accountIds acc_x,acc_y \
                       --assetIds 20145 \
                       --blockchainTxId 0xT \
                       --toAccountId acc_z \
                       --toAddress 0xR \
                       --externalId my-ref \
                       --isTerminated false \
                       --createdAtFrom 2026-04-01T00:00:00Z \
                       --createdAtTo   2026-04-27T23:59:59Z \
                       --updatedAtFrom 1775347200000 \
                       --terminatedAtFrom 2026-04-10 --terminatedAtTo 2026-04-27 \
                       --canSignWithDeviceId dev_x \
                       --includeEvents true \
                       --includeCurrentSigningRequest true \
                       --sortBy createdAt --sortDirection DESC \
                       --limit 50 --offset 0

bron transactions get    <transactionId>
bron transactions events <transactionId>
```

### transactions — write

```
bron transactions create --file ./tx.json
bron transactions create \
    --transactionType withdrawal \
    --accountId acc_xxx \
    --externalId $(uuidgen) \
    --params.amount=100 \
    --params.assetId=20145 \
    --params.networkId=ethereum \
    --params.toAddress=0xR

bron transactions dry-run     --file ./tx.json
bron transactions bulk-create --file ./batch.json

bron transactions approve <transactionId>
bron transactions approve <transactionId> --file ./approve.json
bron transactions decline <transactionId> --reason "policy"
bron transactions cancel  <transactionId> --reason "user"

bron transactions create-signing-request <transactionId>
bron transactions accept-deposit-offer   <transactionId>
bron transactions reject-outgoing-offer  <transactionId>
```

### tx <type> — shortcut for transactions create

```
bron tx types                                     # list all available types

bron tx withdrawal \
    --accountId acc_xxx \
    --externalId $(uuidgen) \
    --params.amount=100 \
    --params.assetId=20145 \
    --params.networkId=ethereum \
    --params.toAddress=0xR

bron tx allowance \
    --accountId acc_xxx \
    --externalId $(uuidgen) \
    --params.assetId=20145 \
    --params.networkId=ethereum \
    --params.toAddress=0xSPENDER \
    --params.amount=1000

bron tx stake-delegation \
    --accountId acc_xxx \
    --externalId $(uuidgen) \
    --params.amount=32 \
    --params.assetId=ETH \
    --params.poolId=pool_x

bron tx stake-undelegation \
    --accountId acc_xxx \
    --externalId $(uuidgen) \
    --params.amount=16 \
    --params.assetId=ETH \
    --params.stakeId=stk_x

bron tx stake-claim \
    --accountId acc_xxx \
    --externalId $(uuidgen) \
    --params.amount=1 \
    --params.assetId=ETH \
    --params.stakeId=stk_x

bron tx address-creation   --accountId acc_xxx --params.assetId=20145
bron tx address-activation --accountId acc_xxx --params.assetId=20145

bron tx fiat-in \
    --accountId acc_xxx \
    --externalId $(uuidgen) \
    --params.amount=100 \
    --params.assetId=USD \
    --params.fiatAmount=100 \
    --params.fiatAssetId=USD

bron tx fiat-out \
    --accountId acc_xxx \
    --externalId $(uuidgen) \
    --params.amount=100 \
    --params.assetId=20145 \
    --params.fiatAssetId=USD \
    --params.networkId=ethereum \
    --params.toAddressBookRecordId=rec_x

bron tx intents \
    --accountId acc_xxx \
    --externalId $(uuidgen) \
    --params.intentId=int_x \
    --params.feeLevel=MEDIUM
```

### env overrides

```
BRON_PROFILE=staging                       bron transactions list
BRON_WORKSPACE_ID=ws_other                 bron transactions list
BRON_BASE_URL=https://api.qa.bron.io       bron transactions list
BRON_API_KEY_FILE=~/.config/bron/keys/other.jwk   bron transactions list
BRON_CONFIG=/tmp/cli.yaml                  bron config show
```

---

## For contributors

### Build & test

```bash
make build              # incremental: regen if spec/cligen changed, then go build
make build-fast         # always regen, then build
make generate           # force-run cligen against the OpenAPI spec
make sync-spec          # pull bron-open-api-public.json from ../bron-sdk-go
make dist               # cross-compile darwin/linux × amd64/arm64 into bin/
make test
make lint               # golangci-lint
make lint-fix
make tidy
make clean              # remove bin/ and generated/
make help               # full target list
```

`VERSION=<tag> make build` stamps the binary with `bron --version`.

Generated files (`generated/commands.go`, `helpdoc.go`, `spec.go`, `spec.json`) are gitignored — `make build` regenerates them from `bron-open-api-public.json`. `bin/` is gitignored too.

### Layout

```
cmd/bron/         CLI binary entrypoint (root, help, auth, config commands)
cmd/cligen/       OpenAPI → cobra commands generator
internal/auth/    JWK keypair generation
internal/body/    --file / --json / --<field> merge
internal/client/  thin wrapper over bron-sdk-go/sdk/http (signing, path-param substitution)
internal/config/  ~/.config/bron/config.yaml + profiles + env overrides
internal/output/  table | json | yaml | jsonl + --query
internal/qparam/  query-parameter coercion (ISO-8601 → millis for date params)
internal/util/    tiny path helpers (~ expansion)
generated/        output of cligen (gitignored)
bron-open-api-public.json   embedded OpenAPI spec, synced from bron-sdk-go
```

### Built on

[`bron-sdk-go`](https://github.com/bronlabs/bron-sdk-go) provides JWT signing, the HTTP client (retries, `APIError`), and shared types. The CLI adds dynamic command dispatch, profile config, output formatting.
