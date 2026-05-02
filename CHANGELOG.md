# Changelog

## Unreleased

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
