// Package httptransport exposes a go-mcp Server over the MCP Streamable
// HTTP transport, per the 2026-07-28 specification, in stateless mode
// (SEP-2567): no Mcp-Session-Id is read or set, and each request is served
// independently. HTTP+SSE, as a dedicated server transport, is not offered;
// Streamable HTTP already carries SSE-formatted responses and notifications
// over the single POST endpoint where the client's Accept header asks for
// them.
package httptransport

import (
	"net/http"
	"strings"

	gmcp "github.com/hollis-labs/go-mcp/server"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// HandlerOptions configures the HTTP handler.
type HandlerOptions struct {
	// AllowedOrigins restricts which Origin header values are accepted. A
	// request whose Origin header is set but not in this list is rejected
	// with 403 Forbidden. If empty, all origins are allowed -- browsers
	// still enforce same-origin unless the deployment adds its own CORS
	// headers.
	AllowedOrigins []string
}

// NewHandler returns an http.Handler exposing server over the MCP
// Streamable HTTP transport.
func NewHandler(server *gmcp.Server, opts HandlerOptions) http.Handler {
	allowed := make(map[string]struct{}, len(opts.AllowedOrigins))
	for _, origin := range opts.AllowedOrigins {
		if origin == "" {
			continue
		}
		allowed[strings.ToLower(origin)] = struct{}{}
	}

	sdkHandler := mcpsdk.NewStreamableHTTPHandler(
		func(*http.Request) *mcpsdk.Server { return server.SDKServer() },
		&mcpsdk.StreamableHTTPOptions{Stateless: true},
	)

	return &originGuard{allowed: allowed, next: sdkHandler}
}

// originGuard enforces HandlerOptions.AllowedOrigins ahead of the SDK
// handler, which has no allowlist concept of its own.
type originGuard struct {
	allowed map[string]struct{}
	next    http.Handler
}

func (g *originGuard) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !g.originAllowed(r) {
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return
	}
	g.next.ServeHTTP(w, r)
}

func (g *originGuard) originAllowed(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" || len(g.allowed) == 0 {
		return true
	}
	_, ok := g.allowed[strings.ToLower(origin)]
	return ok
}
