package client

import (
	"context"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	gmcpserver "github.com/hollis-labs/go-mcp/server"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// runAsServerEnv, when set in the test binary's own environment, makes
// TestMain run a tiny real MCP server over stdio instead of the test suite
// -- the "fork and exec the test binary itself" trick the official SDK's
// own cmd_test.go uses, which avoids needing a separate compiled fixture
// binary or testdata directory.
const runAsServerEnv = "_GOMCP_CLIENT_TEST_RUN_AS_SERVER"

func TestMain(m *testing.M) {
	if os.Getenv(runAsServerEnv) != "" {
		runFixtureServer()
		return
	}
	os.Exit(m.Run())
}

// runFixtureServer serves two tools real enough to exercise a real stdio
// round trip: echo (for the happy path) and big (to produce an
// oversized response on demand, for the response-cap test).
func runFixtureServer() {
	srv := gmcpserver.NewServer("go-mcp-client-test-fixture", "0.0.0")
	srv.RegisterTool(gmcpserver.Tool{
		Name:        "echo",
		Description: "echoes the given text",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"text": map[string]any{"type": "string"}},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			text, _ := args["text"].(string)
			return text, nil
		},
		ReadOnlyHint: true,
	})
	srv.RegisterTool(gmcpserver.Tool{
		Name:        "big",
		Description: "returns a response of the requested size in bytes",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"bytes": map[string]any{"type": "number"}},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			n, _ := args["bytes"].(float64)
			return strings.Repeat("x", int(n)), nil
		},
		ReadOnlyHint: true,
	})
	if err := srv.Run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

// fixtureServerConfig returns a ServerConfig that launches this test binary
// itself as the fixture server (see TestMain), optionally with a response
// cap applied.
func fixtureServerConfig(t *testing.T) ServerConfig {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	return ServerConfig{
		Transport: "stdio",
		Command:   exe,
		Env:       map[string]string{runAsServerEnv: "1"},
	}
}

func TestStdio_RealSubprocess_UncappedRoundTrip(t *testing.T) {
	pool := NewPool() // no cap requested: exercises the plain CommandTransport path
	t.Cleanup(func() { _ = pool.Close() })
	if err := pool.Register("fixture", fixtureServerConfig(t)); err != nil {
		t.Fatalf("Register: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tools, err := pool.ListTools(ctx, "fixture")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools.Tools) != 2 {
		t.Fatalf("got %d tools, want 2", len(tools.Tools))
	}

	res, _, err := pool.CallTool(ctx, "fixture", "echo", map[string]any{"text": "hello"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if got := resultText(t, res); got != "hello" {
		t.Errorf("echo result = %q, want %q", got, "hello")
	}
}

func TestStdio_RealSubprocess_CappedRoundTrip(t *testing.T) {
	pool := NewPool(WithMaxResponseBytes(64 * 1024)) // exercises the IOTransport-capped path
	t.Cleanup(func() { _ = pool.Close() })
	if err := pool.Register("fixture", fixtureServerConfig(t)); err != nil {
		t.Fatalf("Register: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, _, err := pool.CallTool(ctx, "fixture", "echo", map[string]any{"text": "hello"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if got := resultText(t, res); got != "hello" {
		t.Errorf("echo result = %q, want %q", got, "hello")
	}
}

// TestStdio_ConnectionSurvivesPerCallContextCancellation is the
// CW-20260918-0046 regression: a lazy dial-on-first-use triggered by a call
// with its own short-lived context (the common real-world shape -- a
// per-call context.WithTimeout with a deferred cancel) must not kill the
// subprocess when that context is canceled at the end of the call that
// dialed it. Before the fix, dialStdio spawned the subprocess with
// exec.CommandContext(ctx, ...) using that same per-call ctx, so the
// deferred cancel below reaped the process before the second call ever got
// a chance to reuse the cached connection.
func TestStdio_ConnectionSurvivesPerCallContextCancellation(t *testing.T) {
	pool := NewPool()
	t.Cleanup(func() { _ = pool.Close() })
	if err := pool.Register("fixture", fixtureServerConfig(t)); err != nil {
		t.Fatalf("Register: %v", err)
	}

	callOnce := func(text string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		res, _, err := pool.CallTool(ctx, "fixture", "echo", map[string]any{"text": text})
		if err != nil {
			t.Fatalf("CallTool(%q): %v", text, err)
		}
		return resultText(t, res)
	}

	if got := callOnce("first"); got != "first" {
		t.Fatalf("first call = %q, want %q", got, "first")
	}
	// first's deferred cancel has now fired; the connection it dialed must
	// still be alive for this second, independent call to reuse.
	if got := callOnce("second"); got != "second" {
		t.Fatalf("second call = %q, want %q", got, "second")
	}
}

func TestStdio_RealSubprocess_OversizedResponseRejectedAndProcessReaped(t *testing.T) {
	const capBytes = 4096
	pool := NewPool(WithMaxResponseBytes(capBytes))
	t.Cleanup(func() { _ = pool.Close() })
	if err := pool.Register("fixture", fixtureServerConfig(t)); err != nil {
		t.Fatalf("Register: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Force a connection first so we can inspect the underlying process
	// after the oversized call fails.
	if _, err := pool.ListTools(ctx, "fixture"); err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	c, err := pool.Get("fixture")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	_, _, err = pool.CallTool(ctx, "fixture", "big", map[string]any{"bytes": capBytes * 4})
	if err == nil {
		t.Fatal("CallTool with an oversized response: want error, got nil")
	}

	// The connection must have been torn down (leak-prevention-on-error),
	// not left broken-but-open -- the successor to Nanite's own
	// stdio_transport_leak_test.go regression coverage.
	c.mu.Lock()
	stillConnected := c.sess != nil
	c.mu.Unlock()
	if stillConnected {
		t.Error("session was not closed after an oversized-response error")
	}

	// The next call transparently respawns rather than reusing a corrupt
	// stream.
	res, _, err := pool.CallTool(ctx, "fixture", "echo", map[string]any{"text": "still alive"})
	if err != nil {
		t.Fatalf("CallTool after oversized-response error: %v", err)
	}
	if got := resultText(t, res); got != "still alive" {
		t.Errorf("echo result = %q, want %q", got, "still alive")
	}
}

// resultText extracts the first text content block's text from a
// CallToolResult. go-mcp/server's ToolHandler contract uses a plain string
// result verbatim as text content (never JSON-wrapped), which is what the
// echo/big fixture tools return.
func resultText(t *testing.T, res *mcpsdk.CallToolResult) string {
	t.Helper()
	if len(res.Content) == 0 {
		t.Fatal("result has no content blocks")
	}
	tc, ok := res.Content[0].(*mcpsdk.TextContent)
	if !ok {
		t.Fatalf("content[0] is %T, not *mcpsdk.TextContent", res.Content[0])
	}
	return tc.Text
}
