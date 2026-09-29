// Package server is a thin wrapper around the official MCP Go SDK
// (github.com/modelcontextprotocol/go-sdk), targeting the 2026-07-28 MCP
// specification. It adds go-mcp's simplified tool-registration surface: an
// untyped handler signature, and a required typed tool-annotation contract
// (rather than annotations left optional or inferred from a tool's name).
//
// # Registering tools
//
// RegisterTool takes a Tool whose four hint fields are stated explicitly.
// RegisterChecked takes a Behavior instead (Reads, Writes, Destroys, plus
// OpenWorld and Idempotent): the zero Behavior is invalid, and Reads and
// Destroys need a reason, so a tool cannot end up with all-false hints by
// forgetting to think about them. WithBehaviorRequired makes plain
// RegisterTool panic. AnnotationTable and CautiousAnnotations serve servers
// that keep their hints in a name-keyed table; ValidateAnnotations flags
// contradictory hints and never flags all-zero ones.
//
// WithDuplicateTools chooses what registering an existing name does: replace
// (the default), panic, or record an error and keep the first.
//
// # Catalog
//
// WithToolOrder and WithToolsListPagination (catalog.go) replace the SDK's
// always-complete, alphabetical tools/list with the server's own catalog:
// pinned, then registration order, then name, in pages bound by fingerprint
// to the catalog and a profile id. Tool.AlwaysLoad, TTLMs and CacheScope
// feed the _meta hint and each page's cache fields. LintCatalog (lint.go) is
// an opt-in check of names, titles, instructions length and annotations.
//
// # Middleware
//
// There are two layers, and they see different things.
//
// Receiving middleware (WithSanitize, WithReceivingMiddleware) is SDK
// middleware around every request. It sees raw JSON arguments before the tool
// is chosen, and it does not run on Server.CallTool. WithSanitize is opt-in:
// it rewrites arguments, so NewServer installs nothing by default. It always
// runs ahead of WithReceivingMiddleware's middleware.
//
// Tool middleware (WithToolMiddleware) wraps each tool's handler when it is
// registered and receives the tool's ToolDefinition, so it can derive per-tool
// state once. It sees arguments after decoding and after receiving
// middleware, and it also runs on Server.CallTool. The first middleware given
// is outermost.
//
// StrictArgs and ValidateSchema are tool middleware. The SDK's raw AddTool
// validates nothing, so without StrictArgs a misspelled argument is dropped
// and the call reports success. StrictArgs refuses unknown and missing
// arguments with a *budget.ToolError naming the field and the nearest
// declared names; ValidateSchema additionally checks value types and is
// stricter than clients that send "50" for a number tolerate.
package server
