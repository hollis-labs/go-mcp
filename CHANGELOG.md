# Changelog

All notable changes to this project will be documented in this file.

## Unreleased

### Added (skills)

- New package `skills`: `Register` adds one progressive-discovery tool to a
  `server.Server` (no argument: a `{"items", "meta"}` catalog; with a name: that
  skill's body; unknown name: a `skill_not_found` `*budget.ToolError` that lists
  the available names and names the tool as its `HelpTool`). `Source` is the
  extension point; `MapSource` (ordered index plus bodies in memory) and
  `FSSource` (the top-level `*.md` files of an `fs.FS`, `start-here` first, no
  frontmatter) are the two stock ones; `WithArgName` and `WithTitle` adjust the
  tool. The tool is registered read-only and idempotent through
  `RegisterChecked`, and `Register` verifies the source once and refuses an
  already-registered name.
- Provenance, as for `Prop` in v0.7.0: the pattern already existed four times
  with no shared package (Hadron `hadron_skills`, Tesseract `tesseract_skills`,
  Station `atlas_guide`, Tether `mux_skill_list`/`_broker`/`_get`). This is a
  synthesis of the shape the first three share, not a port. Tether's ranked
  broker and layered project/user discovery are not part of it.

### Added (server)

- `server.WithExpectedNames(names...)`, a `LintOption` for `LintCatalog`: a
  registered tool not in `names` is an "unexpected tool" issue and a name not
  registered is a "missing tool" issue, so a catalog that drifts in either
  direction fails a test that asserts the lint is empty. It generalizes the
  hand-written golden tool-name test Tangent keeps for its 31 tools, and gives
  `CatalogFingerprint` (v0.9.0), which has no adopters, a checked-in
  counterpart that says what changed rather than only that something did. Off
  by default; no change to any existing behavior.

## v0.11.0 — 2026-09-29

### Added (clientguard)

- New package `clientguard`: per-key circuit breaking and client-side
  call-rate limiting, wrapped around a caller's own call, such as
  `client.Pool.CallTool`. Stdlib-only, imports nothing from this module, and
  changes no existing package. This is the call-admission guard, not
  `server/guard.go`.
- `Guard` (`New`, `Do`, `State`, `Available`, `Reset`), the generic
  `Do[T]`, `CircuitBreaker`, `RateLimiter`, `ErrCircuitOpen`,
  `ErrRateLimited`, and the options `WithCircuitBreaker`, `WithRateLimit`
  (`RateLimitReject` / `RateLimitBlock`) and `WithFailureClassifier`. All
  options are opt-in; a bare `New()` is a passthrough.
- The breaker's algorithm is ported from go-llm-contracts (not imported) with
  one difference: half-open admits exactly one in-flight probe. The source
  lets every caller through once it is half-open. A probe that never reaches
  a verdict (limiter refusal, cancelled `ctx`, a panic in `fn`) releases its
  slot, and results from calls admitted before a state change are ignored.
- `RateLimiter` is a sliding-window call count; a rejected `Allow` consumes no
  budget, unlike the source's unconditional `Record`.
- `Guard.Do` never counts a caller's own `context.Canceled` as an upstream
  failure.

## v0.10.0 — 2026-09-29

### Added (server)

- Multi-round-trip tool calls (SEP-2322, protocol 2026-07-28). A handler
  returns `server.InputRequired{Requests, State}` to ask the client for input
  (a form or URL elicitation, sampling, roots) before it can finish, and reads
  the retry's `server.InputResponses(ctx)` and `server.RequestState(ctx)`.
  `adaptHandler` maps `InputRequired` to an `input_required` result, and
  passes the retry's responses and state to the handler. The SDK bridges
  sessions on earlier protocols, so one handler serves both. `RequestState`
  travels through the client, so verify it before trusting it.
- `server.ClientCapabilities(ctx)`: the calling client's capabilities, from
  the request `_meta` on 2026-07-28 and from the session before it. Check it
  to see whether the client understands an input request (for example
  `Elicitation.URL`) before sending one.

