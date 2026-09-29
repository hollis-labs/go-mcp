package client

import (
	"context"
	"maps"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// RetryPolicy chooses when a single CallTool may be retried after its
// connection is torn down and re-dialed.
type RetryPolicy uint8

const (
	// RetryDefault retries up to WithRetries(n) times after an error
	// IsRecoverableError accepts. This is the package's behavior when no
	// policy is set anywhere.
	RetryDefault RetryPolicy = iota
	// RetryNever never retries the call. Use it for a non-idempotent tool
	// when the caller cannot prove the request was not delivered.
	RetryNever
	// RetryIfUnsent retries (up to max(n, 1) times) only when
	// IsProvablyUnsent reports the request never reached the wire, so a
	// retry cannot double-execute the tool.
	RetryIfUnsent
)

// callConfig is the resolved per-call settings.
type callConfig struct {
	meta     map[string]any
	retry    RetryPolicy
	retrySet bool
	timeout  time.Duration
}

// CallOption tunes a single CallTool call.
type CallOption func(*callConfig)

// WithCallMeta sets protocol _meta on the tools/call request. The map is
// copied when the option is applied, so mutating it afterward changes
// nothing. Options merge; a later key wins. A nil or empty map is a no-op.
//
// The package does not interpret the keys. A caller that wants an
// idempotency key or a trace carrier picks its own key names:
//
//	client.WithCallMeta(map[string]any{"myapp/idempotencyKey": key})
func WithCallMeta(meta map[string]any) CallOption {
	return func(c *callConfig) {
		if len(meta) == 0 {
			return
		}
		if c.meta == nil {
			c.meta = make(map[string]any, len(meta))
		}
		maps.Copy(c.meta, meta)
	}
}

// WithCallRetry overrides the retry policy for this call, including a
// Pool-level default set with WithCallRetryPolicy.
func WithCallRetry(p RetryPolicy) CallOption {
	return func(c *callConfig) {
		c.retry = p
		c.retrySet = true
	}
}

// WithCallTimeout bounds each attempt of this call at d, layered on the
// caller's context (it can shorten, never extend, a caller deadline). It
// overrides any WithDefaultCallTimeouts value. d <= 0 is ignored.
//
// The timeout covers the request itself, not the lazy dial or health probe:
// a connection is shared between calls and must not be bound to one call's
// deadline.
func WithCallTimeout(d time.Duration) CallOption {
	return func(c *callConfig) {
		if d > 0 {
			c.timeout = d
		}
	}
}

func (c *Client) resolveCall(ctx context.Context, opts []CallOption) callConfig {
	cc := callConfig{retry: c.opts.callRetry}
	for _, o := range opts {
		if o != nil {
			o(&cc)
		}
	}
	if cc.timeout == 0 {
		if _, hasDeadline := ctx.Deadline(); !hasDeadline {
			if kind, err := normalizeTransport(c.cfg.Transport); err == nil {
				cc.timeout = c.opts.callTimeouts[kind]
			}
		}
	}
	return cc
}

// CallTool calls a tool on the server, connecting first if needed. Options
// tune this call only; with none, behavior is unchanged.
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any, opts ...CallOption) (*mcpsdk.CallToolResult, CallMetadata, error) {
	cc := c.resolveCall(ctx, opts)
	params := func() *mcpsdk.CallToolParams {
		p := &mcpsdk.CallToolParams{Name: name, Arguments: args}
		if len(cc.meta) > 0 {
			p.Meta = mcpsdk.Meta(maps.Clone(cc.meta))
		}
		return p
	}
	return withSession(ctx, c, "call tool "+name, cc.retry, func(sess sdkSession) (*mcpsdk.CallToolResult, error) {
		opCtx := ctx
		if cc.timeout > 0 {
			var cancel context.CancelFunc
			opCtx, cancel = context.WithTimeout(ctx, cc.timeout)
			defer cancel()
		}
		return sess.CallTool(opCtx, params())
	})
}
