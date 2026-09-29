package client

import (
	"context"
	"errors"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func newCallClient(t *testing.T, mutate func(*config), sessions ...*fakeSession) (*Client, *fakeDialer) {
	t.Helper()
	dialer := newFakeDialer(TransportHTTP, sessions...)
	c := newClient("srv", ServerConfig{Transport: "http", URL: "http://example"},
		testConfig(func(c *config) {
			c.probeInterval = time.Hour
			if mutate != nil {
				mutate(c)
			}
		}), dialer.dial)
	return c, dialer
}

func TestIsProvablyUnsent(t *testing.T) {
	// Ported from Nanite's TestIsProvablyUnsent (plain-string errors).
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"exact SDK sentinel text", errors.New("client is closing"), true},
		{"wrapped with a read EOF, the common case", errors.New("client is closing: EOF"), true},
		{"wrapped through the full call chain", errors.New(`tools/call noop: go-mcp/client: call tool noop "Agent Mux": client is closing: EOF`), true},
		{"a plain EOF from an in-flight call", errors.New("EOF"), false},
		{"connection lost", errors.New("connection lost"), false},
		{"connection closed", errors.New("connection closed"), false},
		{"an ordinary context deadline", context.DeadlineExceeded, false},
		{"a tool-level argument error", errors.New(`invalid arguments: missing required field "path"`), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsProvablyUnsent(tt.err); got != tt.want {
				t.Errorf("IsProvablyUnsent(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestWithCallMeta_ReachesParamsCopiedAndMerged(t *testing.T) {
	sess := &fakeSession{}
	c, _ := newCallClient(t, nil, sess)

	meta := map[string]any{"a": 1, "b": "x"}
	_, _, err := c.CallTool(context.Background(), "tool", nil,
		WithCallMeta(meta),
		WithCallMeta(map[string]any{"b": "y", "c": true}), // later wins per key
		WithCallMeta(nil),
	)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	meta["a"] = 99 // must not leak into the request
	got := sess.params[0].Meta
	if got["a"] != 1 || got["b"] != "y" || got["c"] != true || len(got) != 3 {
		t.Errorf("Meta = %v, want a=1 b=y c=true", got)
	}
}

func TestCallTool_NoOptions_NoMeta(t *testing.T) {
	sess := &fakeSession{}
	c, _ := newCallClient(t, nil, sess)
	if _, _, err := c.CallTool(context.Background(), "tool", nil); err != nil {
		t.Fatal(err)
	}
	if sess.params[0].Meta != nil {
		t.Errorf("Meta = %v, want nil", sess.params[0].Meta)
	}
	if sess.ctxDeadlines[0] {
		t.Error("a call with no timeout options got a deadline")
	}
}

func TestRetryPolicyMatrix(t *testing.T) {
	closing := errors.New("client is closing: EOF")
	plain := errors.New("boom")
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name        string
		policy      RetryPolicy
		retries     int // WithRetries
		err         error
		ctx         context.Context
		wantCalls   int
		wantRetries int
	}{
		{"default/closing (not recoverable)", RetryDefault, 1, closing, context.Background(), 1, 0},
		{"default/ErrConnectionClosed", RetryDefault, 1, mcpsdk.ErrConnectionClosed, context.Background(), 2, 1},
		{"default/plain", RetryDefault, 1, plain, context.Background(), 1, 0},
		{"never/ErrConnectionClosed", RetryNever, 1, mcpsdk.ErrConnectionClosed, context.Background(), 1, 0},
		{"never/closing", RetryNever, 1, closing, context.Background(), 1, 0},
		{"ifunsent/closing", RetryIfUnsent, 1, closing, context.Background(), 2, 1},
		{"ifunsent/closing with WithRetries(0) still retries once", RetryIfUnsent, 0, closing, context.Background(), 2, 1},
		{"ifunsent/ErrConnectionClosed (ambiguous)", RetryIfUnsent, 1, mcpsdk.ErrConnectionClosed, context.Background(), 1, 0},
		{"ifunsent/plain", RetryIfUnsent, 1, plain, context.Background(), 1, 0},
		{"ifunsent/closing but ctx canceled", RetryIfUnsent, 1, closing, canceled, 1, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Every attempt fails with err until the last dial's session.
			first := &fakeSession{callToolErrs: []error{tt.err}}
			second := &fakeSession{}
			c, dialer := newCallClient(t, func(c *config) { c.retries = tt.retries }, first, second)

			_, meta, err := c.CallTool(tt.ctx, "tool", nil, WithCallRetry(tt.policy))
			calls := len(first.params) + len(second.params)
			if calls != tt.wantCalls {
				t.Errorf("attempts = %d, want %d (err=%v)", calls, tt.wantCalls, err)
			}
			if meta.RetryCount != tt.wantRetries {
				t.Errorf("RetryCount = %d, want %d", meta.RetryCount, tt.wantRetries)
			}
			if (tt.wantRetries > 0) != meta.Reconnected {
				t.Errorf("Reconnected = %v with RetryCount %d", meta.Reconnected, meta.RetryCount)
			}
			if tt.wantRetries > 0 && (err != nil || dialer.dialCount() != 2) {
				t.Errorf("err = %v, dials = %d, want a successful redial", err, dialer.dialCount())
			}
		})
	}
}

func TestPoolDefaultRetryPolicyAndPerCallOverride(t *testing.T) {
	// Pool default RetryNever; a per-call RetryDefault restores today's behavior.
	mk := func() (*Client, *fakeSession, *fakeSession) {
		first := &fakeSession{callToolErrs: []error{mcpsdk.ErrConnectionClosed}}
		second := &fakeSession{}
		c, _ := newCallClient(t, func(c *config) { c.callRetry = RetryNever }, first, second)
		return c, first, second
	}

	c, first, second := mk()
	if _, _, err := c.CallTool(context.Background(), "tool", nil); err == nil {
		t.Error("pool default RetryNever: want error, got nil")
	}
	if len(first.params)+len(second.params) != 1 {
		t.Error("pool default RetryNever retried")
	}

	c, _, second = mk()
	if _, _, err := c.CallTool(context.Background(), "tool", nil, WithCallRetry(RetryDefault)); err != nil {
		t.Errorf("explicit RetryDefault: %v", err)
	}
	if len(second.params) != 1 {
		t.Error("explicit RetryDefault did not retry")
	}
}

func TestCallTimeouts(t *testing.T) {
	defaults := map[string]time.Duration{TransportHTTP: time.Minute}
	dl := func(sess *fakeSession) (time.Duration, bool) {
		if !sess.ctxDeadlines[0] {
			return 0, false
		}
		return time.Until(sess.callDeadline[0]), true
	}

	t.Run("default applies without a caller deadline", func(t *testing.T) {
		sess := &fakeSession{}
		c, _ := newCallClient(t, func(c *config) { c.callTimeouts = defaults }, sess)
		if _, _, err := c.CallTool(context.Background(), "tool", nil); err != nil {
			t.Fatal(err)
		}
		if d, ok := dl(sess); !ok || d <= 30*time.Second || d > time.Minute {
			t.Errorf("deadline = %v, %v; want ~1m", d, ok)
		}
	})
	t.Run("default keyed by transport", func(t *testing.T) {
		sess := &fakeSession{}
		c, _ := newCallClient(t, func(c *config) { c.callTimeouts = map[string]time.Duration{TransportSSE: time.Minute} }, sess)
		if _, _, err := c.CallTool(context.Background(), "tool", nil); err != nil {
			t.Fatal(err)
		}
		if _, ok := dl(sess); ok {
			t.Error("an sse-only default applied to an http server")
		}
	})
	t.Run("caller deadline is never extended", func(t *testing.T) {
		sess := &fakeSession{}
		c, _ := newCallClient(t, func(c *config) { c.callTimeouts = defaults }, sess)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, _, err := c.CallTool(ctx, "tool", nil); err != nil {
			t.Fatal(err)
		}
		if d, ok := dl(sess); !ok || d > 5*time.Second {
			t.Errorf("deadline = %v, %v; want <= 5s", d, ok)
		}
	})
	t.Run("WithCallTimeout overrides the default and shortens a caller deadline", func(t *testing.T) {
		sess := &fakeSession{}
		c, _ := newCallClient(t, func(c *config) { c.callTimeouts = defaults }, sess)
		if _, _, err := c.CallTool(context.Background(), "tool", nil, WithCallTimeout(2*time.Second)); err != nil {
			t.Fatal(err)
		}
		if d, ok := dl(sess); !ok || d > 2*time.Second {
			t.Errorf("deadline = %v, %v; want <= 2s", d, ok)
		}

		sess2 := &fakeSession{}
		c2, _ := newCallClient(t, nil, sess2)
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if _, _, err := c2.CallTool(ctx, "tool", nil, WithCallTimeout(2*time.Second)); err != nil {
			t.Fatal(err)
		}
		if d, ok := dl(sess2); !ok || d > 2*time.Second {
			t.Errorf("deadline = %v, %v; want <= 2s", d, ok)
		}
	})
	t.Run("non-positive is ignored", func(t *testing.T) {
		sess := &fakeSession{}
		c, _ := newCallClient(t, nil, sess)
		if _, _, err := c.CallTool(context.Background(), "tool", nil, WithCallTimeout(0), WithCallTimeout(-1)); err != nil {
			t.Fatal(err)
		}
		if _, ok := dl(sess); ok {
			t.Error("non-positive timeout produced a deadline")
		}
	})
}

func TestWithClientOptions_CalledAtEveryDialWithServerName(t *testing.T) {
	var seen []string
	p := NewPool(WithClientOptions(func(server string, o *mcpsdk.ClientOptions) {
		seen = append(seen, server)
	}))
	if p.opts.clientOpts == nil {
		t.Fatal("WithClientOptions not stored")
	}
	// Two dials through the production seam, against a config that fails
	// fast after building the SDK client (empty URL).
	for i := 0; i < 2; i++ {
		_, _, _ = dialSDK(context.Background(), "alpha", ServerConfig{Transport: "http"}, 0, p.opts)
	}
	if len(seen) != 2 || seen[0] != "alpha" || seen[1] != "alpha" {
		t.Errorf("callback saw %v, want [alpha alpha]", seen)
	}
}

func TestWithToolListChangedHandler_ComposesAndBindsServer(t *testing.T) {
	var order []string
	var got string
	cfg := defaultConfig()
	WithClientOptions(func(server string, o *mcpsdk.ClientOptions) { order = append(order, "opts:"+server) })(&cfg)
	WithToolListChangedHandler(func(_ context.Context, server string) { got = server })(&cfg)

	var o mcpsdk.ClientOptions
	cfg.clientOpts("beta", &o)
	if o.ToolListChangedHandler == nil {
		t.Fatal("ToolListChangedHandler not installed")
	}
	o.ToolListChangedHandler(context.Background(), nil)
	if got != "beta" || len(order) != 1 || order[0] != "opts:beta" {
		t.Errorf("got=%q order=%v", got, order)
	}
}
