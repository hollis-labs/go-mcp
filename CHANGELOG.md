# Changelog

All notable changes to this project will be documented in this file.

## v0.2.0 — 2026-05-24

### Added

- `server` package — reusable stdio MCP server core extracted from Cerberus.
  - `Tool` / `ToolHandler` registration API.
  - `NewServer(name, version)` stdio server with built-in JSON-RPC loop.
  - Strict schema helpers: `EmptyObjectSchema`, `ObjectSchema`.
  - Stable `tools/list` ordering for deterministic agent discovery.
  - In-flight request tracking and `notifications/cancelled` handling for
    request-scoped tool cancellation.
- `transport/http` package — reusable HTTP MCP transport over `server.Server`.
  - `http.Handler` wrapper for `initialize`, `tools/list`, and `tools/call`.
  - `202 Accepted` handling for notifications.
  - Request-context cancellation and optional `Origin` allowlist checks.

### Changed

- `README.md` now documents `budget`, `server`, and `transport/http` instead
  of describing `go-mcp` as budget-only.

## v0.1.0 — 2026-05-10

First public release.

### Added

- `budget` package — MCP response-budget envelope and helpers.
  - `Envelope` type — list-response wrapper with `Items`, `Count`, `Total`,
    `Truncated`, `Hint` fields (JSON-tagged for the MCP wire format).
  - `Config` type — caller-supplied limits (`Limit`, `MaxBytes`, `MaxTokens`)
    with `withDefaults` filling unset fields.
  - `Apply[T any](items, cfg, hintTemplate) Envelope` — generic helper that
    truncates a slice to `Limit` and builds an `Envelope` with a
    progressive-disclosure hint.
  - `Clamp`, `ExtractLimit`, `ExtractPagination` — paging-arg helpers for
    untyped MCP param maps.
  - `ToolJSON`, `ToolError` — JSON marshaling helpers for MCP tool
    responses.
  - `EstimateTokens`, `EstimateTokensFromString` — ~4-chars-per-token
    heuristic for rough payload sizing.
  - Constants: `DefaultLimit` (10), `MaxLimit` (25), `DefaultMaxTokens`
    (2000), `DefaultMaxBytes` (8000).
- `examples/list/main.go` — runnable end-to-end demo of `Apply`,
  `ExtractPagination`, and `ToolJSON`.
- `LICENSE` (MIT, Hollis Labs).
- `.gitignore` covering Go build artifacts and internal-tooling files.

### Changed

- Toolchain bumped to Go 1.26.1 (portfolio standard) from Go 1.25.0.

### Notes

- `Config.MaxBytes` and `Config.MaxTokens` are accepted by `Apply` but not
  yet enforced — only `Limit` drives truncation. Either tightening
  enforcement or removing the fields will be a deliberate choice in a
  future minor release.
- Pre-existing private tags `v0.0.1` and `v0.0.2` were never resolvable
  through `proxy.golang.org` (the repo was `PRIVATE`). The release-engineer
  tags `v0.1.0` at the merge commit of this PR as the canonical first
  public release.
