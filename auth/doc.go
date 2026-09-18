// Package auth is go-mcp's pluggable AuthProvider seam.
//
// [Provider] is the single interface both the lightweight default
// ([StaticProvider]) and any future full OAuth 2.1 + CIMD provider
// implement. It wraps github.com/modelcontextprotocol/go-sdk/auth's own
// bearer-token primitives rather than reimplementing them: [HTTPMiddleware]
// applies a Provider to an http.Handler via the SDK's auth.RequireBearerToken,
// and the resulting token info is available to any downstream MCP receiving
// middleware or tool handler through the SDK's own RequestExtra.TokenInfo.
//
// Auth is opt-in. A nil Provider passed to HTTPMiddleware is a passthrough —
// no server is forced to configure authentication to use go-mcp, and stdio
// transport is untouched by any of this (it keeps the existing local
// process-trust model).
//
// [Redact] and [MergeRedacted] are a separate, general-purpose pair for
// credential-shaped config (e.g. HTTP headers configured for an MCP server
// connection) that a caller stores and later echoes back to a UI or API
// response — grouped here because they answer the same "stop handing
// credentials back in the clear" problem, not because they touch Provider.
package auth
