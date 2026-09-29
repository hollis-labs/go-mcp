package client

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func extraToolsConfig(t *testing.T, extra map[string]string) ServerConfig {
	t.Helper()
	cfg := fixtureServerConfig(t)
	cfg.Env[runAsServerExtraToolsEnv] = "1"
	for k, v := range extra {
		cfg.Env[k] = v
	}
	return cfg
}

func TestCallOptions_MetaReachesServerOverStdio(t *testing.T) {
	pool := NewPool()
	t.Cleanup(func() { _ = pool.Close() })
	if err := pool.Register("fixture", extraToolsConfig(t, nil)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, _, err := pool.CallTool(ctx, "fixture", "meta", nil,
		WithCallMeta(map[string]any{"myapp/idempotencyKey": "k1", "n": 2}))
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(resultText(t, res)), &got); err != nil {
		t.Fatalf("decode %q: %v", resultText(t, res), err)
	}
	if got["myapp/idempotencyKey"] != "k1" || got["n"] != float64(2) {
		t.Errorf("server saw _meta %v", got)
	}
}

func readPIDLog(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // test-owned temp path
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	return strings.Fields(string(b))
}

// TestCallOptions_RetryIfUnsentRespawnsDeadStdioServer is the real-subprocess
// counterpart of Nanite's respawn test: the fixture exits shortly after its
// first tools/call, so a later call on the cached connection fails with the
// SDK's "client is closing" before being written. With RetryIfUnsent it must
// transparently respawn; the pid log proves a second process started.
func TestCallOptions_RetryIfUnsentRespawnsDeadStdioServer(t *testing.T) {
	pidLog := filepath.Join(t.TempDir(), "pids.log")
	pool := NewPool(WithCallRetryPolicy(RetryIfUnsent))
	t.Cleanup(func() { _ = pool.Close() })
	if err := pool.Register("fixture", extraToolsConfig(t, map[string]string{
		runAsServerExitAfterCallEnv: "1",
		runAsServerPIDLogEnv:        pidLog,
	})); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var pids []string
	for attempt := 1; attempt <= 60; attempt++ {
		res, _, err := pool.CallTool(ctx, "fixture", "echo", map[string]any{"text": "hi"})
		if err != nil {
			t.Fatalf("attempt %d did not recover: %v", attempt, err)
		}
		if got := resultText(t, res); got != "hi" {
			t.Fatalf("attempt %d echo = %q", attempt, got)
		}
		if pids = readPIDLog(t, pidLog); len(pids) >= 2 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if len(pids) < 2 || pids[0] == pids[1] {
		t.Fatalf("pid log = %v, want at least two DISTINCT startups (a respawn)", pids)
	}
}

func TestCallOptions_RetryNeverSurfacesDeadServerError(t *testing.T) {
	pidLog := filepath.Join(t.TempDir(), "pids.log")
	pool := NewPool()
	t.Cleanup(func() { _ = pool.Close() })
	if err := pool.Register("fixture", extraToolsConfig(t, map[string]string{
		runAsServerExitAfterCallEnv: "1",
		runAsServerPIDLogEnv:        pidLog,
	})); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if _, _, err := pool.CallTool(ctx, "fixture", "echo", nil, WithCallRetry(RetryNever)); err != nil {
		t.Fatalf("first call: %v", err)
	}
	// Wait for the fixture to die, then the next call must fail unretried.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		_, meta, err := pool.CallTool(ctx, "fixture", "echo", nil, WithCallRetry(RetryNever))
		if err != nil {
			if meta.RetryCount != 0 {
				t.Fatalf("RetryNever retried: %+v", meta)
			}
			if n := len(readPIDLog(t, pidLog)); n != 1 {
				t.Fatalf("pid log has %d startups, want 1 (no respawn)", n)
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("dead server never produced an error")
}

// TestWithToolListChangedHandler_StdioServerPush uses stdio: transport/http
// is stateless, so it carries no server-initiated notifications.
func TestWithToolListChangedHandler_StdioServerPush(t *testing.T) {
	var mu sync.Mutex
	var servers []string
	changed := make(chan struct{}, 4)
	pool := NewPool(WithToolListChangedHandler(func(_ context.Context, server string) {
		mu.Lock()
		servers = append(servers, server)
		mu.Unlock()
		changed <- struct{}{}
	}))
	t.Cleanup(func() { _ = pool.Close() })
	if err := pool.Register("fixture", extraToolsConfig(t, nil)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, _, err := pool.CallTool(ctx, "fixture", "add_tool", nil); err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	select {
	case <-changed:
	case <-time.After(5 * time.Second):
		t.Fatal("ToolListChangedHandler never fired")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(servers) == 0 || servers[0] != "fixture" {
		t.Errorf("handler saw servers %v, want [fixture ...]", servers)
	}
}
