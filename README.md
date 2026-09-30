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
- `clientguard` — per-key circuit breaking and client-side call-rate
  limiting around a caller's own upstream call, such as `client.Pool.CallTool`
- `skills` — one progressive-discovery "skills" tool for a server: a catalog
  with no argument, one skill's body by name, and a `skill_not_found` tool
  error that points back at the tool (`Register`, `MapSource`, `FSSource`)

## Status

Pre-1.0. The `budget`, `server`, `client`, `transport/http`, `auth`,
`compat`, and `supervise` packages are tested and usable, but the module is
still being shaped around real app adoption. Only `budget`, `staleness`,
`supervise`, and `clientguard` are stdlib-only; every other package depends on
`github.com/modelcontextprotocol/go-sdk`. See [`CHANGELOG.md`](./CHANGELOG.md)
for release notes.

## Install

```bash
go get github.com/hollis-labs/go-mcp/budget
go get github.com/hollis-labs/go-mcp/server
go get github.com/hollis-labs/go-mcp/args
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
  and `Hint` fields, plus opt-in `HasMore`, `NextCursor` and `TruncatedBy`
  (`budget/envelope.go`).
- `ApplyPage`, `Seal`, `Page`, `Fingerprint`, the cursor codec (`EncodeCursor`,
  `DecodeCursor`, `EncodeOffset`, `DecodeOffset`, `EncodeKeyset`,
  `DecodeKeyset`, `Keyset`, `ErrInvalidCursor`, `ErrCursorMismatch`) and
  `FitPrefix` / `ArrayBytes` / `BytesCap` — see "Paging, cursors and enforced
  caps" below (`budget/page.go`, `cursor.go`, `fit.go`).
- `Config` — caller-supplied limits: `Limit`, `MaxLimit`, `MaxBytes`,
  `MaxTokens` (`budget/budget.go`). Byte and token caps bind only when set.
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

#### Paging, cursors and enforced caps

`ApplyPage` pages an in-memory slice; `Seal` finishes a page a store has
already windowed. Both enforce the `Config.MaxBytes` / `Config.MaxTokens` the
caller sets (measured on one marshaled `Envelope`, hint and cursor included),
always keep at least one item so paging terminates, and mint the next cursor
*after* the trim, so it reflects what shipped. `Envelope` gains `hasMore`,
`nextCursor` and `truncatedBy` (`"limit"`, `"maxBytes"` or `"maxTokens"`), all
omitted when unset.

```go
package main

import (
	"fmt"

	"github.com/hollis-labs/go-mcp/budget"
)

