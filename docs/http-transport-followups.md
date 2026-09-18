# HTTP Transport Followups

As of the go-mcp v2 core rewrite (2026-09-17), `transport/http` is a thin
`originGuard` wrapper around the official SDK's
`mcp.NewStreamableHTTPHandler`, in stateless mode (2026-07-28 / SEP-2567).
JSON and SSE responses, `_meta` handling, protocol-version negotiation,
`server/discover`, and request cancellation are the SDK's responsibility now,
not go-mcp's -- the hand-rolled implementation and its header-validation
logic (`Mcp-Method`/`Mcp-Name` cross-checks) were removed rather than ported,
since the SDK doesn't have an equivalent surface to validate against. Most of
the items previously tracked here are accordingly moot; what's left:

## Next slice

1. Add a first-class progress token helper.
   Tools still pick their own progress token values when calling
   `server.NotifyProgress`. Shared helpers should generate stable
   request-scoped tokens and make progress emission less manual.

2. Add a real remote-client interoperability harness.
   Keep the local HTTP smoke tests (`transport/http/handler_test.go`, which
   now exercise the real Streamable HTTP client/server round trip via the
   official SDK), but also add an opt-in harness that probes a live remote
   MCP client path -- particularly whether it negotiates 2026-07-28 cleanly,
   the accepted-but-unverified risk this rewrite carries per the ADR.

3. Bridge daemon-routed progress across Cerberus socket RPC.
   In-process MCP handlers can stream stage-level notifications directly via
   `server.NotifyProgress`/`NotifyMessage`, but socket-backed MCP subprocesses
   only see coarse tool-wrapper progress. If Cerberus keeps the daemon as the
   execution authority, the socket layer needs a first-class progress/event
   channel.

## Non-goals for now

- App-specific tool descriptions or tool catalogs
- Provider-specific progress semantics
- Authentication -- tracked separately under the pluggable `AuthProvider` seam
  (Torque `CW-20260917-0031`/`-0032`), not transport/header correctness here
