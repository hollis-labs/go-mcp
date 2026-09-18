package client

import (
	"context"
	"fmt"
	"sync"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// sdkSession is the subset of *mcpsdk.ClientSession this package drives.
// *mcpsdk.ClientSession satisfies it directly -- there is no wrapper
// struct in production use; the interface exists purely as a test seam
// (see dialFunc).
type sdkSession interface {
	CallTool(ctx context.Context, params *mcpsdk.CallToolParams) (*mcpsdk.CallToolResult, error)
	ListTools(ctx context.Context, params *mcpsdk.ListToolsParams) (*mcpsdk.ListToolsResult, error)
	Ping(ctx context.Context, params *mcpsdk.PingParams) error
	Close() error
}

// CallMetadata reports what a call actually did to serve the request --
// observability, not app-owned control flow. Mirrors Hadron's own
// execution.MCPCallMetadata shape, generalized into this package since more
// than one consumer wants it.
type CallMetadata struct {
	Server, Transport string

	ReusedClient bool // the connection already existed; no dial happened
	HealthProbe  bool // the lazy non-stdio health probe ran before this call
	Reconnected  bool // a probe or call failure caused an invalidate+redial

	RetryCount   int // how many times the call itself was retried after a redial
	AttemptCount int // total attempts, including the health probe's own retry
}

// Client is one named connection to an external MCP server: dial-on-first-
// use, one retry on a recoverable error, and (for non-stdio transports) a
// lazy health probe before the next call once the probe interval has
// elapsed. See the package doc for why this shape, not a proactive
// supervisor.
//
// The zero value is not usable; a Pool constructs every Client via
// Register.
type Client struct {
	name string
	cfg  ServerConfig
	opts config
	dial dialFunc

	mu               sync.Mutex
	sess             sdkSession
	transport        string
	lastProbe        time.Time
	maxResponseBytes int
}

func newClient(name string, cfg ServerConfig, opts config, dial dialFunc) *Client {
	return &Client{
		name:             name,
		cfg:              cfg,
		opts:             opts,
		dial:             dial,
		maxResponseBytes: opts.maxResponseBytes,
	}
}

// SetMaxResponseBytes overrides this server's response-size cap. Values <= 0
// are ignored -- the cap exists to close a fixed memory-DoS finding and must
// never be silently disabled by an accidental zero value, matching the
// contract Nanite's own transports already established.
//
// The new value takes effect on the next connect: streamable_http and sse
// caps are set on the transport object at connect time, and a stdio cap
// determines whether the SDK's plain CommandTransport or this package's
// capped IOTransport path is used (see stdio.go) -- neither can be changed
// on an already-open connection. In practice this is not a live-reconfig
// gap: every known caller sets this once, immediately after registering the
// server and before any call has connected it.
func (c *Client) SetMaxResponseBytes(n int) {
	if n <= 0 {
		return
	}
	c.mu.Lock()
	c.maxResponseBytes = n
	c.mu.Unlock()
}

// CallTool calls a tool on the server, connecting first if needed.
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (*mcpsdk.CallToolResult, CallMetadata, error) {
	return withSession(ctx, c, "call tool "+name, func(sess sdkSession) (*mcpsdk.CallToolResult, error) {
		return sess.CallTool(ctx, &mcpsdk.CallToolParams{Name: name, Arguments: args})
	})
}

// ListTools lists the server's tools, connecting first if needed.
func (c *Client) ListTools(ctx context.Context) (*mcpsdk.ListToolsResult, error) {
	res, _, err := withSession(ctx, c, "list tools", func(sess sdkSession) (*mcpsdk.ListToolsResult, error) {
		return sess.ListTools(ctx, &mcpsdk.ListToolsParams{})
	})
	return res, err
}

// Ping checks the connection, connecting first if needed. Callers generally
// don't need this directly -- CallTool and ListTools already run the same
// lazy health probe internally -- it's exposed for a caller that wants to
// force a liveness check on its own schedule.
func (c *Client) Ping(ctx context.Context) error {
	_, _, err := withSession(ctx, c, "ping", func(sess sdkSession) (struct{}, error) {
		return struct{}{}, sess.Ping(ctx, &mcpsdk.PingParams{})
	})
	return err
}

// Close closes the connection, if one is open. It is idempotent.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closeLocked()
}

// SDKSession returns the underlying official-SDK session, for a caller that
// needs SDK functionality this package doesn't wrap (prompts, resources,
// completion). Returns nil when no connection is currently open -- callers
// needing a live session should call CallTool/ListTools/Ping first, or use
// SDKSession only when they already know a connection is open.
func (c *Client) SDKSession() *mcpsdk.ClientSession {
	c.mu.Lock()
	defer c.mu.Unlock()
	cs, _ := c.sess.(*mcpsdk.ClientSession)
	return cs
}