func main() {
	items := []string{"alpha", "beta", "gamma", "delta", "epsilon"}
	fp := budget.Fingerprint("list", "all") // filters + sort, not page size

	cursor := ""
	for {
		env, err := budget.ApplyPage(items,
			budget.Page{Cursor: cursor, Fingerprint: fp, IssueCursors: true},
			budget.Config{Limit: 2, MaxBytes: 4000},
			"%d items available.")
		if err != nil { // errors.Is(err, budget.ErrInvalidCursor) => bad argument
			panic(err)
		}
		fmt.Println(env.Items, env.HasMore, env.TruncatedBy)
		if env.NextCursor == "" {
			break
		}
		cursor = env.NextCursor
	}
}
```

- Cursors are opaque, versioned, base64url tokens bound to a `Fingerprint` of
  the query. A cursor replayed against a different query fails with
  `ErrCursorMismatch` (which is also `ErrInvalidCursor`). `EncodeOffset` /
  `DecodeOffset` and `EncodeKeyset` / `DecodeKeyset` cover offset and keyset
  paging; `EncodeCursor` / `DecodeCursor` carry any JSON state.
- `Seal(items, hasMore, cfg, next, hint)` is for stores that page in SQL: the
  `next(lastKept)` callback builds the cursor from the last row that shipped.
  It must be pure, because it can be called for candidate sizes while fitting.
- `FitPrefix`, `ArrayBytes` and `BytesCap` are the fit primitives for callers
  that keep their own envelope and only want exact byte accounting.
- `Config.MaxLimit` raises (or lowers) the ceiling `Limit` is clamped to;
  zero keeps the package `MaxLimit` of 25.
- The `server` package sends a returned struct twice (StructuredContent and a
  mirrored text block), so `MaxBytes` bounds one copy; halve it if wire size
  matters.

**Compatibility.** Everything is additive. `Apply` with no `MaxBytes` /
`MaxTokens` set returns exactly the output it always did (no new fields).
`DefaultMaxBytes` / `DefaultMaxTokens` remain as suggested values but are
never applied implicitly. Callers that set a cap now get it enforced.

**Out of scope.** Previews, JSON-pointer and string truncation, and result
caching (the sibling `go-toolresult` library); a portfolio-wide list envelope
(surfaces keep their own wire shape); sort allow-lists and SQL typing of
keyset values; token counting beyond the 4-bytes-per-token estimate.

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
- `Prop` and its builders — `StringProp`, `StringEnumProp`, `NumberProp`,
  `IntegerProp`, `BooleanProp`, `ArrayProp`, `StringArrayProp`, `ObjectProp`
  — property-level declarations (name, JSON Schema shape, required flag),
  generalized from near-identical helpers eight apps independently wrote
  after migrating off mark3labs/mcp-go's typed `mcp.WithString`/`WithNumber`/
  `WithBoolean`/`Required` builder chain, which go-mcp's raw `any`
  `InputSchema` has no equivalent for. `InputSchema(props ...Prop)` is the
  property-level counterpart to `ObjectSchema`: it assembles a strict object
  schema from a set of `Prop`s and promotes each `Required` one into the
  schema's `required` list (`server/schema.go`).
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
- `IsProvablyUnsent(err) bool` — true only for the SDK's "client is
  closing" rejection, which happens before the request is written, so a
  retry cannot double-execute a tool. Recoverable is not the same as unsent.
- Per-call options on `Pool.CallTool` / `Client.CallTool`: `WithCallMeta(map)`
  (protocol `_meta`; copied, later wins per key), `WithCallRetry(RetryPolicy)`
  (`RetryDefault` = today's behavior, `RetryNever`, `RetryIfUnsent`), and
  `WithCallTimeout(d)` (per attempt, layered on the caller's ctx; never
  extends a caller deadline). Pool-level: `WithCallRetryPolicy(p)` (default
  for `CallTool` only), `WithDefaultCallTimeouts(map[transport]d)` (only when
  the caller's ctx has no deadline), `WithClientOptions(func(server, *mcp.ClientOptions))`
  (called at every dial; capabilities, elicitation/sampling handlers) and
  `WithToolListChangedHandler(func(ctx, server))`. A caller that passes none
  sees no change.
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

A per-call `_meta` and retry policy, e.g. an idempotency key that makes a
retry safe:

```go
pool := client.NewPool(client.WithIdentity("myapp", "1.0.0"))
_ = pool.Register("files", client.ServerConfig{Transport: client.TransportStdio, Command: "files-mcp"})

res, meta, err := pool.CallTool(ctx, "files", "write", args,
	client.WithCallMeta(map[string]any{"myapp/idempotencyKey": key}),
	client.WithCallRetry(client.RetryIfUnsent),
	client.WithCallTimeout(30*time.Second),
)
```

`client` compatibility: the added variadic parameter compiles for every
existing caller, but a hand-written interface with the old
`CallTool(ctx, server, tool, args)` method set is no longer satisfied by
`*Pool` / `*Client`. Pre-1.0: minor releases may change exported API.

`client` is out of scope for: supervised stdio (proactive reconnect, status,
`OnReconnect`), gateway policy (budgets, visibility, virtual servers), and
OpenTelemetry. The trace-context carrier is the caller's choice via
`WithCallMeta`. Note that a `Client` holds its lock for a whole call, so a
`Pool` serializes calls per server.

## Server kit and `args`

`server` composes tool behavior at registration time and `args` reads
arguments with named coercions. Nothing here is on by default.

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/hollis-labs/go-mcp/server"
)

func main() {
	srv := server.NewServer("notes", "0.1.0",
		server.WithSanitize(nil),                          // opt-in; logs to stderr, never stdout
		server.WithToolMiddleware(server.StrictArgs()),    // refuse unknown / missing arguments
	)
	srv.RegisterChecked(server.Tool{
		Name:        "note_get",
		Description: "Fetch a note by id.",
		InputSchema: server.InputSchema(
			server.StringProp("id", "note id", true),
			server.IntegerProp("limit", "max lines", false),
		),
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			return map[string]any{"id": args["id"]}, nil
		},
	}, server.Reads("looks up one note by id; no side effects"))

	if err := srv.Run(context.Background()); err != nil {
		log.New(os.Stderr, "", 0).Fatal(err)
	}
}
```

