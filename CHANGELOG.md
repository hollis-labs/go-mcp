# Changelog

All notable changes to this project will be documented in this file.

## Unreleased

Per `CW-20260918-0011`, discovered while porting Hadron (`CW-20260917-0013`)
onto v0.3.0: `NewServer` hardcoded the official SDK's `ServerOptions` to
`nil`, and `ServerOptions` can only be set at `mcp.NewServer` construction
time — there was no way to set `Instructions`, a completion handler, or
other such fields at all.

### Added

- `server` — `NewServer(name, version, ...Option)`: a variadic `Option`
  parameter (backward compatible with existing two-argument calls) that
  populates the underlying official-SDK `ServerOptions`.
  - `WithInstructions(string)` — free-text instructions advertised to
    connecting clients.
  - `WithInitializedHandler(func(context.Context, *mcp.InitializedRequest))`
    — fires on `notifications/initialized`. Legacy-handshake-only: a
    2026-07-28 client opens with the stateless `server/discover` RPC
    (SEP-2575) and never sends this notification, so the handler never
    fires over that path.
  - `WithCompletionHandler(func(context.Context, *mcp.CompleteRequest) (*mcp.CompleteResult, error))`
    — serves `completion/complete` for prompt/resource-template argument
    completion.
  - `WithKeepAlive(time.Duration)` — periodic ping interval; an
    unresponsive peer's session is closed.

### Notes

- Prompts, resources, and session lifecycle remain intentionally unwrapped.
  `SDKServer()` already exposes the underlying `*mcp.Server` for
  `AddPrompt`/`AddResource`/`AddResourceTemplate` and for driving sessions
  directly (`SDKServer().Connect` returns the `*mcp.ServerSession`
  synchronously as "session registered"; `ServerSession.Wait` blocks until
  it closes, as "session unregistered") — see the README's `server` section.

## v0.3.0 — 2026-09-18

Per `adr_go-mcp-official-sdk-consolidation`: go-mcp's core is rebuilt on the
official `github.com/modelcontextprotocol/go-sdk`, targeting the 2026-07-28
MCP specification directly. This is a breaking change to `server` and
`transport/http`.

### Changed

- `server` — the hand-rolled stdio JSON-RPC loop is replaced by a thin
  wrapper over `*mcp.Server` from the official SDK.
  - `Tool` gains four **required** typed annotation fields (`ReadOnlyHint`,
    `DestructiveHint`, `IdempotentHint`, `OpenWorldHint`) — the MCP
    tool-annotation set. Every `RegisterTool` call now declares them
    explicitly on the wire; they are never left optional or inferred from
    the tool's name (the exact shape of a permission-gating bug this
    contract exists to make structurally impossible).
  - `ToolDefinition` gains an `Annotations ToolAnnotations` field.
  - `ProtocolVersion` is now `"2026-07-28"`.
  - Deterministic `tools/list` ordering and `notifications/cancelled`
    cancellation are preserved, now inherited from the official SDK rather
    than hand-rolled.
  - `CallTool` remains for direct, in-process tool invocation, bypassing
    the protocol layer; RPC callers are served through the wrapped SDK
    server via the new `Run(ctx)` (stdio) or `transport/http`.
  - `WithNotifier`/`Notify`/`NotifyProgress`/`NotifyMessage` are unchanged
    at the call site; handlers registered via `RegisterTool` now have a
    notifier bridged to the real client session automatically.
- `transport/http` — the hand-rolled HTTP+SSE dual-mode JSON-RPC handler is
  replaced by a thin wrapper over the official SDK's Streamable HTTP
  handler, in stateless mode (SEP-2567): no `Mcp-Session-Id` is read or
  set, and only `POST` is served (`GET`/`DELETE` return 405). The bespoke
  `Mcp-Method`/`Mcp-Name` header cross-validation and CORS preflight
  (`OPTIONS`) handling are dropped along with it -- the SDK handler has no
  equivalent surface to validate against, and full CORS preflight support
  was judged out of scope for this rewrite. `HandlerOptions.AllowedOrigins`
  is preserved, enforced ahead of the SDK handler.

### Added

- `budget` — `Envelope.TTLMs`/`Envelope.CacheScope` and
  `Config.TTLMs`/`Config.CacheScope`: opt-in client-caching hints matching
  the MCP 2026-07-28 `CacheableResult` shape (`ttlMs`/`cacheScope`).
  `CacheScope` defaults to `"public"` when `TTLMs` is set and `CacheScope`
  is left empty, matching the spec's own default.
  - `ErrorCode`, the `ErrCode*` constants, `ProtocolError`, and
    `NewProtocolError` — an app-owned JSON-RPC error-code taxonomy
    (`-32000..-32019`; `-32020..-32099` is reserved for the MCP
    specification itself) for signaling protocol-level MCP errors.
    `server`'s malformed-arguments path now uses `ErrCodeInvalidInput`.

### Notes

- `resultType` (the MRTR `complete`/`input_required` pattern) required no
  work here: the official SDK sets it automatically once tools flow
  through `(*mcp.Server).AddTool`, for clients that negotiate 2026-07-28.
- Whether every MCP client this portfolio runs against already negotiates
  2026-07-28 cleanly is unverified and was an accepted risk of this
  rewrite, not a blocker (see the ADR); handle it empirically if hit.
- The official SDK's `notifications/message` (deprecated logging, SEP-2577)
  did not reach a connected client's `LoggingMessageHandler` in v1.7.0
  during this rewrite's testing, even after `logging/setLevel`, reproduced
  in isolation against the SDK alone with no go-mcp code involved.
  `notifications/progress` was unaffected. Not a go-mcp defect; worth
  knowing if a consumer leans on server-side logging notifications.
- Known issue: `compat.TestNewSSEClientTransport_EndToEnd` fails
  deterministically at release time (`Read: context deadline exceeded`) —
  the endpoint-rewrite/keepalive path in `compat/sse.go` does not surface
  the real message. `compat` is the optional backward-compat package (not
  needed by any current-phase adopter); tracked as a follow-up on
  `CW-20260917-0032` rather than holding this release.
- `govulncheck ./...` at release time reports stdlib vulnerabilities
  (GO-2026-4918, GO-2026-4870, GO-2026-4866, and others) fixed in
  go1.26.2/go1.26.3; this toolchain is pinned at go1.26.1 portfolio-wide.
  A toolchain bump is a portfolio-level change, not a go-mcp code fix, and
  is out of scope for this release.

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
