# go-mcp

Shared utilities for building Model Context Protocol tool servers, rebuilt on
the official `github.com/modelcontextprotocol/go-sdk`, targeting the
2026-07-28 spec (`adr_go-mcp-official-sdk-consolidation`): response budgeting
and truncation, a server core with strict typed tool contracts, an HTTP
transport, a pluggable auth seam, argument sanitization, and backward-compat
adapters, plus a `client` package (a supervised-lite connection pool, since
v0.5.0). It is not a full protocol implementation — it
is the pieces that were being rewritten in every server.

## Start Here

- `README.md` lists what each package currently exposes.
- `budget/envelope.go` and `budget/budget.go` own the list envelope and
  truncation; `budget/tokens.go` owns the token estimate; `budget/{cursor,fit,page}.go` own
  cursors, byte/token fitting and `ApplyPage`/`Seal`; `budget/errors.go`
  owns the app-owned protocol error-code taxonomy.
- `server/server.go` owns tool registration, dispatch and cancellation as a
  thin wrapper over the SDK's `*mcp.Server`; `server/schema.go` owns strict
  tool schemas; `server/middleware.go` (tool/receiving middleware, opt-in
  `WithSanitize`, duplicate policy), `server/annotations.go` (`Behavior`,
  `AnnotationTable`) and `server/guard.go` (`StrictArgs`, `ValidateSchema`)
  own the kit. `args/` holds the named argument accessors (stdlib + `budget`).
- `transport/http/handler.go` wraps the SDK's stateless Streamable HTTP
  handler, with an origin allowlist enforced ahead of it.
- `auth/` — the `Provider` seam (`Verifier`/`Options`) plus `StaticProvider`,
  a lightweight bearer-token default. A nil `Provider` is a passthrough — auth
  is opt-in, never mandatory.
- `sanitize/` — the former `go-mcp-sanitize` module, folded in as an
  `mcp.Middleware` that cleans `tools/call` arguments before they reach a
  handler.
- `compat/` — isolated, independently deletable backward-compat adapters: an
  SSE client transport and an explicit legacy protocol-version negotiation
  wrapper. Nothing in `server`/`transport/http`/`auth`/`sanitize` depends on
  this package; it exists only for peers that haven't moved to 2026-07-28.
- `supervisedstdio/` — the proactive supervisor for one stdio MCP child,
  composing `supervise` (Policy, ClassifyExit, Tail) over the SDK. It imports
  nothing else from this module: not `client`, `clientguard` or `server`. Its
  tests re-exec the test binary as a disposable MCP server (`TestFixtureProcess`)
  and drive the backoff/stable/shutdown clocks through a fake (`fakeClock`), so
  the schedule is asserted exactly.
- `supervise/` — dependency-free primitives (`Policy` backoff schedule,
  `ClassifyExit`, `Tail` redacted stderr buffer) for a product's own
  child-process supervision loop; owns no lifecycle, same boundary as
  `staleness/`.
- `clientguard/` — dependency-free per-key circuit breaker and call-rate
  limiter (`Guard`, `Do[T]`) wrapped around a caller's own upstream call, e.g.
  `client.Pool.CallTool`; imports nothing from this module. Its `Guard` is the
  call-admission guard, not `server/guard.go`.
- `skills/` — the progressive-discovery skills tool (`Register`, `Source`,
  `MapSource`, `FSSource`), built on `server` and `budget`. A synthesis of four
  apps' independent implementations; richer sources (frontmatter, ranking,
  layered discovery) implement `Source` in the app rather than growing this
  package. `server.LintCatalog`'s `WithExpectedNames` is the golden-tool-list
  check that goes with it.
