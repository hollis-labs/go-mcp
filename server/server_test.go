package server

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
)

func TestToolsListIsSortedByName(t *testing.T) {
	var out bytes.Buffer
	srv := NewServer("cerberus", "test")
	srv.out = &out

	srv.RegisterTool(Tool{Name: "zeta_tool", Description: "z", InputSchema: EmptyObjectSchema()})
	srv.RegisterTool(Tool{Name: "alpha_tool", Description: "a", InputSchema: EmptyObjectSchema()})
	srv.RegisterTool(Tool{Name: "mid_tool", Description: "m", InputSchema: EmptyObjectSchema()})

	req := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "tools/list",
	}
	srv.handleRequest(context.Background(), &req)

	var resp struct {
		Result toolsListResult `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if len(resp.Result.Tools) != 3 {
		t.Fatalf("tool count = %d, want 3", len(resp.Result.Tools))
	}
	if resp.Result.Tools[0].Name != "alpha_tool" || resp.Result.Tools[1].Name != "mid_tool" || resp.Result.Tools[2].Name != "zeta_tool" {
		t.Fatalf("unexpected tool order: %#v", resp.Result.Tools)
	}
}

func TestToolCallCancellationSuppressesResponse(t *testing.T) {
	var out bytes.Buffer
	started := make(chan struct{})
	release := make(chan struct{})
	canceled := make(chan struct{})

	srv := NewServer("cerberus", "test")
	srv.out = &out
	srv.RegisterTool(Tool{
		Name:        "block_tool",
		Description: "block",
		InputSchema: EmptyObjectSchema(),
		Handler: func(ctx context.Context, args map[string]interface{}) (string, error) {
			close(started)
			select {
			case <-ctx.Done():
				close(canceled)
				<-release
				return "", ctx.Err()
			case <-release:
				return "done", nil
			}
		},
	})

	callReq := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("42"),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"block_tool","arguments":{}}`),
	}
	srv.handleRequest(context.Background(), &callReq)
	<-started

	cancelReq := jsonRPCRequest{
		JSONRPC: "2.0",
		Method:  "notifications/cancelled",
		Params:  json.RawMessage(`{"requestId":42,"reason":"user cancelled"}`),
	}
	srv.handleRequest(context.Background(), &cancelReq)
	<-canceled
	close(release)
	srv.wg.Wait()

	if out.Len() != 0 {
		t.Fatalf("expected no response after cancellation, got %s", out.String())
	}
}
