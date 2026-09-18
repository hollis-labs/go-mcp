// Package client is a thin wrapper around the official MCP Go SDK
// (github.com/modelcontextprotocol/go-sdk), for connecting OUT to external
// MCP servers over stdio, streamable HTTP, or legacy SSE.
//
// It exists because go-mcp's server package has no client counterpart: two
// apps (Hadron and Nanite) independently built the same "connect out, retry
// on error, probe for liveness" shape against the raw SDK before this
// package did, and a third (Tether) built a heavier version of the same
// problem against a different SDK entirely. The design here follows
// Hadron's internal_caller.go almost exactly -- a reactive, lazy model
// (dial on first use, one retry on a recoverable error, a 30s lazy health
// probe on non-stdio transports) -- rather than Tether's proactive
// goroutine-per-upstream supervisor, because Hadron's shape already works
// uniformly across all three transports and Tether's does not (it has no
// automatic reconnection at all for SSE/HTTP, by its own design).
//
// Response-size capping and leak-prevention-on-error are first-class
// behaviors here (see WithMaxResponseBytes and the stdio handling in
// stdio.go) rather than left to each app to reimplement -- both are real,
// previously-fixed production-security-audit findings from Nanite's own
// history (a memory-DoS defense and a subprocess/FD-leak fix), not
// speculative hardening. Leak-prevention is unconditional: any error from a
// call closes the connection (see client.go). Capping is opt-in and does
// not change an existing caller's behavior by default -- HTTP/SSE already
// inherit the official SDK's own 16 MiB per-event cap when none is
// requested, and stdio uses the SDK's own CommandTransport (same 16 MiB
// default, same MCP-spec shutdown sequence) until a caller asks for a
// tighter one via WithMaxResponseBytes or Client.SetMaxResponseBytes, at
// which point stdio switches to an IOTransport this package manages itself.
package client