- `mcptest/` — test helper: `Connect` runs the in-memory handshake against a
  `server.Server` and asserts nothing else (no tool or annotation checks; that
  is `server.LintCatalog`'s job). Imports `server` and the SDK only.
- `docs/http-transport-followups.md` records known gaps in that transport.

## Commands

```bash
gofmt -l .
go vet ./...
go test -race -count=1 ./...
```

CI (`.github/workflows/check.yml`, config in `.golangci.yml`) runs these plus
`golangci-lint`, `go mod verify` and `govulncheck`; there is no Makefile, so these are the only
local gate.

## Boundaries

**No longer stdlib-only overall** — `server`, `transport/http`, `auth`,
`sanitize`, `compat` and `supervisedstdio` depend on `github.com/modelcontextprotocol/go-sdk`
(the whole point of the SDK-consolidation rewrite). Only `budget`,
`staleness`, `supervise`, and `clientguard` remain dependency-free; don't add an
SDK import to any of the four without a real reason, since that's the one
boundary this rewrite deliberately kept.

`tools/list` output is sorted by name and must stay deterministic —
`TestToolsListIsSortedByName` and `TestToolsListSorted`. Clients cache and diff
that listing, so incidental map-iteration order surfaces as spurious churn.
Every registered tool now requires a complete, explicit annotation set
(`readOnlyHint`/`destructiveHint`/`idempotentHint`/`openWorldHint`) — never
optional, never inferred from the tool's name.

A cancelled tool call must produce no response, not an error response.
`notifications/cancelled` arrives after the client has stopped listening, and
`TestToolCallCancellationSuppressesResponse` guards that the reply is
suppressed rather than written.

The HTTP transport validates `Origin` (`TestOriginValidation`) — a
browser-facing security control, not boilerplate, since a locally bound MCP
server is otherwise reachable from any page the user has open. **CORS
preflight (`OPTIONS`) support and the old `Mcp-Method`/`Mcp-Name` header
cross-validation were dropped in the SDK rewrite** — the SDK's Streamable HTTP
handler has no equivalent surface, and full CORS preflight was judged out of
scope (see `CHANGELOG.md`, `Unreleased`). A browser-based caller doing a
cross-origin, non-simple request (any real MCP call — `Content-Type:
application/json` is never CORS-simple) will fail preflight before
`AllowedOrigins` is ever reached. Confirmed 2026-09-18: nothing in this
portfolio calls an MCP HTTP transport from a browser today — every consumer is
a backend or CLI client — so this was accepted rather than fixed. Revisit if
that changes.

Budget truncation clamps a caller-supplied limit to the configured maximum
rather than honoring it (`TestApply_LimitClampedToMax`), so a large `limit`
cannot blow a context window (`Config.MaxLimit` moves that ceiling when a
caller opts in).

`Config.MaxBytes`/`MaxTokens` are enforced only when the caller sets them
(zero = no cap); never apply `DefaultMaxBytes`/`DefaultMaxTokens`
implicitly, that would silently shrink every existing caller's pages. `Apply`
without a cap must stay byte-identical (no `hasMore`/`truncatedBy`).
Paging must always keep at least one item, or a cursor loops forever.

`supervisedstdio` rules, each guarded by a test: a lost transport is not a process
exit and no replacement starts until `Wait` confirms the old process exited,
including after a failed handshake (`TestTransportLostWaitsForTheProcessToExit`,
`TestFailedHandshakeWaitsForTheProcessToExit`); it never sends a process signal
(`TestCloseNeverSendsASignal`; do not add `Kill`/`Signal`, and do not use
`exec.CommandContext`); the restart budget resets only after `Policy.StableFor`
of uptime, never on a successful handshake (`TestFlappingProcessStillExhausts`,
`TestStableUptimeResetsTheBudget`); a call in flight is never replayed
(`TestInFlightCallIsNeverReplayed`). `go-mcp/client`'s reactive stdio transport is
deliberate and is not to be changed to behave like this package.

## `_meta` conventions (portfolio-wide)

Status: ratified convention, not enforced by code. Nothing in this module
checks, lints or rewrites any of it, and existing apps are not required to
comply today. Ratified 2026-09-30 (brief `mcp-meta-conventions`).

1. **`_meta`, never `arguments`, for anything that isn't a tool's own input.**
   Trace context, idempotency keys, provenance/session stamping and any future
   cross-cutting concern belong in the protocol's `_meta` object. A strict tool
   schema (`additionalProperties: false`) rejects anything smuggled into
   `arguments`. Handlers read it with `server.MetaFromContext`; callers write
   it with `client.WithCallMeta` (neither interprets keys).

2. **Two key-naming tiers.**
   - Tier 1, shared-library concerns: bare, underscore-prefixed keys, spelled
     `_traceparent` and `_tracestate` (W3C trace context, owned by go-otel).
     Reserved for a cross-cutting concern owned by a library shared across the
     whole portfolio. W3C `baggage` has no `_meta` key: go-otel does not carry
     it over MCP today, and this convention does not define one.
   - Tier 2, app-specific concerns: `<app>/<camelCase>`, e.g.
     `hadron/idempotencyKey`. Metadata one app's tools define for their own
     callers. New app-specific keys use this form.
   - `tether/alwaysLoad` (`server.AlwaysLoadMetaKey`) has the tier 2 shape and
     is a reference example. It is per-tool catalog metadata published in
     `tools/list`, not a per-call `_meta` key, and stays as is. One oddity, not
     a violation: go-mcp, a shared library, publishes it under an app prefix
     (`tether/`). Do not rename it; a rename is a wire change for every client.
   - `tether.provenance` (dot-separated) predates this convention. It should
     become `tether/provenance` when Tether next touches that key.

3. **SDK-reserved keys are the SDK's; a relaying gateway must not forward them.**
   The official SDK's `io.modelcontextprotocol/*` keys (e.g. `protocolVersion`,
   `clientInfo`, `clientCapabilities`) are attached to every outbound
   `tools/call` by the client session and describe the hop's own client, not
   the next one. A gateway/proxy relaying a call MUST drop every key with that
   prefix from the inbound `_meta` before forwarding, and never relay a
   caller's raw `_meta` verbatim. Its own upstream connection attaches its own.
   Everything else (trace context, `progressToken`, app keys) passes through.
   A gateway that stamps a key of its own (Tether does with provenance) SHOULD
   overwrite or remove any caller-supplied value for that key, so a client
   cannot forge it.

