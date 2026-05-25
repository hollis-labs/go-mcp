# go-mcp

Shared Go utilities for building [Model Context Protocol](https://modelcontextprotocol.io/)
tool servers. The module currently exposes:

- `budget` — list-style response envelopes and truncation helpers
- `server` — a stdio MCP server core with tool registration, strict tool
  schemas, deterministic `tools/list` ordering, and request cancellation for
  `notifications/cancelled`
- `transport/http` — HTTP handler for exposing an MCP server over POST-based
  MCP transport with request-context cancellation and origin checks

The library is intentionally dependency-free (stdlib only) so any MCP server
can import it without pulling in transitive dependencies.

## Status

Pre-1.0 (`v0.2.x`). The `budget`, `server`, and `transport/http` packages are
tested and usable, but the module is still being shaped around real app
adoption. See
[`CHANGELOG.md`](./CHANGELOG.md) for release notes.

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

`github.com/hollis-labs/go-mcp/server`

- `Tool` and `ToolHandler` — MCP tool registration primitives
- `NewServer(name, version)` — stdio MCP server with built-in JSON-RPC loop
- `RegisterTool` — add tools to the server registry
- `EmptyObjectSchema` and `ObjectSchema` — strict JSON object schema helpers
- cancellation support for `notifications/cancelled`
- deterministic `tools/list` ordering for stable agent discovery

`github.com/hollis-labs/go-mcp/transport/http`

- `NewHandler(server, opts)` — wrap a `server.Server` as an `http.Handler`
- `HandlerOptions.AllowedOrigins` — optional browser-origin allowlist
- POST JSON-RPC handling for `initialize`, `tools/list`, and `tools/call`
- `202 Accepted` handling for notifications
- request-context cancellation for in-flight tool calls

## Notes

- `Config.MaxBytes` and `Config.MaxTokens` are accepted by `Apply` but are
  **not** currently enforced — only `Limit` drives truncation. Either
  tightening enforcement or removing the fields will be a deliberate choice
  in a future minor release; see `CHANGELOG.md` for the open follow-up.

## Dependencies

None. The package only imports `encoding/json` and `fmt` from the Go
standard library.

## Testing

```bash
go test -race ./...
```

Tests live alongside the source under `budget/`:
`budget_test.go`, `helpers_test.go`, `tokens_test.go`, plus
`example_test.go` for godoc-rendered examples.

## License

MIT License — see [`LICENSE`](./LICENSE). © Hollis Labs.
