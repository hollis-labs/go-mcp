# go-mcp

Shared utilities for building Model Context Protocol tool servers: response
budgeting and truncation, a stdio server core with strict tool schemas, and an
HTTP transport. It is stdlib-only by design, so any MCP server can import it
without inheriting transitive dependencies. It is not an MCP client and not a
full protocol implementation — it is the pieces that were being rewritten in
every server.

## Start Here

- `README.md` lists what each of the three packages currently exposes.
- `budget/envelope.go` and `budget/budget.go` own the list envelope and
  truncation; `budget/tokens.go` owns the token estimate.
- `server/server.go` owns tool registration, dispatch and cancellation;
  `server/schema.go` owns strict tool schemas.
- `transport/http/handler.go` owns the POST transport, origin checks and
  request-context cancellation.
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

Stdlib only. The dependency-free property is the stated reason servers adopt
this module rather than vendoring their own copy; one import undoes it for
every consumer at once.

`tools/list` output is sorted by name and must stay deterministic —
`TestToolsListIsSortedByName` and `TestToolsListSorted`. Clients cache and diff
that listing, so incidental map-iteration order surfaces as spurious churn.

A cancelled tool call must produce no response, not an error response.
`notifications/cancelled` arrives after the client has stopped listening, and
`TestToolCallCancellationSuppressesResponse` guards that the reply is
suppressed rather than written.

The HTTP transport validates `Origin` and rejects header mismatches
(`TestOriginValidation`, `TestHeaderMismatchRejected`). That is a browser-facing
security control, not boilerplate — a locally bound MCP server is otherwise
reachable from any page the user has open.

Budget truncation clamps a caller-supplied limit to the configured maximum
rather than honoring it (`TestApply_LimitClampedToMax`), so a large `limit`
cannot blow a context window.