## v0.9.0 — 2026-09-29

### Changed

- Added CI (`.github/workflows/check.yml`), in-repo `.golangci.yml`, and `doc.go` for `staleness`, `transport/http` and `examples/list`.

### Added (server)

- Deterministic `tools/list`: `WithToolOrder(pinned...)` (pinned, then
  registration order, then name; also applied by `ToolDefinitions`) and
  `WithToolsListPagination(pageSize, profileOf)` (catalog middleware installed
  innermost, after sanitize and `WithReceivingMiddleware`), with
  `Server.PaginateCatalog` and `Server.CatalogFingerprint`. Cursors reuse
  `budget`'s codec (kind `tools/list`); a stale or cross-profile cursor is
  JSON-RPC invalid params (-32602).
- `Tool` / `ToolDefinition` gain `AlwaysLoad` (published as
  `_meta["tether/alwaysLoad"]`, `AlwaysLoadMetaKey`), `TTLMs` and `CacheScope`
  (folded into each page's `ttlMs` / `cacheScope`); `ToolDefinition` also
  gains `AnnotationsChecked`, set by `RegisterChecked`.
- `LintCatalog` with `WithNameCharset`, `WithMaxNameLength`,
  `WithLintInstructions`, `WithRequireChecked`; `MaxInstructionsLen`,
  `ValidateInstructions`, and `WithInstructions` now panics past the limit.

## v0.8.0 — 2026-09-29

### Changed

- `go` line raised to 1.26.6 (portfolio-wide floor).

### Added (client)

- `CallOption`s on `Pool.CallTool` and `Client.CallTool` (variadic, so
  existing callers compile unchanged): `WithCallMeta` (protocol `_meta`),
  `WithCallRetry` with `RetryDefault` / `RetryNever` / `RetryIfUnsent`, and
  `WithCallTimeout`.
- `IsProvablyUnsent(err)` — public form of Nanite's classifier for the SDK's
  pre-write "client is closing" rejection.
- Pool options `WithCallRetryPolicy`, `WithDefaultCallTimeouts`,
  `WithClientOptions` (the SDK's `ClientOptions` were always passed as nil)
  and `WithToolListChangedHandler`.
- Note: `*Pool` / `*Client` no longer satisfy a hand-written interface that
  declares the old `CallTool` signature.

### Added (budget)

- Opaque, versioned, fingerprint-bound cursors: `EncodeCursor` /
  `DecodeCursor`, `Fingerprint`, `EncodeOffset` / `DecodeOffset`,
  `EncodeKeyset` / `DecodeKeyset` and `Keyset`; errors `ErrInvalidCursor`
  and `ErrCursorMismatch` (wraps `ErrInvalidCursor`).
- `FitPrefix`, `ArrayBytes` and `BytesCap`: exact byte-accounted prefix
  fitting in O(log n), always keeping at least one item so paging terminates.
- `ApplyPage` (in-memory slice with `Page`) and `Seal` (store-windowed page),
  and `Envelope.HasMore` / `NextCursor` / `TruncatedBy` (`limit`, `maxBytes`,
  `maxTokens`; omitted when unset).
- `Config.MaxLimit` overrides the package `MaxLimit` clamp (0 keeps 25).
- `Config.MaxBytes` / `Config.MaxTokens` are now enforced when the caller sets
  a positive value (resolves the open "enforce or remove" follow-up). With
  neither set, `Apply` output is unchanged; the `Default*` constants are
  suggestions only and are never applied implicitly.

### Added (server)

- Tool middleware: `ToolMiddleware`, `WithToolMiddleware` (first registered is
  outermost; also runs on `Server.CallTool`) and `WithReceivingMiddleware`.
- `WithSanitize(logger)`: explicit opt-in install of `sanitize.Middleware`.
  `NewServer` installs nothing by default; the default lives in one unexported
  constant. A nil logger uses the `WithLogger` logger, else stderr.
- `WithDuplicateTools` (`DuplicateReplace` default, `DuplicatePanic`,
  `DuplicateRecord`) and `Server.RegistrationErrors`.
- Annotation policy: `Behavior` (`Reads`, `Writes`, `Destroys`, `OpenWorld`,
  `Idempotent`), `Server.RegisterChecked`, `WithBehaviorRequired`,
  `AnnotationTable` with `UnknownPolicy`, `CautiousAnnotations`,
  `ValidateAnnotations`.
- `StrictArgs` (unknown and missing argument guard with nearest-name
  suggestions, returning a `*budget.ToolError`) and opt-in `ValidateSchema`
  (jsonschema-go). Options: `WithTransportKeys`, `WithTransportPrefix`,
  `WithStripTransportKeys`, `WithRetiredArgs`, `WithErrorCode`,
  `WithViolationHandler`.
- `doc.go` for the package.
- Note: `Option` is now `func(*options)`; a hand-written
  `func(*mcpsdk.ServerOptions)` no longer converts to it. `google/jsonschema-go`
  moves from indirect to direct in `go.mod`.

### Added (args)

- New package `args`: named argument accessors (`String`, `Trimmed`,
  `NonBlank`, `Bool`, `Float`, `Int`, `IntClamped`, `PositiveInt`, `Whole`,
  `Strings`, `NonBlankStrings`, `LenientInt`, `LenientFloat`, `LenientBool`)
  and `Require`. Standard library and `budget` only.

### Fixed (compat)

- `NewSSEClientTransport`: the sanitizing reader now hands out at most one
  event block per `Read`. The SDK reads the `endpoint` event through a
  throwaway `bufio.Reader` and then starts a second reader on the same body,
  discarding whatever the first buffered; when the `endpoint` and the first
  `message` arrived together, `Read` blocked until the caller's deadline
  (`TestNewSSEClientTransport_EndToEnd` failed ~60% of runs).

## v0.7.1 — 2026-09-18

### Fixed (client)

- A stdio subprocess's lifetime is no longer tied to the per-call context
  (`exec.CommandContext`), so a call ending no longer kills the shared server.

## v0.7.0 — 2026-09-18

### Added

- `server` — `Prop` and its builders (`StringProp`, `StringEnumProp`,
  `NumberProp`, `IntegerProp`, `BooleanProp`, `ArrayProp`,
  `StringArrayProp`, `ObjectProp`) plus `InputSchema(props ...Prop)`, the
  property-level counterpart to `ObjectSchema`. Surveyed eight apps
  (Tether, Hadron, Tesseract, Torque, fragments-engine, Stack Explorer,
  NIL, ...) before designing this: each independently wrote a near-identical
  `strProp`/`numProp`/`boolProp`-shaped helper after migrating off
  mark3labs/mcp-go, whose typed `mcp.WithString`/`WithNumber`/`WithBoolean`/
  `Required` builder chain go-mcp's raw `any` `InputSchema` has no
  equivalent for. This is a synthesis of the richest parts of three real
  designs (Tesseract's required-flag-on-the-definition shape, Tether's
  fuller type coverage including enum/array/object, NIL's `integer` vs
  `number` distinction), not a straight port of any single one -- so
  existing per-app helpers are not source-compatible with it and each app
  adopting it does a small mechanical rename, not a no-op swap.

## v0.6.0 — 2026-09-18

### Added

- `supervise` — a new dependency-free package holding the primitives behind
  Tether's own proactive stdio-upstream supervisor
  (`internal/mcpadapter/client_pool.go` and `upstream_stdio.go`),
  generalized for reuse by any product supervising a child process, not
  only an MCP stdio upstream. `Policy` is a bounded exponential backoff
  schedule with a stable-for reset window; `ClassifyExit` classifies a
  process's terminal `os.ProcessState` into clean/error/signal; `Tail` is a
  bounded, concurrent-safe, redacted stderr ring buffer. Like `staleness`,
  it owns no lifecycle: it never spawns, signals, waits on, or restarts a
  process itself -- the caller's own supervision loop reads state from
  these primitives and decides what to do, per the same ownership boundary
  ADR `adr_mcp_staleness_detection_ownership` (CW-20260912-0106)
  established for `staleness`. Tracks CW-20260918-0021.

## v0.5.0 — 2026-09-18

### Added

- `client` — a new package for connecting OUT to external MCP servers over
  stdio, streamable HTTP, or legacy SSE (CW-20260918-0013). go-mcp was
  server-only by design; Hadron and Nanite each independently built the
  same connect/reconnect/health-probe shape against the raw SDK, and
  Tether built a heavier version of the same problem against a different
  SDK. `Pool`/`Client` follow Hadron's proven reactive shape (dial on
  first use, one retry on a recoverable error, a lazy 30s non-stdio
  health probe) rather than Tether's proactive supervisor. Response-size
  capping (`WithMaxResponseBytes`/`Client.SetMaxResponseBytes`) is
  opt-in and changes no existing caller's behavior by default; leak-
  prevention-on-error (any connection-shaped error tears the connection
  down before deciding whether to retry) is unconditional.

### Fixed

- `compat` — `NewSSEClientTransport`'s sanitizing reader now bounds how
  much of one not-yet-terminated SSE event block it will buffer
  (`maxPartialEventBytes`, 1 MiB) while scanning for the blank-line
  terminator. A server that never sent one could previously grow that
  buffer without limit, exhausting memory before the downstream
  transport's own `MaxEventSize` cap ever saw a complete block to check.

## v0.4.3 — 2026-09-18

### Added

- `server` — `MetaFromContext(ctx) map[string]any`, reading a tool call's
  protocol-level `_meta` object (`WithMeta` installs it; `adaptHandler` does
  so automatically for every protocol-served call). `ToolHandler`'s
  simplified `(ctx, args map[string]any)` signature had no way to see
  anything outside the tool's own arguments, but a caller-attached
  out-of-band field sent via `_meta` -- Hadron's `hadron/idempotencyKey`
  convention, discovered while porting its Torque bulk-create end-to-end
  fixture -- has nowhere else to go. Direct in-process `Server.CallTool`
  bypasses the protocol layer entirely, so it carries no `_meta`;
  `MetaFromContext` returns nil there rather than a stale value.

### Notes

- Mirrors the existing `WithNotifier`/`Notify` context-injection pattern
  rather than introducing a new mechanism.

## v0.4.2 — 2026-09-18

### Added

- `budget` — `StructuredError`, an interface (`error` + `ToolErrorContent()
  any`) completing v0.4.0's error-contract work for a caller whose error
  shape doesn't fit `ToolError`'s required fields. Prompted by a real
  second consumer, mid-port: Hadron's workflow-operation error envelope
  deliberately omits a human-readable message for data-minimization
  reasons ("Message text is intentionally not transported"), so it can't
  satisfy `ToolError.Message`, but still needs StructuredContent+IsError
  treatment for its own fully custom shape.
- `server` — `adaptHandler` reports a returned `budget.StructuredError` the
  same way it reports a `*budget.ToolError`: `ToolErrorContent()`'s value
  in StructuredContent (and mirrored text), `IsError` set.

## v0.4.1 — 2026-09-18

### Added

- `server` — `Server.RemoveTools(names ...string)`, symmetric with
  `RegisterTool`. Needed for a caller (Hadron's dynamic per-session-derived
  workflow tools, the first real consumer) to unmount a previously
  registered tool without leaving go-mcp's own `defs`/`handlers`
  bookkeeping stale -- `SDKServer().RemoveTools()` alone would desync
  `ToolDefinitions()`/`CallTool()` from the wire-visible set.

## v0.4.0 — 2026-09-18

Per `project/atlas/knowledge/mcp-acp-shared-contract-and-adoption-notes`'s
tool-contract area ("typed outputs and machine-readable errors with
correction/retry guidance"), reviewed with Chrispian ahead of Hadron's port
-- the first real consumer of this surface, so this is the cheapest point to
land it. Every portfolio tool already returns JSON; until now that JSON was
always embedded as text a client had to re-parse, because `ToolHandler`
could only ever produce a `TextContent` block. This is a breaking change to
`ToolHandler`, but breaks nothing running in production: no application has
adopted the `server` package's tool-registration surface yet.

### Changed

- `server` — `ToolHandler` is now `func(ctx, args map[string]any) (any,
  error)`, was `(string, error)`.
  - A `string` result is used verbatim as text content, unchanged from
    before.
  - Any other value is JSON-marshaled into `CallToolResult.StructuredContent`
    (per SEP-2106) *and* mirrored as JSON text content, so a client reading
    either gets the same data. A marshal failure is reported as a
    protocol-level error (`ErrCodeInternal`), not folded into tool result
    content -- it's go-mcp's own inability to serialize what the tool
    produced, not a tool-execution failure.
  - A returned `*budget.ToolError` (see below) keeps its full structured
    shape in StructuredContent instead of being collapsed to
    `err.Error()`. Any other error is reported exactly as before: its
    plain `Error()` string, `IsError=true`, no StructuredContent.
  - `Server.CallTool` (the direct in-process/test path) now returns `(any,
    error)` to match, returning the handler's raw value unconverted.
- `Tool` gains `Title` (optional display name) and `OutputSchema` (optional
  JSON Schema for the StructuredContent shape) -- both direct official-SDK
  `Tool` fields that were previously dropped. `ToolDefinition` carries both
  through too.
- `budget.ToolError` is now a struct (`Code`, `Message`, `Field`,
  `Retryable`, `NextStep`, `HelpTool`), implementing `error`, built with
  `NewToolError(code, message)` and `With*` chain methods -- was a function
  returning a bare `{"error","message"}` JSON string (a key-name mismatch
  with `ProtocolError`'s `{"code","message","data"}`, fixed along the way).
  The point of the new fields: the caller already knows the error and its
  context, so giving the calling agent a concrete next step -- not just
  what went wrong -- costs nothing extra. `NextStep` is where that goes.
- `budget.ToolJSON` is unchanged, but is now the secondary path: prefer
  returning a value directly from a `ToolHandler` over pre-marshaling it,
  since only the former gets StructuredContent.

### Notes

- This does not touch the tool-contract area's other, still-draft
  requirements (cursor pagination, bulk partial-success, stable
  action-oriented naming, retired-argument guidance) -- those remain under
  portfolio review, not adopted here.

## v0.3.2 — 2026-09-18

Per `project/atlas/knowledge/mcp-acp-shared-contract-and-adoption-notes`:
`Instructions`/`InitializedHandler`/`CompletionHandler` (v0.3.1) fall under
that note's "initialization/capability negotiation" MCP contract area, which
it names as shared-library-owned. This release rounds out the rest of that
same area with the remaining unreached `ServerOptions` fields -- exact
protocol-version negotiation, capability overrides, and observability --
using the identical Option mechanism. It stops there: the note's other
contract areas (tool-definition conventions, cursor pagination, bulk
partial-success, retired-argument guidance) remain an explicit draft
profile, not an accepted standard, and are not touched by this release.

### Added

- `server` — four more `Option`s for `NewServer`, all populating
  otherwise-unreachable `ServerOptions` fields:
  - `WithKeepAliveFailureThreshold(int)` — pairs with `WithKeepAlive`;
    consecutive ping failures tolerated before a session is closed.
  - `WithCapabilities(*mcp.ServerCapabilities)` — overrides the server's
    default/inferred advertised capabilities.
  - `WithSupportedProtocolVersions([]string)` — narrows the MCP protocol
    versions this server advertises and accepts.
  - `WithLogger(*slog.Logger)` — enables logging of server activity.

## v0.3.1 — 2026-09-18

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
