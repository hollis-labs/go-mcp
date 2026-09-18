# go-mcp

Shared Go utilities for building [Model Context Protocol](https://modelcontextprotocol.io/)
tool servers, targeting the 2026-07-28 MCP specification. The module
currently exposes:

- `staleness` — observation-only comparison of verified running and replacement images
- `budget` — list-style response envelopes, client-caching hints, an
  app-owned protocol error-code taxonomy, and truncation helpers
- `server` — a thin wrapper around the official
  [`modelcontextprotocol/go-sdk`](https://github.com/modelcontextprotocol/go-sdk),
  adding a simplified tool-registration surface with a required, typed
  tool-annotation contract (`readOnlyHint`/`destructiveHint`/etc. are never
  optional or inferred from a tool's name), deterministic `tools/list`
  ordering, and stdio serving
- `transport/http` — exposes a `server.Server` over the official SDK's
  Streamable HTTP transport, in stateless mode, with an origin allowlist

## Status

Pre-1.0 (`v0.2.x`). The `budget`, `server`, and `transport/http` packages are
tested and usable, but the module is still being shaped around real app
adoption. `server` and `transport/http` depend on
`github.com/modelcontextprotocol/go-sdk`; `budget` and `staleness` remain
stdlib-only. See [`CHANGELOG.md`](./CHANGELOG.md) for release notes.

## Install

```bash
go get github.com/hollis-labs/go-mcp/budget
go get github.com/hollis-labs/go-mcp/server
go get github.com/hollis-labs/go-mcp/transport/http
```

## Quickstart

Apply the budget envelope to a list of items inside an MCP tool handler:

```go
package main

import (
    "fmt"

    "github.com/hollis-labs/go-mcp/budget"
)

type Task struct {
    ID    string `json:"id"`
    Title string `json:"title"`
}

func listTasks(params map[string]any, allTasks []Task) string {
    // Pull caller-provided pagination, clamped to safe bounds.
    limit, _ := budget.ExtractPagination(params)

    // Wrap the slice in a truncation-aware envelope with a progressive
    // disclosure hint.
    env := budget.Apply(
        allTasks,
        budget.Config{Limit: limit},
        "%d tasks found. Use task_get for details.",
    )

    // Serialize to a JSON string suitable for an MCP tool response.
    return budget.ToolJSON(env)
}

func main() {
    tasks := []Task{{ID: "1", Title: "first"}, {ID: "2", Title: "second"}}
    fmt.Println(listTasks(map[string]any{"limit": 1}, tasks))
}
```

For error responses, use `budget.ToolError(code, message)` to produce a
consistent JSON error object.

A runnable end-to-end demo lives in [`examples/list/`](./examples/list).

## Packages

`github.com/hollis-labs/go-mcp/budget`

- `Envelope` — response wrapper with `Items`, `Count`, `Total`, `Truncated`,
  and `Hint` fields (`budget/envelope.go`).
- `Config` — caller-supplied limits: `Limit`, `MaxBytes`, `MaxTokens`
  (`budget/budget.go`).
- `Apply[T any](items []T, cfg Config, hintTemplate string) Envelope` —
  generic helper that truncates a slice to the budget and builds an
  `Envelope` with a progressive-disclosure hint (`budget/budget.go`).
- `Clamp(v, min, max int) int` — integer clamp helper (`budget/helpers.go`).
- `ExtractLimit(params map[string]any, defaultVal int) int` — read a clamped
  `"limit"` from an untyped params map (`budget/helpers.go`).
- `ExtractPagination(params map[string]any) (limit, offset int)` — read
  `"limit"` and `"offset"` from an untyped params map (`budget/helpers.go`).
- `ToolJSON(v any) string` — marshal a value to a JSON string; returns a JSON
  error object on failure (`budget/helpers.go`).
- `ToolError(code, message string) string` — build a JSON error response
  (`budget/helpers.go`).
- `EstimateTokens(payload []byte) int` and `EstimateTokensFromString(s string) int`
  — ~4-chars-per-token heuristic for payload sizing (`budget/tokens.go`).
- Constants: `DefaultLimit` (10), `MaxLimit` (25), `DefaultMaxTokens` (2000),
  `DefaultMaxBytes` (8000) (`budget/budget.go`).
- `Envelope.TTLMs` / `Envelope.CacheScope` and `Config.TTLMs` /
  `Config.CacheScope` — opt-in client-caching hints matching the MCP
  2026-07-28 `CacheableResult` shape (`ttlMs`/`cacheScope`); zero/empty by
  default, `CacheScope` defaults to `"public"` when `TTLMs` is set
  (`budget/envelope.go`, `budget/budget.go`).
- `ErrorCode` and the `ErrCode*` constants — an app-owned JSON-RPC
  error-code taxonomy (`-32000..-32019`; `-32020..-32099` is reserved for
  the MCP spec itself) (`budget/errors.go`).
- `ProtocolError` and `NewProtocolError(code, message, data)` — a
  structured, protocol-level MCP error for signaling a request-level
  failure (as opposed to a tool-execution error reported in successful
  result content) (`budget/errors.go`).

`github.com/hollis-labs/go-mcp/server`

- `Tool` — a tool registration: name, description, input schema, handler,
  and four **required** typed annotation fields (`ReadOnlyHint`,
  `DestructiveHint`, `IdempotentHint`, `OpenWorldHint`) that are always
  declared on the wire, never left optional or inferred from the tool's
  name (`server/server.go`).
- `ToolHandler` — `func(ctx, args map[string]any) (string, error)`, go-mcp's
  simplified handler signature, unchanged across the v2 rewrite.
- `NewServer(name, version)` — wraps an official-SDK `*mcp.Server`.
- `RegisterTool` / `ToolDefinitions` / `CallTool` — registration and
  direct, in-process tool invocation (bypassing the protocol layer).
- `Run(ctx)` — serve over stdio via the official SDK.
- `SDKServer()` — the underlying `*mcp.Server`, for transports (like
  `transport/http`) that need to drive it directly.
- `EmptyObjectSchema` and `ObjectSchema` — strict JSON object schema helpers.
- `WithNotifier` / `Notify` / `NotifyProgress` / `NotifyMessage` — a
  context-installed notification sink; handlers registered via
  `RegisterTool` have one bridged to the real client session automatically.
- Cancellation (`notifications/cancelled`) and deterministic `tools/list`
  ordering are inherited from the official SDK.

`github.com/hollis-labs/go-mcp/transport/http`

- `NewHandler(server, opts)` — wrap a `server.Server` as an `http.Handler`
  exposing it over the official SDK's Streamable HTTP transport, in
  stateless mode (2026-07-28 / SEP-2567).
- `HandlerOptions.AllowedOrigins` — optional browser-origin allowlist,
  enforced ahead of the SDK handler (403 on a disallowed `Origin`).

### Executable staleness

`github.com/hollis-labs/go-mcp/staleness` compares identities acquired by the
product against the launch selector supplied by its owner. `Compare` requires
compatible schemes, products and platforms; missing evidence yields `unknown`.
`different` means different replacement content, including a rollback. It does
not establish version order or grant permission to replace a process.

`Observe(ctx, running, target, inspect)` supports absolute Unix selectors,
including symlinks. It copies a regular executable of at most 256 MiB into a
private temporary directory, asks the product's `Inspector` to verify that
snapshot without executing it, then rechecks the source bytes and selector.
Inode and timestamp changes detect races; they never prove content equality.
The snapshot is removed before return. Callers should serialize inspections to
bound temporary disk use. Inspectors run synchronously and should honor context
cancellation; a blocking native verifier cannot be interrupted by this package.

Identity acquisition is deliberately outside this dependency-free library.
A version string, commit, pathname, or first hash of a mutable executable path
is not verified running identity. The product must bind its retained identity
to the executing image and document which content its digest scheme covers.
PATH/relative selectors require owner-side resolution evidence that this first
implementation does not reconstruct. Unsupported selectors and verification
failures remain `unknown`; raw verifier errors are not published.

An observation is point-in-time evidence. It neither reserves a future exec nor
proves that OS policy will permit one. No exit, signal, retry, admission or drain
behavior is included.

## Notes

- `Config.MaxBytes` and `Config.MaxTokens` are accepted by `Apply` but are
  **not** currently enforced — only `Limit` drives truncation. Either
  tightening enforcement or removing the fields will be a deliberate choice
  in a future minor release; see `CHANGELOG.md` for the open follow-up.

## Dependencies

`budget` and `staleness` use only the Go standard library. `server` and
`transport/http` depend on
[`github.com/modelcontextprotocol/go-sdk`](https://github.com/modelcontextprotocol/go-sdk)
(and its transitive dependencies), which they wrap.

## Testing

```bash
go test -race ./...
```

Tests live alongside the source under `budget/`:
`budget_test.go`, `helpers_test.go`, `tokens_test.go`, plus
`example_test.go` for godoc-rendered examples.

## License

MIT License — see [`LICENSE`](./LICENSE). © Hollis Labs.
