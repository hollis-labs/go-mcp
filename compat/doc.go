// Package compat holds go-mcp's backward-compatibility adapters: pieces
// that keep working for peers not yet on the 2026-07-28 spec, isolated
// behind an explicit boundary so each is a future delete, not a refactor,
// once no longer needed. Per adr_go-mcp-official-sdk-consolidation
// (CW-20260917-0032), nothing in go-mcp's core depends on this package.
//
//   - [NewSSEClientTransport] interops with upstream MCP servers still on
//     the 2024-11-05 SSE transport.
//   - [Connect] makes the official SDK's silent fallback to the legacy
//     (pre-2026-07-28) initialize handshake explicit, and optionally
//     rejectable.
package compat
