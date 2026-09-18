package httptransport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gmcp "github.com/hollis-labs/go-mcp/server"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// connect starts an httptest server for h and returns a connected client
// session over the real Streamable HTTP transport, closing both on test
// cleanup.
func connect(t *testing.T, h http.Handler) *mcpsdk.ClientSession {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test-client", Version: "0.0.0"}, nil)
	cs, err := client.Connect(context.Background(), &mcpsdk.StreamableClientTransport{Endpoint: srv.URL}, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func TestToolsListOverHTTP(t *testing.T) {
	srv := gmcp.NewServer("cerberus", "0.1.0")
	srv.RegisterTool(gmcp.Tool{Name: "zeta", Description: "z", InputSchema: gmcp.EmptyObjectSchema(), ReadOnlyHint: true})
	srv.RegisterTool(gmcp.Tool{Name: "alpha", Description: "a", InputSchema: gmcp.EmptyObjectSchema(), ReadOnlyHint: true})

	cs := connect(t, NewHandler(srv, HandlerOptions{}))

	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(res.Tools) != 2 || res.Tools[0].Name != "alpha" || res.Tools[1].Name != "zeta" {
		t.Fatalf("unexpected tools: %+v", res.Tools)
	}
}

func TestToolCallOverHTTP(t *testing.T) {
	srv := gmcp.NewServer("cerberus", "0.1.0")
	srv.RegisterTool(gmcp.Tool{
		Name:         "echo",
		Description:  "echo",
		InputSchema:  gmcp.ObjectSchema(map[string]any{"text": map[string]any{"type": "string"}}, "text"),
		ReadOnlyHint: true,
		Handler: func(ctx context.Context, args map[string]any) (string, error) {
			return args["text"].(string), nil
		},
	})

	cs := connect(t, NewHandler(srv, HandlerOptions{}))

	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "echo",
		Arguments: map[string]any{"text": "hello"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	text, ok := res.Content[0].(*mcpsdk.TextContent)
	if !ok || text.Text != "hello" {
		t.Fatalf("unexpected content: %#v", res.Content)
	}
}

func TestOriginValidation(t *testing.T) {
	srv := gmcp.NewServer("cerberus", "0.1.0")
	h := NewHandler(srv, HandlerOptions{AllowedOrigins: []string{"https://allowed.example"}})

	ts := httptest.NewServer(h)
	defer ts.Close()

	req, err := http.NewRequest(http.MethodPost, ts.URL, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Origin", "https://blocked.example")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

func TestOriginAllowedPassesThrough(t *testing.T) {
	srv := gmcp.NewServer("cerberus", "0.1.0")
	h := NewHandler(srv, HandlerOptions{AllowedOrigins: []string{"https://allowed.example"}})

	ts := httptest.NewServer(h)
	defer ts.Close()

	req, err := http.NewRequest(http.MethodPost, ts.URL, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Origin", "https://allowed.example")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden {
		t.Fatalf("allowed origin was rejected: status = %d", resp.StatusCode)
	}
}

func TestStatelessModeRejectsGET(t *testing.T) {
	srv := gmcp.NewServer("cerberus", "0.1.0")
	h := NewHandler(srv, HandlerOptions{})

	ts := httptest.NewServer(h)
	defer ts.Close()

	resp, err := http.Get(ts.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405 (stateless mode serves POST only)", resp.StatusCode)
	}
}
