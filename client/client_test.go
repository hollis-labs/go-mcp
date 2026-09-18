package client

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeSession is a scriptable sdkSession for reconnect/health-probe tests,
// standing in for a real *mcpsdk.ClientSession.
type fakeSession struct {
	callToolErrs []error // consumed one per call; last repeats
	pingErrs     []error
	closed       bool
}

func (f *fakeSession) CallTool(ctx context.Context, params *mcpsdk.CallToolParams) (*mcpsdk.CallToolResult, error) {
	if err := popErr(&f.callToolErrs); err != nil {
		return nil, err
	}
	return &mcpsdk.CallToolResult{}, nil
}

func (f *fakeSession) ListTools(ctx context.Context, params *mcpsdk.ListToolsParams) (*mcpsdk.ListToolsResult, error) {
	return &mcpsdk.ListToolsResult{}, nil
}

func (f *fakeSession) Ping(ctx context.Context, params *mcpsdk.PingParams) error {
	return popErr(&f.pingErrs)
}

func (f *fakeSession) Close() error {
	f.closed = true
	return nil
}

func popErr(q *[]error) error {
	if len(*q) == 0 {
		return nil
	}
	err := (*q)[0]
	if len(*q) > 1 {
		*q = (*q)[1:]
	}
	return err
}

// fakeDialer hands out fakeSessions in order, counting how many times it was
// asked to dial -- the seam that lets reconnect/health-probe logic be tested
// without a real process or network, mirroring Hadron's clientFactory swap.
type fakeDialer struct {
	mu        sync.Mutex
	dials     int
	sessions  []*fakeSession // consumed one per dial; last repeats
	transport string
	dialErr   error
}

func newFakeDialer(transport string, sessions ...*fakeSession) *fakeDialer {
	return &fakeDialer{transport: transport, sessions: sessions}
}

func (d *fakeDialer) dial(ctx context.Context, name string, cfg ServerConfig, maxResponseBytes int, opts config) (sdkSession, string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.dials++
	if d.dialErr != nil {
		return nil, "", d.dialErr
	}
	var s *fakeSession
	switch {
	case len(d.sessions) == 0:
		s = &fakeSession{}
	case len(d.sessions) == 1:
		s = d.sessions[0]
	default:
		s = d.sessions[0]
		d.sessions = d.sessions[1:]
	}
	return s, d.transport, nil
}

func (d *fakeDialer) dialCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.dials
}

func testConfig(mutate func(*config)) config {
	c := defaultConfig()
	if mutate != nil {
		mutate(&c)
	}
	return c
}

func TestClient_CallTool_ReusesConnection(t *testing.T) {
	dialer := newFakeDialer(TransportHTTP)
	c := newClient("srv", ServerConfig{Transport: "http", URL: "http://example"}, testConfig(func(c *config) { c.probeInterval = time.Hour }), dialer.dial)

	_, meta1, err := c.CallTool(context.Background(), "tool", nil)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if meta1.ReusedClient {
		t.Error("first call: ReusedClient = true, want false")
	}

	_, meta2, err := c.CallTool(context.Background(), "tool", nil)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if !meta2.ReusedClient {
		t.Error("second call: ReusedClient = false, want true")
	}
	if got := dialer.dialCount(); got != 1 {
		t.Errorf("dial count = %d, want 1", got)
	}
}

func TestClient_CallTool_RetriesOnceOnRecoverableError(t *testing.T) {
	first := &fakeSession{callToolErrs: []error{mcpsdk.ErrConnectionClosed}}
	second := &fakeSession{}
	dialer := newFakeDialer(TransportHTTP, first, second)
	c := newClient("srv", ServerConfig{Transport: "http", URL: "http://example"}, testConfig(func(c *config) { c.probeInterval = time.Hour }), dialer.dial)

	_, meta, err := c.CallTool(context.Background(), "tool", nil)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !meta.Reconnected || meta.RetryCount != 1 {
		t.Errorf("meta = %+v, want Reconnected=true RetryCount=1", meta)
	}
	if !first.closed {
		t.Error("first session was not closed after its error")
	}
	if got := dialer.dialCount(); got != 2 {
		t.Errorf("dial count = %d, want 2 (initial + retry)", got)
	}
}

func TestClient_CallTool_NonRecoverableErrorClosesButDoesNotRetry(t *testing.T) {
	sess := &fakeSession{callToolErrs: []error{errors.New(`unknown tool "foo"`)}}
	dialer := newFakeDialer(TransportHTTP, sess)
	c := newClient("srv", ServerConfig{Transport: "http", URL: "http://example"}, testConfig(func(c *config) { c.probeInterval = time.Hour }), dialer.dial)

	_, meta, err := c.CallTool(context.Background(), "tool", nil)
	if err == nil {
		t.Fatal("CallTool: want error, got nil")
	}
	if meta.Reconnected || meta.RetryCount != 0 {
		t.Errorf("meta = %+v, want no retry for a non-recoverable error", meta)
	}
	// Design point: any connection-shaped error tears the connection down,
	// even when it isn't recoverable enough to retry -- see withSession's
	// doc comment. This is the broadened-past-Hadron leak-prevention
	// behavior.
	if !sess.closed {
		t.Error("session was not closed after a non-recoverable error")
	}
	if got := dialer.dialCount(); got != 1 {
		t.Errorf("dial count = %d, want 1 (no reconnect attempted)", got)
	}
}

