# go-mcp

Shared Go utilities for building [Model Context Protocol](https://modelcontextprotocol.io/)
tool servers and clients, targeting the 2026-07-28 MCP specification. The
module currently exposes:

- `staleness` — observation-only comparison of verified running and replacement images
- `budget` — list-style response envelopes, client-caching hints, an
  app-owned protocol error-code taxonomy, and truncation helpers
- `server` — a thin wrapper around the official
  [`modelcontextprotocol/go-sdk`](https://github.com/modelcontextprotocol/go-sdk),
  adding a simplified tool-registration surface with a required, typed
  tool-annotation contract (`readOnlyHint`/`destructiveHint`/etc. are never
  optional or inferred from a tool's name), deterministic `tools/list`
  ordering, and stdio serving
- `client` — connects OUT to external MCP servers over stdio, streamable
  HTTP, or legacy SSE: dial-on-first-use, one retry on a recoverable
  error, a lazy health probe for non-stdio connections, and an opt-in
  response-size cap
- `transport/http` — exposes a `server.Server` over the official SDK's
  Streamable HTTP transport, in stateless mode, with an origin allowlist
- `auth` — a pluggable auth/middleware seam for `transport/http`, with a
  lightweight static-token default provider
- `compat` — isolated backward-compat adapters for peers that don't speak
  2026-07-28 yet; currently an SSE client transport for the legacy
  2024-11-05 transport
- `supervise` — pure primitives for a product's own child-process
  supervision loop: a bounded backoff schedule, an exit classifier, and a
  redacted stderr tail

## Status

Pre-1.0. The `budget`, `server`, `client`, `transport/http`, `auth`,
`compat`, and `supervise` packages are tested and usable, but the module is
still being shaped around real app adoption. Only `budget`, `staleness`, and
`supervise` are stdlib-only; every other package depends on
`github.com/modelcontextprotocol/go-sdk`. See [`CHANGELOG.md`](./CHANGELOG.md)
for release notes.

## Install

```bash
go get github.com/hollis-labs/go-mcp/budget
go get github.com/hollis-labs/go-mcp/server
go get github.com/hollis-labs/go-mcp/client
go get github.com/hollis-labs/go-mcp/transport/http
go get github.com/hollis-labs/go-mcp/auth
go get github.com/hollis-labs/go-mcp/compat
go get github.com/hollis-labs/go-mcp/supervise
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

func listTasks(params map[string]any, allTasks []Task) (any, error) {
    // Pull caller-provided pagination, clamped to safe bounds.
    limit, _ := budget.ExtractPagination(params)

    // Wrap the slice in a truncation-aware envelope with a progressive
    // disclosure hint.
    env := budget.Apply(
        allTasks,
        budget.Config{Limit: limit},
        "%d tasks found. Use task_get for details.",
    )

    // Return the value itself, as a server.ToolHandler would: go-mcp
    // JSON-marshals it into CallToolResult.StructuredContent AND a mirrored
    // text block. Don't pre-marshal with budget.ToolJSON here -- that
    // produces a string, which is used verbatim as prose and never
    // promoted to StructuredContent.
    return env, nil
}

func main() {
    tasks := []Task{{ID: "1", Title: "first"}, {ID: "2", Title: "second"}}
    result, _ := listTasks(map[string]any{"limit": 1}, tasks)
    fmt.Printf("%+v\n", result)
}
```

For error responses, return a `*budget.ToolError` — it gets the same
StructuredContent treatment, so the calling agent sees a machine-readable
code plus a concrete next step, not just a message:

```go
return nil, budget.NewToolError("not_found", "task TASK-001 not found").
    WithField("task_id").
    WithNextStep("call task_list to find a valid task_id")
```

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
  error object on failure (`budget/helpers.go`). For a `server.ToolHandler`,
  prefer returning the value directly over pre-marshaling with this: a
  `server.ToolHandler` result gets StructuredContent for free, a
  pre-marshaled string doesn't. Useful for building text by hand outside a
  `ToolHandler`.
- `ToolError` — a structured, tool-execution error (`Code`, `Message`,
  `Field`, `Retryable`, `NextStep`, `HelpTool`), for reporting inside a
  successful `CallToolResult`'s content (`IsError=true`), not as a
  protocol-level error — see `ProtocolError` for that case. Build one with
  `NewToolError(code, message)` and chain `WithField`/`WithRetryable`/
  `WithNextStep`/`WithHelpTool`; a `server.ToolHandler` returning one gets
  its full shape preserved in StructuredContent, not collapsed to a bare
  message (`budget/errors.go`).
- `StructuredError` — an interface (`error` + `ToolErrorContent() any`) for
  a caller whose error contract doesn't fit `ToolError`'s fields (for
  example, one that deliberately omits a human-readable message) but still
  wants the same StructuredContent+IsError treatment from a
  `server.ToolHandler` (`budget/errors.go`).
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

- `Tool` — a tool registration: name, optional `Title`, description, input
  schema, optional `OutputSchema`, handler, and four **required** typed
  annotation fields (`ReadOnlyHint`, `DestructiveHint`, `IdempotentHint`,
  `OpenWorldHint`) that are always declared on the wire, never left optional
  or inferred from the tool's name (`server/server.go`).
- `ToolHandler` — `func(ctx, args map[string]any) (any, error)`. A `string`
  result is used verbatim as text content. Anything else is JSON-marshaled
  into `CallToolResult.StructuredContent` (SEP-2106) *and* mirrored as JSON
  text content, so a client reading either gets the same data — most tools
  should return a map/struct/slice, not a pre-marshaled JSON string. A
  returned `*budget.ToolError` keeps its full structured shape in
  StructuredContent; any other error is reported as its plain `Error()`
  string, exactly as before.
- `NewServer(name, version, ...Option)` — wraps an official-SDK
  `*mcp.Server`. `Option` populates the handful of official-SDK
  `ServerOptions` fields that can only be set at construction time and have
  no other way in: `WithInstructions`, `WithInitializedHandler`,
  `WithCompletionHandler`, `WithKeepAlive`, `WithKeepAliveFailureThreshold`,
  `WithCapabilities`, `WithSupportedProtocolVersions`, `WithLogger`.
- `RegisterTool` / `RemoveTools` / `ToolDefinitions` / `CallTool` —
  registration, removal, and direct, in-process tool invocation (bypassing
  the protocol layer).
- `Run(ctx)` — serve over stdio via the official SDK.
- `SDKServer()` — the underlying `*mcp.Server`, for transports (like
  `transport/http`) that need to drive it directly, and for prompts,
  resources, and session lifecycle, which go-mcp does not wrap (see below).
- `EmptyObjectSchema` and `ObjectSchema` — strict JSON object schema helpers.
- `WithNotifier` / `Notify` / `NotifyProgress` / `NotifyMessage` — a
  context-installed notification sink; handlers registered via
  `RegisterTool` have one bridged to the real client session automatically.
- `MetaFromContext` — reads the tool call's protocol-level `_meta` object
  (installed automatically for every call served through the protocol
  layer; nil for a direct in-process `Server.CallTool`, which carries no
  `_meta`). For a caller-attached field that isn't a tool argument, such as
  an idempotency key sent via `_meta` -- a convention this portfolio
  already uses.
- Cancellation (`notifications/cancelled`) and deterministic `tools/list`
  ordering are inherited from the official SDK.
- **Prompts, resources, and session lifecycle are not wrapped**, by design:
  `SDKServer()` returns the underlying `*mcp.Server`, and callers drive
  `AddPrompt`, `AddResource`/`AddResourceTemplate`, and session tracking
  directly against it. For "a new session started" / "a session ended",
  call `SDKServer().Connect` yourself instead of `Run` — it returns the
  `*mcp.ServerSession` synchronously as the connection is established, and
  `ServerSession.Wait` blocks until it closes. `WithInitializedHandler`
  is not a substitute: a 2026-07-28 client opens with the stateless
  `server/discover` RPC (SEP-2575) and never sends
  `notifications/initialized`, so that handler only fires over a legacy
  pre-2026-07-28 handshake.

`github.com/hollis-labs/go-mcp/client`

- `Pool` — a named set of external MCP server connections, dialing each
  lazily on first use and reusing the connection thereafter.
  `NewPool(...Option)`, `Register(name, ServerConfig)`,
  `Deregister(name)`, `Get(name) (*Client, error)`, `CallTool`,
  `ListTools`, `Invalidate(name)` (tears the connection down without
  deregistering — the next call transparently re-dials), `Close()`.
- `ServerConfig` — `Transport` (`"stdio"` default, `"http"`/
  `"streamable_http"`, or `"sse"`), `Command`/`Args`/`Env` (stdio),
  `URL`/`Headers`/`TimeoutSeconds` (http/sse).
- `Client` — one named connection: `CallTool`, `ListTools`, `Ping`,
  `SetMaxResponseBytes(n)`, `Close()`, `SDKSession()` (escape hatch to the
  underlying `*mcp.ClientSession`).
- `CallMetadata` — observability for one call: `Server`, `Transport`,
  `ReusedClient`, `HealthProbe`, `Reconnected`, `RetryCount`,
  `AttemptCount`.
- `Option`s: `WithIdentity(name, version)` (required — no default client
  identity), `WithRetries(n)` (default 1), `WithProbeInterval(d)`
  (default 30s, stdio is never probed), `WithMaxResponseBytes(n)` (opt-in;
  unset means stdio uses the SDK's own `CommandTransport` and http/sse
  inherit the SDK's `DefaultMaxEventSize`), `WithCommandEnv(func)` (stdio
  subprocess environment policy; default inherits the host environment),
  `WithHTTPClient(func)`, `WithLogger(*slog.Logger)`.
- `DefaultMaxResponseBytes` — a suggested cap (10 MiB) for
  `WithMaxResponseBytes`, not applied automatically.
- `IsRecoverableError(err) bool` — the classifier deciding whether a
  connection error is worth invalidating and re-dialing.
- Reconnect and health-probe both leave a connection alone when the
  failure was only the caller's own context ending — an abandoned
  request looks identical to a broken connection from here, and closing
  for it would charge the next caller a reconnect.

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

### Child-process supervision

`github.com/hollis-labs/go-mcp/supervise` holds the primitives Tether's own
proactive stdio-upstream supervisor was built from, generalized for reuse.
Like `staleness`, it is dependency-free and owns no lifecycle: it never spawns
a process, calls `os.Exit`, sends a signal, or restarts anything. The
caller's own supervision loop reads state from these primitives and acts on
it.

- `Policy` — a bounded exponential backoff schedule (`Delays`) plus a
  continuous-uptime window (`StableFor`) after which the caller's restart
  counter should reset. `DefaultPolicy()` returns Tether's own schedule
  (1s/2s/4s/8s/16s, reset after 1 minute stable). `Next(attempt)` returns the
  delay for a zero-based restart attempt, or `false` once the policy is
  exhausted; `Limit()` is the restart count it allows.
- `ClassifyExit(state *os.ProcessState, at time.Time) Exit` — classifies a
  process's terminal `os.ProcessState` (captured right after `Wait` returns)
  into `Clean`, `Error`, or `Signal`, with the exit code and (on a
  platform that reports one) the signal name.
- `Tail` — a bounded, concurrent-safe ring buffer for a process's stderr,
  assignable directly to `exec.Cmd.Stderr`. `String()` returns the retained
  window with every configured `Secrets` value redacted, including a secret
  split across the window's edge by a mid-stream read. `Redact(original,
  secrets)` is the underlying pure string-redaction function.

## Notes

- `Config.MaxBytes` and `Config.MaxTokens` are accepted by `Apply` but are
  **not** currently enforced — only `Limit` drives truncation. Either
  tightening enforcement or removing the fields will be a deliberate choice
  in a future minor release; see `CHANGELOG.md` for the open follow-up.

## Dependencies

`budget`, `staleness`, and `supervise` use only the Go standard library.
`server`, `client`, `transport/http`, `auth`, and `compat` depend on
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