4. **Identity is never in `_meta`.** Caller user id, email and groups travel as
   the `X-Forwarded-User-{Id,Email,Groups}` HTTP headers stamped at the
   gateway, not duplicated into `_meta`. `_meta` is MCP call metadata; identity
   is a transport-layer, gateway-stamped concern with a different trust model.

### Current state in the portfolio

Read from code on 2026-09-30, paths relative to the repos under
`~/dev/hollis-labs/`; not exhaustive (only Tether, Hadron, Torque, go-otel and
go-mcp were checked).

| Where | Key / behavior | Source |
|---|---|---|
| go-otel | writes `_traceparent`, `_tracestate` into whatever map it is given | `libs/go-otel/propagation/propagation.go` (`InjectMCP`) |
| Tether | trace context injected into `params._meta` with go-otel's bare keys, kept bare on purpose; reads `_meta` first with an `arguments` fallback (transition) | `apps/tether/internal/mcpadapter/trace_meta.go` |
| Tether | `tether.provenance` (dot form) in `_meta`, replaced or removed if a caller supplies it | `apps/tether/internal/mcpadapter/provenance.go` |
| Tether | `stripSDKMeta` drops keys with prefix `io.modelcontextprotocol/` before forwarding; other keys are kept | `apps/tether/internal/mcpadapter/proxy.go` |
| Hadron | `hadron/idempotencyKey` in `_meta`; trace context injected into `arguments` (outbound); no inbound extraction found | `apps/hadron/internal/mcpadapter/internal_caller.go` |
| Torque | reads trace context from `arguments` (inbound), exempted from its unknown-argument guard | `apps/torque/internal/mcpadapter/adapter.go` |
| go-mcp | `tether/alwaysLoad` tool-catalog key; `StrictArgs` exempts `_traceparent`/`_tracestate` from the argument check by default | `server/catalog.go`, `server/guard.go` |

Net: three key styles (`_x`, `app.x`, `app/x`) and three trace placements
(`_meta`, outbound `arguments`, inbound `arguments`). Not verified: Nanite and
Tangent (the brief reports Nanite does neither), and any app outside those
checked.

### Out of scope

No code, lint rule, typed accessor or API is added by this convention. Trace
injection into `_meta` is a separate piece of work (brief
`go-otel-mcp-tool-trace`). Identity header design is settled elsewhere.

### Compatibility and migration

Nothing breaks: existing keys and placements keep working. The convention
applies to new keys and to the next time an app touches an existing one.
Moving Hadron's and Torque's `arguments`-based trace propagation to `_meta`,
and renaming `tether.provenance`, are app-owned adoption work, unscheduled
here. While a mixed fleet exists, a receiver moving to `_meta` should keep an
`arguments` fallback (as Tether does) until its callers have moved.
