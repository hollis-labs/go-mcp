package client

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/hollis-labs/go-mcp/compat"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Transport kind strings, as reported on ServerConfig.Transport and on
// CallMetadata.Transport. These are the canonical, normalized forms;
// normalizeTransport accepts a couple of aliases on the way in (see
// ServerConfig.Transport's doc comment).
const (
	TransportStdio = "stdio"
	TransportHTTP  = "http"
	TransportSSE   = "sse"
)

// ServerConfig describes how to reach one external MCP server. The shape
// mirrors Hadron's ExternalServerConfig, generalized as the shared type
// every consumer of this package registers with a Pool.
type ServerConfig struct {
	// Transport selects the transport kind: "stdio" (the default, when
	// empty), "streamable_http" or "http" (both mean TransportHTTP), or
	// "sse" (the legacy 2024-11-05 transport, served via go-mcp/compat).
	Transport string

	// Command and Args launch a stdio server's subprocess. Env is appended
	// to the subprocess environment last (see WithCommandEnv for how the
	// rest of that environment is built).
	Command string
	Args    []string
	Env     map[string]string

	// URL is the endpoint for streamable_http and sse servers.
	URL string
	// Headers are set on every outgoing request to URL (see WithHTTPClient).
	Headers map[string]string
	// TimeoutSeconds sets the underlying *http.Client's whole-request
	// timeout (covering a streaming response body, not just headers). Zero
	// leaves Go's/the SDK's own default in place.
	TimeoutSeconds int
}

// normalizeTransport maps ServerConfig.Transport's accepted spellings onto
// one of the Transport* constants.
func normalizeTransport(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", TransportStdio:
		return TransportStdio, nil
	case "http", "streamable_http", "streamable-http":
		return TransportHTTP, nil
	case TransportSSE:
		return TransportSSE, nil
	default:
		return "", fmt.Errorf("unsupported transport %q", raw)
	}
}

// dialFunc dials one server, returning a live session and the normalized
// transport kind it connected over. It exists as a seam: tests substitute a
// fake to exercise reconnect/health-probe logic without a real process or
// network.
type dialFunc func(ctx context.Context, name string, cfg ServerConfig, maxResponseBytes int, opts config) (sdkSession, string, error)

// dialSDK is the production dialFunc: it builds an official-SDK client and
// connects it to the transport matching cfg.Transport.
func dialSDK(ctx context.Context, name string, cfg ServerConfig, maxResponseBytes int, opts config) (sdkSession, string, error) {
	kind, err := normalizeTransport(cfg.Transport)
	if err != nil {
		return nil, "", fmt.Errorf("go-mcp/client: server %q: %w", name, err)
	}

	sdkClient := mcpsdk.NewClient(&mcpsdk.Implementation{Name: opts.identityName, Version: opts.identityVersion}, nil)

	switch kind {
	case TransportStdio:
		cs, err := dialStdio(ctx, sdkClient, name, cfg, maxResponseBytes, opts)
		if err != nil {
			return nil, "", err
		}
		return cs, TransportStdio, nil

	case TransportHTTP:
		if strings.TrimSpace(cfg.URL) == "" {
			return nil, "", fmt.Errorf("go-mcp/client: http server %q: url is required", name)
		}
		t := &mcpsdk.StreamableClientTransport{
			Endpoint:   cfg.URL,
			HTTPClient: opts.buildHTTPClient(cfg.Headers, cfg.TimeoutSeconds),
		}
		if maxResponseBytes > 0 {
			t.MaxEventSize = maxResponseBytes
		}
		cs, err := sdkClient.Connect(ctx, t, nil)
		if err != nil {
			return nil, "", fmt.Errorf("go-mcp/client: http server %q: connect: %w", name, err)
		}
		return cs, TransportHTTP, nil

	case TransportSSE:
		if strings.TrimSpace(cfg.URL) == "" {
			return nil, "", fmt.Errorf("go-mcp/client: sse server %q: url is required", name)
		}
		t := compat.NewSSEClientTransport(cfg.URL, opts.buildHTTPClient(cfg.Headers, cfg.TimeoutSeconds))
		if maxResponseBytes > 0 {
			t.MaxEventSize = maxResponseBytes
		}
		cs, err := sdkClient.Connect(ctx, t, nil)
		if err != nil {
			return nil, "", fmt.Errorf("go-mcp/client: sse server %q: connect: %w", name, err)
		}
		return cs, TransportSSE, nil

	default:
		// Unreachable: normalizeTransport already rejected anything else.
		return nil, "", fmt.Errorf("go-mcp/client: server %q: unsupported transport %q", name, cfg.Transport)
	}
}

// defaultHTTPClientBuilder is WithHTTPClient's default, ported from
// Hadron's headeredHTTPClient: a whole-request timeout (0 leaves it unset)
// plus a RoundTripper that sets every configured header on every outgoing
// request.
func defaultHTTPClientBuilder(headers map[string]string, timeoutSeconds int) *http.Client {
	c := &http.Client{}
	if timeoutSeconds > 0 {
		c.Timeout = time.Duration(timeoutSeconds) * time.Second
	}
	if len(headers) > 0 {
		c.Transport = &staticHeaderRoundTripper{headers: cloneStringMap(headers), base: http.DefaultTransport}
	}
	return c
}

// staticHeaderRoundTripper sets a fixed set of headers on every outgoing
// request -- the mechanism behind a server's static API-key/bearer-token
// configuration, since the SDK's client transports take only an *http.Client
// and expose no per-request header hook of their own.
type staticHeaderRoundTripper struct {
	headers map[string]string
	base    http.RoundTripper
}

func (rt *staticHeaderRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	for k, v := range rt.headers {
		req.Header.Set(k, v)
	}
	return rt.base.RoundTrip(req)
}

func cloneStringMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