func TestClient_CallTool_CallerCancelDoesNotCloseConnection(t *testing.T) {
	sess := &fakeSession{}
	dialer := newFakeDialer(TransportHTTP, sess)
	c := newClient("srv", ServerConfig{Transport: "http", URL: "http://example"}, testConfig(func(c *config) { c.probeInterval = time.Hour }), dialer.dial)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the caller has already given up before the call even runs
	sess.callToolErrs = []error{ctx.Err()}

	_, meta, err := c.CallTool(ctx, "tool", nil)
	if err == nil {
		t.Fatal("CallTool with a canceled context: want error, got nil")
	}
	if meta.Reconnected {
		t.Error("meta.Reconnected = true, want false (a caller cancellation is not a reconnect)")
	}
	if sess.closed {
		t.Error("session was closed after a caller-cancellation error, want it left open — an abandoned request says nothing about the connection's health, and closing here would charge the NEXT caller a reconnect")
	}
	if got := dialer.dialCount(); got != 1 {
		t.Errorf("dial count = %d, want 1 (no reconnect attempted)", got)
	}
}

func TestClient_CallTool_RetriesDisabledByWithRetriesZero(t *testing.T) {
	sess := &fakeSession{callToolErrs: []error{mcpsdk.ErrConnectionClosed}}
	dialer := newFakeDialer(TransportHTTP, sess)
	c := newClient("srv", ServerConfig{Transport: "http", URL: "http://example"}, testConfig(func(c *config) { c.probeInterval = time.Hour; c.retries = 0 }), dialer.dial)

	_, meta, err := c.CallTool(context.Background(), "tool", nil)
	if err == nil {
		t.Fatal("CallTool: want error, got nil")
	}
	if meta.RetryCount != 0 {
		t.Errorf("RetryCount = %d, want 0 with retries disabled", meta.RetryCount)
	}
	if got := dialer.dialCount(); got != 1 {
		t.Errorf("dial count = %d, want 1", got)
	}
}

func TestClient_HealthProbe_SkippedForStdio(t *testing.T) {
	sess := &fakeSession{pingErrs: []error{errors.New("should never be called")}}
	dialer := newFakeDialer(TransportStdio, sess)
	// A negative interval means "always due" -- forces the probe path to be
	// reachable so the test actually exercises the stdio exemption, rather
	// than skipping it for "not due yet" and passing for the wrong reason.
	c := newClient("srv", ServerConfig{Transport: "stdio", Command: "irrelevant"}, testConfig(func(c *config) { c.probeInterval = -1 }), dialer.dial)

	_, meta, err := c.CallTool(context.Background(), "tool", nil)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if meta.HealthProbe {
		t.Error("HealthProbe = true, want false for stdio")
	}
}

func TestClient_HealthProbe_FiresAfterIntervalAndReconnectsOnFailure(t *testing.T) {
	firstConn := &fakeSession{pingErrs: []error{mcpsdk.ErrConnectionClosed}}
	secondConn := &fakeSession{}
	dialer := newFakeDialer(TransportHTTP, firstConn, secondConn)
	c := newClient("srv", ServerConfig{Transport: "http", URL: "http://example"}, testConfig(func(c *config) { c.probeInterval = time.Hour }), dialer.dial)

	// Establish the connection, then force the probe to be "due" by
	// backdating lastProbe directly -- this is an internal test, in the same
	// package, so touching the field is the simplest way to avoid a flaky
	// real-time sleep.
	if _, _, err := c.CallTool(context.Background(), "tool", nil); err != nil {
		t.Fatalf("initial call: %v", err)
	}
	c.mu.Lock()
	c.lastProbe = time.Now().Add(-time.Hour)
	c.mu.Unlock()

	_, meta, err := c.CallTool(context.Background(), "tool", nil)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if !meta.HealthProbe {
		t.Error("HealthProbe = false, want true once the interval has elapsed")
	}
	if !meta.Reconnected {
		t.Error("Reconnected = false, want true after a failed probe")
	}
	if !firstConn.closed {
		t.Error("connection was not closed after a failed probe")
	}
	if got := dialer.dialCount(); got != 2 {
		t.Errorf("dial count = %d, want 2 (initial + probe-triggered reconnect)", got)
	}
}

func TestClient_SetMaxResponseBytes_IgnoresNonPositive(t *testing.T) {
	dialer := newFakeDialer(TransportHTTP)
	c := newClient("srv", ServerConfig{Transport: "http"}, testConfig(nil), dialer.dial)
	c.maxResponseBytes = 4096

	c.SetMaxResponseBytes(0)
	c.SetMaxResponseBytes(-1)
	if c.maxResponseBytes != 4096 {
		t.Errorf("maxResponseBytes = %d, want unchanged 4096", c.maxResponseBytes)
	}

	c.SetMaxResponseBytes(8192)
	if c.maxResponseBytes != 8192 {
		t.Errorf("maxResponseBytes = %d, want 8192", c.maxResponseBytes)
	}
}

func TestClient_Close_IsIdempotent(t *testing.T) {
	sess := &fakeSession{}
	dialer := newFakeDialer(TransportHTTP, sess)
	c := newClient("srv", ServerConfig{Transport: "http"}, testConfig(func(c *config) { c.probeInterval = time.Hour }), dialer.dial)

	if _, _, err := c.CallTool(context.Background(), "tool", nil); err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if !sess.closed {
		t.Error("underlying session was never closed")
	}
}
