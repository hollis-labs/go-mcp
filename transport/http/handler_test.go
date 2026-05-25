package httptransport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gmcp "github.com/hollis-labs/go-mcp/server"
)

func TestInitialize(t *testing.T) {
	srv := gmcp.NewServer("cerberus", "0.1.0")
	h := NewHandler(srv, HandlerOptions{})

	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Mcp-Method", "initialize")
	req.Header.Set("MCP-Protocol-Version", "2024-11-05")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp struct {
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
			ServerInfo      struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"serverInfo"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Result.ProtocolVersion != gmcp.ProtocolVersion {
		t.Fatalf("protocolVersion = %q, want %q", resp.Result.ProtocolVersion, gmcp.ProtocolVersion)
	}
	if resp.Result.ServerInfo.Name != "cerberus" {
		t.Fatalf("serverInfo.name = %q, want cerberus", resp.Result.ServerInfo.Name)
	}
}

func TestToolsListSorted(t *testing.T) {
	srv := gmcp.NewServer("cerberus", "0.1.0")
	srv.RegisterTool(gmcp.Tool{Name: "zeta", Description: "z", InputSchema: gmcp.EmptyObjectSchema()})
	srv.RegisterTool(gmcp.Tool{Name: "alpha", Description: "a", InputSchema: gmcp.EmptyObjectSchema()})

	h := NewHandler(srv, HandlerOptions{})
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Mcp-Method", "tools/list")
	req.Header.Set("MCP-Protocol-Version", "2025-03-26")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Result.Tools) != 2 || resp.Result.Tools[0].Name != "alpha" || resp.Result.Tools[1].Name != "zeta" {
		t.Fatalf("unexpected tools: %+v", resp.Result.Tools)
	}
}

func TestToolCall(t *testing.T) {
	srv := gmcp.NewServer("cerberus", "0.1.0")
	srv.RegisterTool(gmcp.Tool{
		Name:        "echo",
		Description: "echo",
		InputSchema: gmcp.ObjectSchema(map[string]interface{}{"text": map[string]interface{}{"type": "string"}}, "text"),
		Handler: func(ctx context.Context, args map[string]interface{}) (string, error) {
			return args["text"].(string), nil
		},
	})

	h := NewHandler(srv, HandlerOptions{})
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{"text":"hello"}}}`))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Mcp-Method", "tools/call")
	req.Header.Set("Mcp-Name", "echo")
	req.Header.Set("MCP-Protocol-Version", "2025-03-26")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"hello"`) {
		t.Fatalf("body = %s, want hello", rec.Body.String())
	}
}

func TestToolCallSSE(t *testing.T) {
	srv := gmcp.NewServer("cerberus", "0.1.0")
	srv.RegisterTool(gmcp.Tool{
		Name:        "echo",
		Description: "echo",
		InputSchema: gmcp.ObjectSchema(map[string]interface{}{"text": map[string]interface{}{"type": "string"}}, "text"),
		Handler: func(ctx context.Context, args map[string]interface{}) (string, error) {
			return args["text"].(string), nil
		},
	})

	h := NewHandler(srv, HandlerOptions{})
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{"text":"hello"}}}`))
	req.Header.Set("Accept", "text/event-stream, application/json")
	req.Header.Set("Mcp-Method", "tools/call")
	req.Header.Set("Mcp-Name", "echo")
	req.Header.Set("MCP-Protocol-Version", "2025-03-26")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "event: message") || !strings.Contains(body, `"hello"`) {
		t.Fatalf("body = %s, want SSE message with hello", body)
	}
}

func TestToolCallSSENotifications(t *testing.T) {
	srv := gmcp.NewServer("cerberus", "0.1.0")
	srv.RegisterTool(gmcp.Tool{
		Name:        "notify",
		Description: "notify",
		InputSchema: gmcp.EmptyObjectSchema(),
		Handler: func(ctx context.Context, args map[string]interface{}) (string, error) {
			gmcp.NotifyMessage(ctx, "info", "starting")
			gmcp.NotifyProgress(ctx, "tok-1", 1, 2, "halfway")
			return "done", nil
		},
	})

	h := NewHandler(srv, HandlerOptions{})
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"notify","arguments":{}}}`))
	req.Header.Set("Accept", "text/event-stream, application/json")
	req.Header.Set("Mcp-Method", "tools/call")
	req.Header.Set("Mcp-Name", "notify")
	req.Header.Set("MCP-Protocol-Version", "2025-03-26")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, `"method":"notifications/message"`) {
		t.Fatalf("body = %s, want notifications/message", body)
	}
	if !strings.Contains(body, `"method":"notifications/progress"`) {
		t.Fatalf("body = %s, want notifications/progress", body)
	}
	if !strings.Contains(body, `"done"`) {
		t.Fatalf("body = %s, want final done response", body)
	}
}

func TestNotificationAccepted(t *testing.T) {
	srv := gmcp.NewServer("cerberus", "0.1.0")
	h := NewHandler(srv, HandlerOptions{})

	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Mcp-Method", "notifications/initialized")
	req.Header.Set("MCP-Protocol-Version", "2025-03-26")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}
}

func TestOriginValidation(t *testing.T) {
	srv := gmcp.NewServer("cerberus", "0.1.0")
	h := NewHandler(srv, HandlerOptions{AllowedOrigins: []string{"https://allowed.example"}})

	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	req.Header.Set("Origin", "https://blocked.example")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Mcp-Method", "tools/list")
	req.Header.Set("MCP-Protocol-Version", "2025-03-26")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestServerDiscover(t *testing.T) {
	srv := gmcp.NewServer("cerberus", "0.1.0")
	h := NewHandler(srv, HandlerOptions{})

	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":"d1","method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2025-03-26"}}}`))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Mcp-Method", "server/discover")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"supportedVersions"`) {
		t.Fatalf("body = %s, want supportedVersions", rec.Body.String())
	}
}

func TestServerDiscoverSSE(t *testing.T) {
	srv := gmcp.NewServer("cerberus", "0.1.0")
	h := NewHandler(srv, HandlerOptions{})

	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":"d1","method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2025-03-26"}}}`))
	req.Header.Set("Accept", "text/event-stream, application/json")
	req.Header.Set("Mcp-Method", "server/discover")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}
	if !strings.Contains(rec.Body.String(), `"supportedVersions"`) {
		t.Fatalf("body = %s, want supportedVersions", rec.Body.String())
	}
}

func TestHeaderMismatchRejected(t *testing.T) {
	srv := gmcp.NewServer("cerberus", "0.1.0")
	h := NewHandler(srv, HandlerOptions{})

	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{}}}`))
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Mcp-Method", "tools/call")
	req.Header.Set("Mcp-Name", "wrong")
	req.Header.Set("MCP-Protocol-Version", "2025-03-26")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"code":-32001`) {
		t.Fatalf("body = %s, want header mismatch code", rec.Body.String())
	}
}

func TestPreflight(t *testing.T) {
	srv := gmcp.NewServer("cerberus", "0.1.0")
	h := NewHandler(srv, HandlerOptions{AllowedOrigins: []string{"https://allowed.example"}})

	req := httptest.NewRequest(http.MethodOptions, "/mcp", nil)
	req.Header.Set("Origin", "https://allowed.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://allowed.example" {
		t.Fatalf("allow-origin = %q, want allowed origin", got)
	}
}