With that server, `note_get {"idd": "1"}` returns an error result (`IsError`)
whose `ToolError` says `idd` is not an argument and suggests `id`, instead of
a successful call that silently dropped the value.

- `server.WithToolMiddleware` / `ToolMiddleware` — per-tool handler wrappers;
  first registered is outermost; they also run on `Server.CallTool`.
  `WithReceivingMiddleware` installs SDK middleware; `WithSanitize` (explicit
  opt-in, runs first) installs the `sanitize` middleware.
- `server.RegisterChecked(tool, Reads(why) | Writes() | Destroys(why))` with
  `.OpenWorld()` / `.Idempotent()`; `WithBehaviorRequired`; `AnnotationTable`
  with `UnknownPanic` / `UnknownCautious` / `UnknownError`;
  `CautiousAnnotations`; `ValidateAnnotations`.
- `server.WithDuplicateTools(DuplicateReplace | DuplicatePanic |
  DuplicateRecord)` and `Server.RegistrationErrors`.
- `server.StrictArgs(opts...)` (names and required only; options
  `WithTransportKeys`, `WithTransportPrefix`, `WithStripTransportKeys`,
  `WithRetiredArgs`, `WithErrorCode`, `WithViolationHandler`) and
  `server.ValidateSchema()` (full jsonschema-go validation, rejects `"50"` for
  an integer; use only where clients send exact types).
- `args` — `String`, `Trimmed`, `NonBlank`, `Bool`, `Float`, `Int`,
  `IntClamped`, `PositiveInt`, `Whole`, `Strings`, `NonBlankStrings`,
  `LenientInt` / `LenientFloat` / `LenientBool`, `Require`. Each coercion is
  its own function; see the package doc. It depends only on the standard
  library and `budget`.

Compatibility: this is additive. `server.Tool`, `RegisterTool`,
`NewServer(name, version, ...Option)` and every existing `With*` keep their
signatures. `server.Option` is now `func(*options)` over an unexported
struct (it was `func(*mcpsdk.ServerOptions)`), so code that wrote its own
`Option` literal against the SDK type no longer compiles; none in this
portfolio does. `google/jsonschema-go` becomes a direct dependency (already in
the build graph through the SDK).

### Ordered, paginated `tools/list` and catalog lint

The SDK's own `tools/list` is always complete and alphabetical. Two opt-in
options replace it with the server's own catalog: `WithToolOrder(pinned...)`
(pinned names first, then registration order, then name) and
`WithToolsListPagination(pageSize, profileOf)` (pages with an opaque
`nextCursor` bound to `CatalogFingerprint()` and the profile id, so a cursor
from another profile or from before a catalog change is refused with JSON-RPC
invalid params, -32602). Each page's `ttlMs` / `cacheScope` fold the tools'
`Tool.TTLMs` / `Tool.CacheScope` (smallest ttl, `private` wins), and
`Tool.AlwaysLoad` publishes `_meta["tether/alwaysLoad"] = true`.

