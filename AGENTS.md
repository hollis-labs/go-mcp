# go-mcp

Shared utilities for building Model Context Protocol tool servers, rebuilt on
the official `github.com/modelcontextprotocol/go-sdk`, targeting the
2026-07-28 spec (`adr_go-mcp-official-sdk-consolidation`): response budgeting
and truncation, a server core with strict typed tool contracts, an HTTP
transport, a pluggable auth seam, argument sanitization, and backward-compat
adapters. It is not an MCP client and not a full protocol implementation — it
is the pieces that were being rewritten in every server.

## Start Here

- `README.md` lists what each package currently exposes.
- `budget/envelope.go` and `budget/budget.go` own the list envelope and
  truncation; `budget/tokens.go` owns the token estimate; `budget/errors.go`
  owns the app-owned protocol error-code taxonomy.
- `server/server.go` owns tool registration, dispatch and cancellation as a
  thin wrapper over the SDK's `*mcp.Server`; `server/schema.go` owns strict
  tool schemas.
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
- `supervise/` — dependency-free primitives (`Policy` backoff schedule,
  `ClassifyExit`, `Tail` redacted stderr buffer) for a product's own
  child-process supervision loop; owns no lifecycle, same boundary as
  `staleness/`.
- `docs/http-transport-followups.md` records known gaps in that transport.

## Commands

```bash
gofmt -l .
go vet ./...
go test -race -count=1 ./...
```

There is no CI workflow and no Makefile in this repo, so these are the only
gate.

## Boundaries

**No longer stdlib-only overall** — `server`, `transport/http`, `auth`,
`sanitize` and `compat` depend on `github.com/modelcontextprotocol/go-sdk`
(the whole point of the SDK-consolidation rewrite). Only `budget`,
`staleness`, and `supervise` remain dependency-free; don't add an SDK import
to any of the three without a real reason, since that's the one boundary
this rewrite deliberately kept.

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
cannot blow a context window.
