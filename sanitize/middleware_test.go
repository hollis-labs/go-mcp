package sanitize

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// captureLogger returns a slog.Logger that writes JSON lines into the
// returned buffer — tests assert on the captured output.
func captureLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	h := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	return slog.New(h), &buf
}

func callToolRequest(t *testing.T, name string, args map[string]any) *mcp.CallToolRequest {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{Name: name, Arguments: raw},
	}
}

func unmarshalArgs(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil {
		t.Fatalf("unmarshal cleaned args: %v", err)
	}
	return args
}

// TestMiddleware_CleanCallNoLog verifies that a clean tool-call passes through
// unmodified and emits NO log line.
func TestMiddleware_CleanCallNoLog(t *testing.T) {
	logger, buf := captureLogger()

	called := false
	next := mcp.MethodHandler(func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		called = true
		call := req.(*mcp.CallToolRequest)
		args := unmarshalArgs(t, call.Params.Arguments)
		if got, want := args["payload_summary"], "clean prose"; got != want {
			t.Fatalf("handler saw mutated args: %v", args)
		}
		return &mcp.CallToolResult{}, nil
	})

	wrapped := Middleware(logger)(next)
	req := callToolRequest(t, "memory_write", map[string]any{
		"payload_summary": "clean prose",
		"payload_body":    "more clean prose",
	})
	if _, err := wrapped(context.Background(), "tools/call", req); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !called {
		t.Fatalf("handler not called")
	}
	if buf.Len() != 0 {
		t.Fatalf("expected no log output for clean call, got %q", buf.String())
	}
}

// TestMiddleware_PollutedCallLogsAndCleans verifies the middleware emits
// exactly one warn-level line on a polluted call, and that the wrapped
// handler sees the cleaned args.
func TestMiddleware_PollutedCallLogsAndCleans(t *testing.T) {
	logger, buf := captureLogger()

	var seen map[string]any
	next := mcp.MethodHandler(func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		call := req.(*mcp.CallToolRequest)
		seen = unmarshalArgs(t, call.Params.Arguments)
		return &mcp.CallToolResult{}, nil
	})

	wrapped := Middleware(logger)(next)
	req := callToolRequest(t, "memory_write", map[string]any{
		"payload_summary": "Lead-in.</payload_summary>\n<parameter name=\"payload_body\">## Body</parameter>",
		"payload_body":    "",
	})
	if _, err := wrapped(context.Background(), "tools/call", req); err != nil {
		t.Fatalf("handler error: %v", err)
	}

	if got := seen["payload_body"].(string); got != "## Body" {
		t.Fatalf("handler saw uncleaned payload_body: %q", got)
	}
	if strings.Contains(seen["payload_summary"].(string), "</payload_summary>") {
		t.Fatalf("handler saw uncleaned payload_summary: %q", seen["payload_summary"])
	}

	lines := splitNonEmpty(buf.String())
	if len(lines) != 1 {
		t.Fatalf("expected exactly 1 log line, got %d: %q", len(lines), buf.String())
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("log line not valid JSON: %v: %s", err, lines[0])
	}
	if entry["level"] != "WARN" {
		t.Fatalf("expected WARN level, got %v", entry["level"])
	}
	if entry["msg"] != "mcp-sanitize: cleaned tool call" {
		t.Fatalf("unexpected msg: %v", entry["msg"])
	}
	if entry["tool"] != "memory_write" {
		t.Fatalf("unexpected tool: %v", entry["tool"])
	}
}

// TestMiddleware_NilLoggerUsesDefault confirms the contract that a nil logger
// falls back to slog.Default() rather than panicking.
func TestMiddleware_NilLoggerUsesDefault(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("middleware panicked with nil logger: %v", r)
		}
	}()
	called := false
	next := mcp.MethodHandler(func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		called = true
		return &mcp.CallToolResult{}, nil
	})
	wrapped := Middleware(nil)(next)
	req := callToolRequest(t, "x", map[string]any{"y": "z"})
	if _, err := wrapped(context.Background(), "tools/call", req); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !called {
		t.Fatalf("handler not called")
	}
}

// TestMiddleware_EmptyArgsPassThrough confirms calls with no arguments are
// not logged or modified.
func TestMiddleware_EmptyArgsPassThrough(t *testing.T) {
	logger, buf := captureLogger()
	next := mcp.MethodHandler(func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		return &mcp.CallToolResult{}, nil
	})
	wrapped := Middleware(logger)(next)
	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "ping"}}
	if _, err := wrapped(context.Background(), "tools/call", req); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("expected no logs for empty-args call, got %q", buf.String())
	}
}

// TestMiddleware_NonCallToolMethodPassesThrough confirms the middleware
// leaves every method other than tools/call alone.
func TestMiddleware_NonCallToolMethodPassesThrough(t *testing.T) {
	logger, buf := captureLogger()
	called := false
	next := mcp.MethodHandler(func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		called = true
		return &mcp.ListToolsResult{}, nil
	})
	wrapped := Middleware(logger)(next)
	req := &mcp.ListToolsRequest{}
	if _, err := wrapped(context.Background(), "tools/list", req); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !called {
		t.Fatalf("handler not called")
	}
	if buf.Len() != 0 {
		t.Fatalf("expected no logs for a non-tools/call method, got %q", buf.String())
	}
}

func splitNonEmpty(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}