func (c *Client) connectLocked(ctx context.Context) error {
	sess, transport, err := c.dial(ctx, c.name, c.cfg, c.maxResponseBytes, c.opts)
	if err != nil {
		return err
	}
	c.sess = sess
	c.transport = transport
	c.lastProbe = time.Now().UTC()
	return nil
}

func (c *Client) closeLocked() error {
	if c.sess == nil {
		return nil
	}
	err := c.sess.Close()
	c.sess = nil
	c.transport = ""
	return err
}

// maybeProbeLocked runs Hadron's ensureHealthy shape: skip entirely for
// stdio (a local process's liveness is the process; a call failure already
// tears it down), skip if the probe interval hasn't elapsed, otherwise ping
// and -- on a recoverable failure -- invalidate and redial once,
// synchronously, with no backoff, exactly like a call's own retry.
func (c *Client) maybeProbeLocked(ctx context.Context) (probed, reconnected bool, err error) {
	if c.transport == TransportStdio {
		return false, false, nil
	}
	if time.Since(c.lastProbe) < c.opts.probeInterval {
		return false, false, nil
	}

	pingErr := c.sess.Ping(ctx, &mcpsdk.PingParams{})
	if pingErr == nil {
		c.lastProbe = time.Now().UTC()
		return true, false, nil
	}

	c.logf("health probe %q failed: %v", c.name, pingErr)
	// A caller whose own context is already done said nothing about the
	// connection's health -- see withSession's doc comment for why that
	// specifically must not cost the connection.
	if ctx.Err() == nil {
		_ = c.closeLocked()
	}
	if !IsRecoverableError(pingErr) || ctx.Err() != nil {
		return true, false, pingErr
	}
	if err := c.connectLocked(ctx); err != nil {
		return true, false, fmt.Errorf("reconnect after failed probe: %w", err)
	}
	return true, true, nil
}

func (c *Client) logf(format string, args ...any) {
	if c.opts.logger != nil {
		c.opts.logger.Debug(fmt.Sprintf(format, args...))
	}
}

// withSession runs op against c's session, connecting first if needed,
// running the lazy health probe, and retrying once (per c.opts.retries) on
// a recoverable error.
//
// Every error CallTool/ListTools/Ping can return is connection-shaped: a
// tool-level failure comes back as a CallToolResult with IsError set and
// err == nil, never as a Go error from these methods. So any non-nil error
// here always tears the connection down before deciding whether to retry --
// broader than Hadron's literal gate, which only invalidated on a
// recoverable error and could otherwise leave a broken-but-open stdio
// subprocess connected, the exact leak shape Nanite's own transport-leak
// audit fixed. The recoverable-error classifier still gates whether a
// *retry* is attempted; it no longer gates whether the connection is torn
// down.
//
// The one exception: a caller whose own ctx is already done when fn
// returns an error said nothing about the connection's health -- an
// abandoned request looks identical to a broken connection from here, and
// tearing the connection down for it would make every canceled call cost
// the NEXT caller a reconnect too. This matches a real prior behavior
// (Nanite's SSE transport's retireUnlessCallerGaveUp) that a broader
// close-on-any-error rule would otherwise have silently regressed.
func withSession[T any](ctx context.Context, c *Client, op string, fn func(sdkSession) (T, error)) (T, CallMetadata, error) {
	var zero T
	c.mu.Lock()
	defer c.mu.Unlock()

	meta := CallMetadata{Server: c.name}

	if c.sess != nil {
		meta.ReusedClient = true
	} else if err := c.connectLocked(ctx); err != nil {
		return zero, meta, fmt.Errorf("go-mcp/client: %s %q: %w", op, c.name, err)
	}
	meta.Transport = c.transport

	probed, reconnected, err := c.maybeProbeLocked(ctx)
	meta.HealthProbe = probed
	if err != nil {
		return zero, meta, fmt.Errorf("go-mcp/client: %s %q: %w", op, c.name, err)
	}
	if reconnected {
		meta.Reconnected = true
		meta.AttemptCount++
	}

	for attempt := 0; ; attempt++ {
		meta.AttemptCount++
		result, err := fn(c.sess)
		if err == nil {
			return result, meta, nil
		}

		if ctx.Err() == nil {
			_ = c.closeLocked()
		}

		if attempt < c.opts.retries && IsRecoverableError(err) && ctx.Err() == nil {
			meta.RetryCount++
			meta.Reconnected = true
			if dialErr := c.connectLocked(ctx); dialErr != nil {
				return zero, meta, fmt.Errorf("go-mcp/client: %s %q: reconnect after %v: %w", op, c.name, err, dialErr)
			}
			meta.Transport = c.transport
			continue
		}
		return zero, meta, fmt.Errorf("go-mcp/client: %s %q: %w", op, c.name, err)
	}
}