```go
package main

import (
	"context"
	"fmt"

	"github.com/hollis-labs/go-mcp/server"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	srv := server.NewServer("catalog", "0.1.0",
		server.WithToolOrder("status"),         // pinned first
		server.WithToolsListPagination(2, nil), // 2 tools per page; nil = one shared profile
	)
	for _, name := range []string{"zeta", "alpha", "status"} {
		srv.RegisterTool(server.Tool{
			Name: name, Title: name, Description: name,
			InputSchema: server.EmptyObjectSchema(), ReadOnlyHint: true,
			AlwaysLoad: name == "status", TTLMs: 60000,
		})
	}

	page, next, _ := srv.PaginateCatalog("", "", 2)
	for _, d := range page {
		fmt.Println(d.Name) // status, zeta
	}
	fmt.Println(next != "") // true

	// The same order is what a real client sees over the wire.
	ct, st := mcpsdk.NewInMemoryTransports()
	ctx := context.Background()
	_, _ = srv.SDKServer().Connect(ctx, st, nil)
	cs, _ := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "c", Version: "0"}, nil).Connect(ctx, ct, nil)
	res, _ := cs.ListTools(ctx, nil)
	fmt.Println(len(res.Tools), res.NextCursor != "")

	// Opt-in lint, never run by NewServer.
	for _, is := range server.LintCatalog(srv.ToolDefinitions()) {
		fmt.Println(is.Tool, is.Message)
	}
}
```

`server.LintCatalog(defs, opts...)` checks name charset (`^[a-z0-9_]+$`) and
length (default 64, `WithMaxNameLength`; `WithNameCharset`), blank titles,
names that differ only by case, an over-long instructions string
(`WithLintInstructions`), and appends `ValidateAnnotations`' findings;
`WithRequireChecked` also flags tools not registered through `RegisterChecked`
(`ToolDefinition.AnnotationsChecked`). `WithInstructions` now panics past
`MaxInstructionsLen` (2048 runes); `ValidateInstructions` is the non-panicking
check. A gateway merging several upstream catalogs should set
`DuplicatePanic` or `DuplicateRecord`; `LintCatalog` covers only what that
cannot see (case-only collisions).

`WithExpectedNames(names...)` pins the exact set of tool names a catalog may
expose, the golden-list contract test servers otherwise write by hand: a tool
registered but not listed is an `unexpected tool` issue and a listed name that
is not registered is a `missing tool` issue, so a test asserting
`LintCatalog(srv.ToolDefinitions(), server.WithExpectedNames(...))` is empty
fails in both directions and says which side moved. It is off by default,
because a catalog is often meant to grow.

