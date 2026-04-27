# bron CLI

Public CLI for the Bron API. Single static Go binary, generated from the OpenAPI spec.

Built on top of [`bron-sdk-go`](https://github.com/bronlabs/bron-sdk-go) — auth (JWT signing), HTTP client (retries, `APIError`) come from the SDK; the CLI adds dynamic command dispatch, profile config, output formatting.

## Status

In progress. See [BRO-486](https://linear.app/bron/issue/BRO-486/bron-cli-design-and-implementation) for the design and roadmap.

## Build

```bash
make build              # generate + go build → bin/bron
make generate           # regenerate cobra commands from OpenAPI
make sync-spec          # copy bron-open-api-public.json from ../bron-sdk-go
make test
make lint
```

`go.mod` uses `replace github.com/bronlabs/bron-sdk-go => ../bron-sdk-go` for local dev.
On release this is dropped in favor of a tagged version.

## Quick start

```bash
bron auth keygen --out ~/.bron/keys/me.jwk
bron config init --name dev --workspace <wsId> --base-url https://api.bron.org \
                 --key-file ~/.bron/keys/me.jwk --set-active

bron transactions list --limit 5 --output table
bron tx withdrawal --accountId acc_xxx --externalId $(uuidgen) \
                   --params.amount=10 --params.assetId=20145 \
                   --params.networkId=ethereum --params.toAddress=0xabc
bron help transactions get
```

## Commands at a glance

Generated from OpenAPI (one resource per first URL segment, verb is the remaining path-segments verbatim):

```
bron <resource> <verb> [<positional-id>...] [--<field>...] [--file <path> | --json '{...}']
```

The `{workspaceId}` path parameter is always implicit (taken from the active profile or `--workspace`). Other path params are positional in URL order.

Special case: `bron tx <type>` is a shortcut for `bron transactions create --transactionType <type>`, with body fields exposed as `--params.<field>` flags. The list of `<type>` values comes from `CreateTransaction.params.oneOf` in the spec.

Service commands (hand-written, not generated):

| Command | Purpose |
|---|---|
| `bron auth keygen` | generate a P-256 JWK keypair |
| `bron config init` | create/update a profile (interactive or via flags) |
| `bron config use-profile <name>` | set active profile |
| `bron config set <k>=<v>` | update fields on a profile |
| `bron config` / `bron config show` | print active profile (env overrides applied; `--raw` for unmodified YAML) |
| `bron config list` | list all profiles |
| `bron config path` | print the resolved config file path |
| `bron help <resource> <verb>` | agent-friendly help: usage + flags + body/response schemas (`--output yaml` for YAML) |

## Global flags

| Flag | Purpose |
|---|---|
| `--profile` | config profile name (overrides `BRON_PROFILE` and `active_profile` in YAML) |
| `--workspace` | override profile workspace |
| `--base-url` | override profile base URL |
| `--key-file` | override profile JWK private key path |
| `--output` | `table \| json \| yaml \| jsonl` (default `json`) |
| `--query` | JSONPath subset filter, e.g. `.transactions[*].transactionId` |

Env overrides applied on top of the profile (lowest precedence): `BRON_PROFILE`, `BRON_WORKSPACE_ID`, `BRON_BASE_URL`, `BRON_API_KEY_FILE`, `BRON_CONFIG`.

## Body composition (write operations)

Two baseline sources (mutually exclusive), then field-flag overlay:

1. `--file <path>` — read body from a file (`-` = stdin), **or**
2. `--json '{...}'` — inline JSON string.
3. `--<field>` / `--<a>.<b>` — structured field flags overlay (one flag per body field, generated from the request schema). Field values override matching paths in the baseline.

Date-shaped query parameters (names ending in `AtFrom`, `AtTo`, `Since`, `Before`, `After`) accept both ISO-8601 (`2026-04-01T00:00:00Z`, `2026-04-01`) and millisecond-epoch integers — the CLI normalizes to millis before sending.

## Exit codes

Mapped from HTTP status (per BRO-486):

| Status | Exit |
|---|---|
| `401` / `403` | `3` |
| `404` | `4` |
| `400` | `5` |
| `409` | `6` |
| `429` | `7` |
| `5xx` | `8` |
| other / non-API errors | `1` |

---

## Command reference

Comprehensive list of every command the CLI exposes today. `list` is shown once with the full set of filters per resource — pick the subset you need.

### help / version / completion

```
bron --help
bron --version
bron help                             # = bron --help
bron help <resource>                  # list verbs for the resource
bron help <resource> <verb>           # JSON/YAML schema dump (agent mode), format via --output
bron --output yaml help <res> <verb>  # same dump in YAML

bron <resource> --help
bron <resource> <verb> --help

bron completion zsh > ~/.zsh/completions/_bron
bron completion bash
bron completion fish
```

### auth

```
bron auth keygen
bron auth keygen --out ~/.bron/keys/me.jwk
```

### config

```
bron config                           # = bron config show (env overrides applied)
bron config show
bron config show --raw                # YAML entry without env overrides
bron config show --profile staging
bron config list
bron config path

bron config init                                                       # interactive
bron config init --name dev \
                 --workspace 70000 \
                 --base-url https://api.bron.org \
                 --key-file ~/.bron/keys/me.jwk \
                 --set-active

bron config use-profile production
bron config set workspace=70000
bron config set base_url=https://api.bron.org key_file=~/.bron/keys/me.jwk
bron config set --profile staging workspace=80000
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
bron assets prices                            # no filters in API

bron networks list --networkIds ethereum,tron
bron networks get <networkId>

bron symbols list --symbolIds s1,s2 --assetIds 20145,20146 --limit 100 --offset 0
bron symbols get <symbolId>
bron symbols prices --baseSymbolIds s1,s2 --baseAssetIds 20145
```

### transactions — query

Date filters accept **both millis (`1777311599505`) and ISO-8601 (`2026-04-01T00:00:00Z`, `2026-04-01`)** — the CLI normalizes to millis.

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
                       --updatedAtTo   1777939199000 \
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
bron tx types                                 # list all available types

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

### body composition examples

```
# baseline from file
bron tx withdrawal --file ./tx.json

# baseline from stdin
cat tx.json | bron tx withdrawal --file -

# baseline as inline JSON
bron tx withdrawal --json '{"accountId":"acc_xxx","params":{"amount":100,"assetId":20145}}'

# baseline + per-field overrides
bron tx withdrawal \
    --file ./tx.json \
    --externalId $(uuidgen) \
    --params.amount=250 \
    --params.feeLevel=HIGH

# inline JSON + overrides (fields beat --json)
bron tx withdrawal \
    --json '{"accountId":"acc_xxx","params":{"amount":100,"assetId":20145}}' \
    --params.toAddress=0xR
```

### output / query / global flags

```
bron --profile staging              transactions list
bron --workspace ws_other           transactions list
bron --base-url https://api.bron.org transactions list
bron --key-file ~/.bron/keys/other.jwk transactions list

bron transactions list --output json     # default
bron transactions list --output yaml
bron transactions list --output jsonl
bron transactions list --output table

bron transactions list      --query '.transactions[*].transactionId'
bron transactions list      --query '.transactions[0]'
bron transactions get <id>  --query '.status'
bron accounts list          --output table --query '.accounts[*]'
```

### env overrides

```
BRON_PROFILE=staging                       bron transactions list
BRON_WORKSPACE_ID=ws_other                 bron transactions list
BRON_BASE_URL=https://api.qa.bron.io       bron transactions list
BRON_API_KEY_FILE=~/.bron/keys/other.jwk   bron transactions list
BRON_CONFIG=/tmp/cli.yaml                  bron config show
```

---

## Layout

```
cmd/bron/        CLI binary entrypoint (root, help, auth, config commands)
cmd/cligen/      OpenAPI → cobra commands generator
internal/auth/   JWK keypair generation
internal/body/   --file / --json / --<field> merge
internal/client/ thin wrapper over bron-sdk-go/sdk/http (signing, path-param substitution)
internal/config/ ~/.bron/config.yaml + profiles + env overrides
internal/output/ table | json | yaml | jsonl + --query
internal/qparam/ query-parameter coercion (ISO-8601 → millis for date params)
internal/util/   tiny path helpers (~ expansion)
generated/       output of cligen — commands.go, helpdoc.go, spec.go, spec.json (gitignored)
bron-open-api-public.json   embedded OpenAPI spec, synced from bron-sdk-go
```
