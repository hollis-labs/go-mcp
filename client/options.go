package client

import (
	"log/slog"
	"net/http"
	"os"
	"sort"
	"time"
)

// DefaultMaxResponseBytes is a suggested cap, matching Nanite's own prior
// default: generous for a well-behaved server, but bounding how much memory
// a misbehaving or malicious one can force a caller to buffer. It is NOT
// applied automatically -- the package's own default is no cap at all (see
// config.maxResponseBytes's zero value), so that a caller who never asks
// for one (Hadron, today) sees no behavior change. A caller that wants
// this cap opts in explicitly: WithMaxResponseBytes(client.DefaultMaxResponseBytes).
const DefaultMaxResponseBytes = 10 * 1024 * 1024

// defaultProbeInterval matches Hadron's externalClientProbeInterval.
const defaultProbeInterval = 30 * time.Second

// config holds the resolved settings a Pool (and every Client it creates)
// uses. It is built once, from defaultConfig plus every applied Option, and
// then shared read-only by every Client the Pool creates.
type config struct {
	identityName    string
	identityVersion string

	retries       int
	probeInterval time.Duration

	// maxResponseBytes is the default cap a new Client starts with;
	// Client.SetMaxResponseBytes overrides it per-server.
	maxResponseBytes int

	buildCommandEnv func(ServerConfig) ([]string, error)
	buildHTTPClient func(headers map[string]string, timeoutSeconds int) *http.Client

	logger *slog.Logger
}

func defaultConfig() config {
	return config{
		identityName:  "go-mcp-client",
		retries:       1,
		probeInterval: defaultProbeInterval,
		// maxResponseBytes starts unset (0): HTTP/SSE then get the SDK's
		// own DefaultMaxEventSize (16 MiB), and stdio uses the SDK's own
		// CommandTransport (same 16 MiB default, same shutdown sequence) --
		// see stdio.go. A caller opts into a tighter cap explicitly.
		buildCommandEnv: defaultCommandEnv,
		buildHTTPClient: defaultHTTPClientBuilder,
	}
}

// Option configures a Pool at construction time, via NewPool.
type Option func(*config)

// WithIdentity sets the client identity (name/version) advertised to every
// server this Pool connects to, during the MCP initialize handshake. Hadron
// hardcodes this to "hadron"/"dev"; a shared package can't, so every caller
// should set one.
func WithIdentity(name, version string) Option {
	return func(c *config) {
		c.identityName = name
		c.identityVersion = version
	}
}

// WithRetries sets how many times a call is retried after a recoverable
// error invalidates and re-dials the connection. The default, 1, matches
// Hadron's proven behavior (its legacy CallTool path always retries once;
// its workflow-bridge path additionally gates that on an idempotency key --
// a policy decision for that caller to make above this package, not
// something this package enforces). 0 disables retrying entirely; a
// negative value is ignored.
func WithRetries(n int) Option {
	return func(c *config) {
		if n >= 0 {
			c.retries = n
		}
	}
}

// WithProbeInterval sets how often a non-stdio connection is pinged, lazily,
// before the next call is allowed through. stdio connections are never
// probed -- a stdio server's liveness is the local process, and closing on
// any call error already tears it down (see stdio.go). Zero/negative
// values are ignored; the default is 30s.
func WithProbeInterval(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.probeInterval = d
		}
	}
}

// WithMaxResponseBytes sets the default response-size cap every Client this
// Pool creates starts with (Client.SetMaxResponseBytes can override it
// per-server afterward). Values <= 0 are ignored. Unset (the default),
// HTTP/SSE fall back to the official SDK's own DefaultMaxEventSize (16 MiB)
// and stdio uses the SDK's own CommandTransport (same 16 MiB default) --
// see DefaultMaxResponseBytes for a suggested tighter value.
func WithMaxResponseBytes(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.maxResponseBytes = n
		}
	}
}

// WithCommandEnv overrides how a stdio server's subprocess environment is
// built from its ServerConfig. The default inherits the whole host
// environment (os.Environ()) and appends ServerConfig.Env last, matching
// Hadron's flattenEnv/cmd.Environ() behavior exactly. A caller that needs a
// stricter policy -- Nanite's env-allowlist discipline, which inherits
// nothing except explicitly allowlisted names -- supplies its own build
// function here; that policy is app-specific and deliberately does not live
// in this package.
func WithCommandEnv(build func(ServerConfig) ([]string, error)) Option {
	return func(c *config) {
		if build != nil {
			c.buildCommandEnv = build
		}
	}
}

// WithHTTPClient overrides how the *http.Client used for streamable_http and
// sse connections is built from a server's headers and configured timeout.
// The default, headeredHTTPClient, matches Hadron's behavior: a
// whole-request timeout (0 disables it) and a RoundTripper that sets every
// configured header on every outgoing request.
func WithHTTPClient(build func(headers map[string]string, timeoutSeconds int) *http.Client) Option {
	return func(c *config) {
		if build != nil {
			c.buildHTTPClient = build
		}
	}
}

// WithLogger enables diagnostic logging of connect/reconnect/probe-failure
// events. Any header values logged alongside a server name are always
// redacted first (see auth.Redact) -- this package never logs a credential
// value, only the fact that headers are configured for a server.
func WithLogger(l *slog.Logger) Option {
	return func(c *config) {
		if l != nil {
			c.logger = l
		}
	}
}

// defaultCommandEnv is WithCommandEnv's default: inherit the full host
// environment, then append ServerConfig.Env (sorted for reproducible
// debug/test output) so a per-server override wins over an inherited value
// with the same key.
func defaultCommandEnv(cfg ServerConfig) ([]string, error) {
	env := os.Environ()
	if len(cfg.Env) == 0 {
		return env, nil
	}
	extra := make([]string, 0, len(cfg.Env))
	for k, v := range cfg.Env {
		extra = append(extra, k+"="+v)
	}
	sort.Strings(extra)
	return append(env, extra...), nil
}
