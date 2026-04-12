# MCP Helpers (go-mcp)

Shared Go utilities for building Model Context Protocol (MCP) tool servers. The
module currently exposes a single subpackage, `budget`, which implements the
response-size contract used by the Fragments Engine MCP servers (Engine,
Hadron, Cortex) to keep list responses within a ~2000-token budget. See
`budget/doc.go` for the package godoc, which references ADR-006 as the
canonical contract.

This library is intentionally dependency-free (stdlib only) so any MCP server
can import it without pulling in framework dependencies.

## Status

Beta. The `budget` package has a stable API surface backed by unit tests, but
the module is single-subpackage and has no CHANGELOG. See `AUDIT_RESULTS.md`.

## Install

```bash
go get github.com/hollis-labs/mcp-helpers/budget
```

## Usage

Apply the budget envelope to a list of items inside an MCP tool handler:

```go
package main

import (
    "fmt"

    "github.com/hollis-labs/mcp-helpers/budget"
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
        "%d tasks found. Use volon_task_get for details.",
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

## API Overview

All public API lives in `github.com/hollis-labs/mcp-helpers/budget`:

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

## Architecture Notes

- The module has exactly one subpackage (`budget/`). The `go.mod` declares no
  top-level package, so consumers import `github.com/hollis-labs/mcp-helpers/budget`.
- `budget.Config.MaxBytes` and `budget.Config.MaxTokens` are accepted by
  `Config.withDefaults` but are **not** currently enforced by `Apply` — only
  `Limit` affects truncation. If byte/token enforcement is required, it must
  be added by the caller or a future revision.
- The package godoc in `budget/envelope.go` points to "ADR-006" as the
  authoritative contract; that ADR is not bundled with this library.

## Dependencies

- **Framework-internal:** none.
- **External:** none. The package only imports `encoding/json` and `fmt` from
  the Go standard library.

## Testing

```bash
go test ./...
```

Tests live alongside the source under `budget/`:
`budget_test.go`, `helpers_test.go`, `tokens_test.go`. They are pure unit
tests with no external dependencies, fixtures, or environment variables.

## License

MIT License. See `LICENSE`.
