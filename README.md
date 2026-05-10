# go-mcp

Shared Go utilities for building [Model Context Protocol](https://modelcontextprotocol.io/)
tool servers. The module currently exposes a single subpackage, `budget`,
which wraps list-style tool responses with pagination and truncation
metadata so they fit a model-context-friendly token budget.

The library is intentionally dependency-free (stdlib only) so any MCP server
can import it without pulling in transitive dependencies.

## Status

Pre-1.0 (`v0.1.x`). The `budget` package has a stable API surface backed by
unit tests, and is the only package shipped today. See
[`CHANGELOG.md`](./CHANGELOG.md) for release notes and
[pkg.go.dev](https://pkg.go.dev/github.com/hollis-labs/go-mcp/budget) for godoc.

## Install

```bash
go get github.com/hollis-labs/go-mcp/budget
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

## API overview

All public API lives in `github.com/hollis-labs/go-mcp/budget`:

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

## Notes

- The module has exactly one subpackage (`budget/`); consumers import
  `github.com/hollis-labs/go-mcp/budget`.
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
