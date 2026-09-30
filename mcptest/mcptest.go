package mcptest

import (
	"context"
	"time"

	"github.com/hollis-labs/go-mcp/server"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// DefaultHandshakeTimeout bounds the handshake unless WithHandshakeTimeout or
// a shorter WithContext deadline says otherwise.
const DefaultHandshakeTimeout = 10 * time.Second

// Default client identity advertised by Connect.
const (
	DefaultClientName    = "mcptest"
	DefaultClientVersion = "0.0.0"
)

// TestingT is the slice of *testing.T that Connect needs, so a caller's own
// failure reporting is not forced through *testing.T.
type TestingT interface {
	Helper()
	Fatalf(format string, args ...any)
}

type connectConfig struct {
	name, version string
	clientOpts    *mcpsdk.ClientOptions
	ctx           context.Context
	timeout       time.Duration
}

// ConnectOption customizes Connect.
type ConnectOption func(*connectConfig)

// WithClientIdentity sets the connecting client's advertised name and version
// (default DefaultClientName / DefaultClientVersion).
func WithClientIdentity(name, version string) ConnectOption {
	return func(c *connectConfig) { c.name, c.version = name, version }
}

// WithClientOptions passes opts to mcpsdk.NewClient, for example elicitation
// or sampling handlers that must exist during the handshake.
func WithClientOptions(opts *mcpsdk.ClientOptions) ConnectOption {
	return func(c *connectConfig) { c.clientOpts = opts }
}

// WithContext sets the parent context of the handshake (default
// context.Background()). Its cancellation or deadline aborts a handshake in
// progress; it does not bound the returned session afterwards.
func WithContext(ctx context.Context) ConnectOption {
	return func(c *connectConfig) {
		if ctx != nil {
			c.ctx = ctx
		}
	}
}

// WithHandshakeTimeout bounds the handshake (default DefaultHandshakeTimeout).
// A value <= 0 removes the extra bound, leaving only WithContext's.
func WithHandshakeTimeout(d time.Duration) ConnectOption {
	return func(c *connectConfig) { c.timeout = d }
}

// Connect wires srv to an in-process client over the SDK's in-memory
// transport pair, completes the MCP handshake on both ends, and returns the
// connected client session plus a cleanup func that closes both sessions
// (safe to call more than once). A failure at any step calls t.Fatalf, closes
// whatever was opened, and returns nil, nil.
//
// This is deliberately the whole "mcptest.Connect only" floor: a passing call
// asserts nothing about the server's tools, annotations or behavior beyond a
// clean connect and handshake. See the package documentation.
func Connect(t TestingT, srv *server.Server, opts ...ConnectOption) (*mcpsdk.ClientSession, func()) {
	t.Helper()
	cfg := connectConfig{
		name: DefaultClientName, version: DefaultClientVersion,
		ctx: context.Background(), timeout: DefaultHandshakeTimeout,
	}
	for _, o := range opts {
		o(&cfg)
	}
	if srv == nil {
		t.Fatalf("mcptest: Connect: nil server")
		return nil, nil
	}

	ctx, cancel := cfg.ctx, context.CancelFunc(func() {})
	if cfg.timeout > 0 {
		ctx, cancel = context.WithTimeout(cfg.ctx, cfg.timeout)
	}
	defer cancel()

	serverT, clientT := mcpsdk.NewInMemoryTransports()
	ss, err := srv.SDKServer().Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatalf("mcptest: server connect: %v", err)
		return nil, nil
	}
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: cfg.name, Version: cfg.version}, cfg.clientOpts)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		_ = ss.Close()
		t.Fatalf("mcptest: client handshake: %v", err)
		return nil, nil
	}
	if cs == nil || cs.InitializeResult() == nil {
		_ = ss.Close()
		if cs != nil {
			_ = cs.Close()
		}
		t.Fatalf("mcptest: handshake returned no initialize result")
		return nil, nil
	}
	return cs, func() {
		_ = cs.Close()
		_ = ss.Close()
	}
}
