package clientguard_test

import (
	"bufio"
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hollis-labs/go-mcp/client"
	"github.com/hollis-labs/go-mcp/clientguard"
	gmcpserver "github.com/hollis-labs/go-mcp/server"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Re-exec fixture, the pattern client/stdio_test.go uses: when
// runAsServerEnv is set, this test binary is the MCP server subprocess
// instead of the test suite. Each launch appends its pid to the file named
// by pidLogEnv, so a test can count real dials; dieEnv makes the launch
// exit right after logging, so the upstream is "down".
const (
	runAsServerEnv = "_GOMCP_CLIENTGUARD_TEST_RUN_AS_SERVER"
	pidLogEnv      = "_GOMCP_CLIENTGUARD_TEST_PID_LOG"
	dieEnv         = "_GOMCP_CLIENTGUARD_TEST_DIE"
)

func TestMain(m *testing.M) {
	if os.Getenv(runAsServerEnv) != "" {
		runFixtureServer()
		return
	}
	os.Exit(m.Run())
}

func runFixtureServer() {
	if p := os.Getenv(pidLogEnv); p != "" {
		if f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil { //nolint:gosec // test-owned temp path
			_, _ = f.WriteString("launch\n")
			_ = f.Close()
		}
	}
	if os.Getenv(dieEnv) != "" {
		os.Exit(1)
	}
	srv := gmcpserver.NewServer("clientguard-test-fixture", "0.0.0")
	srv.RegisterTool(gmcpserver.Tool{
		Name:        "echo",
		Description: "echoes the given text",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"text": map[string]any{"type": "string"}},
		},
		Handler: func(_ context.Context, args map[string]any) (any, error) {
			text, _ := args["text"].(string)
			return text, nil
		},
		ReadOnlyHint: true,
	})
	if err := srv.Run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

func fixtureConfig(t *testing.T, extraEnv map[string]string) (client.ServerConfig, string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	pidLog := filepath.Join(t.TempDir(), "launches")
	env := map[string]string{runAsServerEnv: "1", pidLogEnv: pidLog}
	for k, v := range extraEnv {
		env[k] = v
	}
	return client.ServerConfig{Transport: client.TransportStdio, Command: exe, Env: env}, pidLog
}

func countLaunches(t *testing.T, path string) int {
	t.Helper()
	f, err := os.Open(path) //nolint:gosec // test-owned temp path
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	n := 0
	for s := bufio.NewScanner(f); s.Scan(); {
		n++
	}
	return n
}

func guardedEcho(ctx context.Context, pool *client.Pool, g *clientguard.Guard, server string) (*mcpsdk.CallToolResult, error) {
	return clientguard.Do(ctx, g, server, func(ctx context.Context) (*mcpsdk.CallToolResult, error) {
		res, _, err := pool.CallTool(ctx, server, "echo", map[string]any{"text": "hi"}, client.WithCallTimeout(10*time.Second))
		return res, err
	})
}

// (a) real failures trip the breaker, and the next call does not dial.
func TestIntegration_BreakerTripsAndStopsDialing(t *testing.T) {
	cfg, pidLog := fixtureConfig(t, map[string]string{dieEnv: "1"})
	pool := client.NewPool(client.WithIdentity("clientguard-test", "0"))
	t.Cleanup(func() { _ = pool.Close() })
	if err := pool.Register("dead", cfg); err != nil {
		t.Fatal(err)
	}
	g := clientguard.New(clientguard.WithCircuitBreaker(3, time.Hour))

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for i := range 3 {
		if _, err := guardedEcho(ctx, pool, g, "dead"); err == nil || errors.Is(err, clientguard.ErrCircuitOpen) {
			t.Fatalf("call %d: err = %v, want a real dial failure", i, err)
		}
	}
	dialed := countLaunches(t, pidLog)
	if dialed < 3 {
		t.Fatalf("only %d launches for 3 failing calls", dialed)
	}
	if g.State("dead") != clientguard.CircuitOpen {
		t.Fatalf("State = %v, want open", g.State("dead"))
	}

	_, err := guardedEcho(ctx, pool, g, "dead")
	if !errors.Is(err, clientguard.ErrCircuitOpen) {
		t.Fatalf("err = %v, want ErrCircuitOpen", err)
	}
	if got := countLaunches(t, pidLog); got != dialed {
		t.Fatalf("launches %d -> %d: an open circuit still dialed", dialed, got)
	}
}

// (b) a burst over the limit against a live server is rejected, and the
// rejected calls never reach the pool.
func TestIntegration_RateLimitRejectsBurst(t *testing.T) {
	cfg, pidLog := fixtureConfig(t, nil)
	pool := client.NewPool(client.WithIdentity("clientguard-test", "0"))
	t.Cleanup(func() { _ = pool.Close() })
	if err := pool.Register("live", cfg); err != nil {
		t.Fatal(err)
	}
	// A one-hour period keeps the window from sliding during the test.
	g := clientguard.New(clientguard.WithRateLimit(3, time.Hour, clientguard.RateLimitReject))

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var okN, limited int
	for range 6 {
		res, err := guardedEcho(ctx, pool, g, "live")
		switch {
		case err == nil:
			okN++
			if res == nil || res.IsError {
				t.Fatalf("bad result: %+v", res)
			}
		case errors.Is(err, clientguard.ErrRateLimited):
			limited++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if okN != 3 || limited != 3 {
		t.Fatalf("ok=%d limited=%d, want 3 and 3", okN, limited)
	}
	if got := countLaunches(t, pidLog); got != 1 {
		t.Fatalf("launches = %d, want 1 (one pooled connection)", got)
	}
}