Compatibility: additive and opt-in. A server that installs neither
`WithToolOrder` nor `WithToolsListPagination` behaves exactly as before
(alphabetical `ToolDefinitions`, the SDK's own `tools/list`). The new `Tool`
and `ToolDefinition` fields have zero values that reproduce today's behavior.
Only tools registered through the `Server` are listed by the catalog
middleware; tools added directly via `SDKServer().AddTool` are not.

### Asking the client mid-call (multi-round-trip)

On protocol 2026-07-28 a server cannot send `elicitation/create` while it is
serving a tool call. Instead, a handler returns `server.InputRequired`. The
client fulfills the requests and calls again with the responses:

```go
Handler: func(ctx context.Context, args map[string]any) (any, error) {
    if resp, ok := server.InputResponses(ctx)["confirm"].(*mcpsdk.ElicitResult); ok {
        // the retry: verify server.RequestState(ctx), then act on resp
    }
    return server.InputRequired{
        Requests: mcpsdk.InputRequestMap{"confirm": &mcpsdk.ElicitParams{Message: "Proceed?"}},
        State:    signedState, // echoed back by the client: sign it
    }, nil
},
```

The SDK bridges clients on earlier protocols, so the same handler works for
them. URL-mode elicitation (`ElicitParams{Mode: "url", URL: …}`) goes the same
way to a client that advertises it.

### Out of scope

Not part of these packages: required-scope enforcement, an error-code
vocabulary (the guard's code is `invalid_argument` and overridable with
`WithErrorCode`), a conformance suite beyond `mcptest.Connect` (see below), OpenTelemetry spans or
trace-carrier parsing (go-mcp stays OTel-free; `_traceparent` is only
exempted from the argument check), and gateway concerns such as policy,
budgets, tool visibility and virtual servers. Sanitization is not default-on,
and hints are never inferred from tool names.

## Skills tool (`skills`)

`github.com/hollis-labs/go-mcp/skills` registers one tool that lets an agent
read a server's orientation docs progressively: called with no argument it
returns the catalog (`{"items": [{"name", "description"}...], "meta":
{"count", "progressive_discovery": true, "next": <tool>}}`), called with a name
it returns that skill's text, and an unknown name is a `*budget.ToolError`
(`skill_not_found`) whose message lists the available names and whose help tool
is the skills tool itself.

```go
//go:embed skills/*.md
var skillFiles embed.FS

skillsFS, _ := fs.Sub(skillFiles, "skills")
src, err := skills.FSSource(skillsFS) // one skill per *.md; "start-here" listed first
if err != nil {
	return err
}
err = skills.Register(srv, "myapp_skills",
	"MyApp orientation. Call with no arguments for the catalog; pass `name` to read one skill.", src)
```

`MapSource(index, bodies)` serves the same from memory, `WithArgName("topic")`
renames the argument, and `WithTitle` sets the display title. A server needing
more than a name, a description and a body implements `skills.Source` itself.
`FSSource` does not read frontmatter, and `Register` verifies the source once
(every listed name non-empty, unique and gettable). The tool is read-only and
idempotent, and refuses a name that is already registered.

Provenance: the shape is a synthesis of four independent implementations
(Hadron's `hadron_skills`, Tesseract's `tesseract_skills`, Station's
`atlas_guide`, Tether's `mux_skill_*`), not a port of any of them; Tether's
ranked broker and layered discovery are deliberately not part of this package.

## Client call guard (`clientguard`)

`github.com/hollis-labs/go-mcp/clientguard` decides whether a call to an
external upstream is attempted at all: per-key circuit breaking and client-side
call-rate limiting, meant to wrap `client.Pool.CallTool`. Like `budget`,
`staleness` and `supervise` it is stdlib-only and owns no lifecycle: it does
not import `client` (or the SDK, or any go-mcp package). The caller wraps its
own call and supplies a key, conventionally the server name passed to
`Pool.Register`. This is the call-admission guard; it is unrelated to
`server/guard.go` (`StrictArgs`, `ValidateSchema`).

- `Guard` / `New(opts...)` — one `CircuitBreaker` and one `RateLimiter` per
  key, created lazily. With no options it is a passthrough.
  `Guard.Do(ctx, key, fn)` and the generic `Do[T](ctx, g, key, fn)` run `fn`
  in this order: breaker (`ErrCircuitOpen`), then limiter (`ErrRateLimited`,
  or a wait), then `fn`, then record the outcome. `fn` runs with no lock held.
  `State(key)` and `Available(key)` are read-only introspection;
  `Reset(key)` drops a key's state (pair it with `Pool.Deregister`).
- Options, all opt-in: `WithCircuitBreaker(threshold, cooldown)`,
  `WithRateLimit(limit, period, mode)` with `RateLimitReject` (default, fail
  fast) or `RateLimitBlock` (wait, honoring `ctx`), and
  `WithFailureClassifier(func(error) bool)`.
- `CircuitBreaker` — closed, open, half-open. After `cooldown` it admits
  **exactly one** probe; concurrent callers get `ErrCircuitOpen` until the
  probe reports. A probe that never reaches a verdict (limiter refusal,
  cancelled `ctx`, a panic in `fn`) hands its slot back, so the breaker cannot
  stay half-open forever. `Release()` is the primitive for a caller using
  `Allow` directly. Defaults: `DefaultThreshold` (5), `DefaultCooldown` (30s),
  starting points, not measured against any real MCP upstream.
- `RateLimiter` — a sliding-window call count (`Allow`, `Wait`, `WaitTime`,
  `Available`). A rejected `Allow` consumes no budget.
- `Guard.Do` never counts a caller's own `context.Canceled` as an upstream
  failure. An expired `ctx` deadline does count, since a slow upstream is a
  failing one. The default classifier counts every other non-nil error. For
  `Pool.CallTool` that is nearly right: tool-level failures arrive as a
  `CallToolResult` with `IsError` and a nil error, so Go errors are
  connection failures, timeouts, dial errors and JSON-RPC protocol errors
  (and `Pool`'s "is not registered" error). Use `WithFailureClassifier` to
  exempt the ones that say nothing about the upstream.

```go
pool := client.NewPool(client.WithIdentity("myapp", "1.0.0"))
_ = pool.Register("flaky", client.ServerConfig{Transport: client.TransportStdio, Command: "flaky-mcp"})

g := clientguard.New(
	clientguard.WithCircuitBreaker(5, 30*time.Second),
	clientguard.WithRateLimit(60, time.Minute, clientguard.RateLimitReject),
)

res, err := clientguard.Do(ctx, g, "flaky", func(ctx context.Context) (*mcpsdk.CallToolResult, error) {
	result, _, err := pool.CallTool(ctx, "flaky", "search", args, client.WithCallTimeout(10*time.Second))
	return result, err
})
if errors.Is(err, clientguard.ErrCircuitOpen) {
	// the upstream is being given a break; fail fast rather than dial again
}
```

`clientguard` compatibility: a new package; nothing existing changes, and it
adds no `go.mod` dependency. Pre-1.0: minor releases may change exported API.

`clientguard` is out of scope for: per-call timeouts and connection retry
(`client`'s `WithCallTimeout` and `RetryPolicy`), a backoff delay between
retries (`client` reconnects immediately; that is a gap in `client`, not
something a wrapper can add), profile, policy or visibility (a gateway's job),
and a `Pool` facade (the two-line wrap above is the API).

## Notes

- `Config.MaxBytes` and `Config.MaxTokens` are enforced, but only when the
  caller sets them to a positive value; zero means no cap and the `Default*`
  constants are never applied implicitly. `Apply`, `ApplyPage` and `Seal`
  trim to the largest prefix whose marshaled `Envelope` fits (at least one
  item), and `truncatedBy` names the knob that bound.

## Dependencies

`budget`, `staleness`, `supervise`, and `clientguard` use only the Go standard library.
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

## Test helper (`mcptest`)

`github.com/hollis-labs/go-mcp/mcptest` is the portfolio's minimum-profile
check for a server built on `server`: `Connect` wires it to an in-process
client over the SDK's in-memory transport and completes the MCP handshake. A
server for which it succeeds meets the minimum profile (ruling Q14a,
"`mcptest.Connect` only"); there is no broader profile. It replaces the
connect/defer-close boilerplate that tests otherwise hand-roll.

```go
func TestServerConnects(t *testing.T) {
	srv := server.NewServer("demo", "1.0.0")
	cs, cleanup := mcptest.Connect(t, srv) // fails the test on any handshake error
	defer cleanup()
	_ = cs // a *mcpsdk.ClientSession; use it for your own tool-call assertions
}
```

Options: `WithClientIdentity(name, version)` (default `mcptest`/`0.0.0`),
`WithClientOptions(*mcpsdk.ClientOptions)` for elicitation or sampling
handlers, `WithContext(ctx)` and `WithHandshakeTimeout(d)` (default 10s) to
bound the handshake. `TestingT` is just `Helper()` and `Fatalf`, so
`*testing.T` fits without being required.

Out of scope: `Connect` deliberately asserts nothing about tools,
annotations, schemas or behavior; use `LintCatalog` for catalog checks and
your own assertions on the returned session for behavior. It uses the primary
in-memory transport, not `compat`, stdio or HTTP, and is unrelated to
`mark3labs/mcp-go`'s package of the same name.

Compatibility: additive, new in the next release; it imports `server` and the
official SDK's `mcp` package and adds no dependency. Any change to what
counts as a passing handshake is a breaking change to the floor it defines.
