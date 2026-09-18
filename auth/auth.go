package auth

import (
	"net/http"

	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
)

// Provider is go-mcp's AuthProvider seam. Both the lightweight default
// ([StaticProvider]) and a future full OAuth 2.1 + CIMD provider implement
// it, so a server can be built against Provider without caring which one is
// configured.
type Provider interface {
	// Verifier returns the SDK-compatible token verifier this provider uses
	// to authenticate an incoming bearer token.
	Verifier() sdkauth.TokenVerifier

	// Options returns the bearer-token enforcement options (required scopes,
	// resource metadata URL, expiration/clock-skew handling) to apply
	// alongside Verifier. May return nil to accept the SDK's zero-value
	// defaults.
	Options() *sdkauth.RequireBearerTokenOptions
}

// HTTPMiddleware wraps handler with bearer-token enforcement from p, via the
// official SDK's auth.RequireBearerToken. A nil Provider is a passthrough:
// the handler is returned unwrapped, so a server that never configures a
// Provider is never forced to stand up auth infrastructure.
//
// Apply this around the Streamable HTTP handler that serves an MCP server;
// stdio transport has no HTTP layer to wrap and is unaffected.
func HTTPMiddleware(p Provider) func(http.Handler) http.Handler {
	if p == nil {
		return func(h http.Handler) http.Handler { return h }
	}
	return sdkauth.RequireBearerToken(p.Verifier(), p.Options())
}
