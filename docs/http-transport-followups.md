# HTTP Transport Followups

The core MCP HTTP transport is now usable for local and remote agent traffic:
JSON responses, SSE responses, request cancellation, `server/discover`, header
validation, and request-scoped notifications are in place. The next followups
should stay in `go-mcp`, not be reimplemented app-by-app.

## Next slice

1. Add a first-class progress token helper.
   Today tools pick their own progress token values. Shared helpers should
   generate stable request-scoped tokens and make progress emission less manual.

2. Tighten modern MCP lifecycle and `_meta` handling.
   The current transport accepts modern protocol hints, but `_meta` is not yet
   a first-class server concept across request parsing, capability negotiation,
   and notification flow.

3. Add a real remote-client interoperability harness.
   Keep the local HTTP smoke tests, but also add one opt-in harness that probes
   a live remote MCP client path so header, SSE, and lifecycle regressions show
   up before release.

4. Bridge daemon-routed progress across Cerberus socket RPC.
   Today in-process MCP handlers can stream stage-level notifications directly,
   but socket-backed MCP subprocesses only see coarse tool-wrapper progress.
   If Cerberus keeps the daemon as the execution authority, the socket layer
   needs a first-class progress/event channel.

## Non-goals for now

- App-specific tool descriptions or tool catalogs
- Provider-specific progress semantics
- Authentication policy beyond transport/header correctness
