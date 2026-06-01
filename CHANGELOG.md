# Changelog

## 0.3.12 — 2026-06-01

### Fixed
- `bron_help` `tx-aggregation` recipes used postfix `tonumber?`, which silently drops a whole object on a null/non-numeric input. Switched to null-safe `(.x // "0" | tonumber)`.

## 0.3.11 — 2026-06-01

### Added
- MCP response shaping on every read tool. `fields` projects the reply to a comma-separated list of dot-paths (array-aware: a path that crosses an array applies to every element); `jq` runs a [gojq](https://github.com/itchyny/gojq)-compatible program server-side for filtering, aggregation and reshaping. Both compose (`jq` after `fields`) and trim the response before it reaches an agent's context. The `jq` sandbox has no environment, stdin or module access and is time- and size-bounded.
- `bron_help` MCP tool — read-only discovery: the data model, a tool's response shape resolved from the OpenAPI spec, and worked `jq` recipes, on demand. Keeps per-tool descriptions short.
- Pitfall hints on `bron_tx_list` / `bron_tx_get`: for financial totals pass `includeEvents: true` and aggregate `_embedded.events`, not `params.amount`.

### Changed
- Slimmer MCP tool schemas — dead documentation links and oversized enum lists are dropped from tool descriptions, lowering the constant context every session pays.

## 0.3.10 — 2026-05-31

### Changed
- `bron mcp` documents its tool-routing logic in the server instructions.
- `Makefile`: drop the `help` target and its output.

## 0.3.9 — 2026-05-02

### Changed
- `bron tx subscribe`: full filter set. `bron tx dry-run`: typed per-type subcommands.
- OpenAPI spec synced to the 2026-05-02 build.

## 0.3.8 — 2026-05-02

### Added
- `bron mcp install --target <claude-code|claude-desktop|cursor|cline>` registers `bron mcp` with the host's `mcp.json` (or runs `claude mcp add` for Claude Code). Supports `--name`, `--read-only`, `--dry-run`. Idempotent atomic write.
- `bron config init` interactive flow auto-resolves `workspaceId`: after you paste the public JWK in Bron Settings → API keys and press Enter, the CLI calls `GET /workspaces` to validate the registration and pick up the workspace ID from the response.
- Client-side enum validation for query flags (`--statuses`, `--transactionTypes`, …). Bad values fail with `error: --<flag>: invalid value "<bad>" (allowed: …)`, exit 1, no API call made.
- ANSI-colorised JSON output for `bron config init` public-JWK print on TTY (matches the `--output json` palette). Workspace name highlighted in green when `Resolved workspace from /workspaces:` prints.

### Changed (breaking)
- `bron auth keygen` removed — folded into `bron config init` (`--key-file` pointing at a non-existent path generates a fresh keypair).
- `--query` global flag removed — pipe to `jq` for transformations.
- `bron tx subscribe` default flipped: live-only by default. Pass `--with-history` for an initial replay of every currently-matching transaction. The old `--no-history` flag is removed.
- `bron config path` removed — the resolved config-file path is now part of `bron config show` output as `configPath`.
- Bare `bron config` prints cobra help (was: equivalent to `bron config show`). Use `bron config show` explicitly.
- Error envelope on stderr: the `trace:` field is renamed to `id:` (CLI surface only). The SDK `APIError.RequestID` field and the MCP error payload `requestId` keep their names.
- `bron config init` no longer prompts interactively for `--key-file`. If omitted, the CLI falls back to `~/.config/bron/keys/<name>.jwk`. Pass `--key-file` to override.
- `bron config init` profile-name prompt suggests `default` only when no `default` profile exists yet; otherwise the suggestion is empty so a stray Enter can't silently overwrite the active profile.
- `bron config init` requires `--workspace` in non-interactive runs (no TTY on stdin) — the auto-discovery prompt has no scripted equivalent.
- JSON output keys camelCase. `bron config show` / `bron config list`: `base_url` → `baseUrl`, `key_file` → `keyFile`, `config_path` → `configPath`, `key_source` → `keySource`. `bron --schema` `x-bron-cli` block: `tx_shortcuts` → `txShortcuts`, `path_args` → `pathArgs`, `params_ref` → `paramsRef`, `top_fields` → `topFields`. The on-disk YAML config schema is unchanged (still snake_case `active_profile` / `key_file` / `base_url`) — existing user files keep loading.
- `bron config set <key>=<value>` accepts canonical camelCase keys (`workspaceId`, `keyFile`, `baseUrl`); legacy snake_case kept for back-compat.

### Fixed
- The "(paste the trace ID into a support ticket — …)" hint no longer trails every API error envelope.
