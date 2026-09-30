// Package mcptest is the minimum-profile test helper for a go-mcp server:
// one call, Connect, wires a *server.Server to an in-process client over the
// SDK's in-memory transport pair and completes the MCP handshake on both
// ends. A server for which Connect succeeds is, by the portfolio's ruling
// (Q14a, "mcptest.Connect only"), sufficient evidence of meeting the minimum
// MCP profile; there is no broader profile.
//
// # What Connect asserts
//
// Only that both ends connect and the client's initialize (or, on protocol
// 2026-07-28, server/discover) exchange completes. It says nothing about the
// server's tools, annotations, schemas, or behavior. Catalog concerns belong
// to server.LintCatalog; a test that wants to call a tool does so on the
// returned session.
//
// # Failure reporting
//
// Connect reports through TestingT (Helper and Fatalf, which *testing.T
// satisfies) instead of requiring *testing.T. On any failure it calls Fatalf
// and returns zero values; it never blocks past the handshake timeout.
//
// # Scope
//
// It uses the primary in-memory transport, not compat, stdio or HTTP, and it
// tests the inbound direction (a server you are building), not client or
// clientguard. It is unrelated to mark3labs/mcp-go's package of the same
// name.
package mcptest
