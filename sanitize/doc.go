// Package sanitize cleans malformed Anthropic tool-call XML that leaks into
// MCP free-text parameter VALUES.
//
// Some agent harnesses occasionally emit a stray `</PARAM_NAME>` close-tag —
// or a full `<parameter name="OTHER">…</parameter>` block — inside the value
// of a free-text MCP tool-call argument. The MCP server parses the call
// correctly, but stores the polluted JSON. This package detects four common
// pollution shapes and produces a cleaned args map that callers can hand to
// their normal handler logic.
//
// This package folds in go-mcp-sanitize (github.com/hollis-labs/go-mcp-sanitize)
// as go-mcp's sanitize subpackage, per the go-mcp v2 consolidation ADR. Its
// detection logic below is unchanged; only [Middleware] was rewritten, from
// a github.com/mark3labs/mcp-go ToolHandlerFunc wrapper to an
// github.com/modelcontextprotocol/go-sdk/mcp.Middleware.
//
// # Detection patterns
//
// Applied per field, in order:
//
//  1. Trailing self-named close-tag: a value ending in `</PARAM_NAME>` (or
//     followed only by whitespace / short markup) is truncated at the marker.
//  2. Leaked sibling `<parameter>` block: a value containing
//     `<parameter name="OTHER">CONTENT</parameter>` (including the open-only
//     tail variant) has the markup removed; CONTENT is injected into
//     args["OTHER"] only when that slot is empty/missing.
//  3. Trailing generic close-tag: a stray `</xxx>` near the tail with no
//     balancing open-tag within 64 characters earlier is stripped.
//  4. Tags-array recovery: a "tags" string field is parsed as JSON; if that
//     fails, patterns 1-3 are applied first and the parse is retried.
//
// # Side-effects
//
// [Sanitize] and [CleanFreeText] are zero-side-effect: no logging, no I/O,
// no globals. Telemetry is the responsibility of the optional [Middleware]
// adapter, which emits a single warn-level slog line per cleaned call
// (clean calls are silent).
package sanitize
