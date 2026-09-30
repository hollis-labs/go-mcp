package server

import (
	"context"
	"fmt"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// mrtrTool asks the client to confirm, then answers with what came back.
func mrtrTool(req mcpsdk.InputRequest) Tool {
	return Tool{
		Name: "confirm", Description: "asks first", InputSchema: EmptyObjectSchema(), ReadOnlyHint: true,
		Handler: func(ctx context.Context, _ map[string]any) (any, error) {
			responses := InputResponses(ctx)
			if len(responses) == 0 {
				return InputRequired{Requests: mcpsdk.InputRequestMap{"ask": req}, State: "state-1"}, nil
			}
			res, ok := responses["ask"].(*mcpsdk.ElicitResult)
			if !ok {
				return nil, fmt.Errorf("response is %T", responses["ask"])
			}
			return fmt.Sprintf("action=%s yes=%v state=%s", res.Action, res.Content["yes"], RequestState(ctx)), nil
		},
	}
}

func connectMRTR(t *testing.T, tool Tool, version string, caps *mcpsdk.ClientCapabilities, seen *[]*mcpsdk.ElicitParams) *mcpsdk.ClientSession {
	t.Helper()
	srv := NewServer("test", "0")
	srv.RegisterTool(tool)
	serverT, clientT := mcpsdk.NewInMemoryTransports()
	ss, err := srv.SDKServer().Connect(context.Background(), serverT, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "c", Version: "0"}, &mcpsdk.ClientOptions{
		Capabilities: caps,
		ElicitationHandler: func(_ context.Context, req *mcpsdk.ElicitRequest) (*mcpsdk.ElicitResult, error) {
			*seen = append(*seen, req.Params)
			return &mcpsdk.ElicitResult{Action: "accept", Content: map[string]any{"yes": true}}, nil
		},
	})
	cs, err := client.Connect(context.Background(), clientT, &mcpsdk.ClientSessionOptions{ProtocolVersion: version})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func text(res *mcpsdk.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcpsdk.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// A tool that returns InputRequired gets the client's answer on the retry,
// with its state, on the 2026-07-28 protocol (the client retries) and on an
// earlier one (the SDK asks the client and calls the handler again).
func TestInputRequiredRoundTrips(t *testing.T) {
	form := &mcpsdk.ElicitParams{Message: "Proceed?", RequestedSchema: map[string]any{"type": "object", "properties": map[string]any{"yes": map[string]any{"type": "boolean"}}}}
	for _, version := range []string{"", "2025-11-25"} {
		var seen []*mcpsdk.ElicitParams
		cs := connectMRTR(t, mrtrTool(form), version, nil, &seen)
		res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: "confirm"})
		if err != nil {
			t.Fatalf("protocol %q: %v", version, err)
		}
		if got := text(res); got != "action=accept yes=true state=state-1" {
			t.Fatalf("protocol %q: %q", version, got)
		}
		if len(seen) != 1 || seen[0].Message != "Proceed?" {
			t.Fatalf("protocol %q: the client was asked %d times: %+v", version, len(seen), seen)
		}
	}
}

// URL-mode elicitation goes the same way, to a client that supports it.
func TestInputRequiredURLMode(t *testing.T) {
	url := &mcpsdk.ElicitParams{Mode: "url", Message: "Approve in the browser", URL: "http://localhost:4783/approvals?id=apr_1", ElicitationID: "apr_1"}
	var seen []*mcpsdk.ElicitParams
	caps := &mcpsdk.ClientCapabilities{Elicitation: &mcpsdk.ElicitationCapabilities{URL: &mcpsdk.URLElicitationCapabilities{}}}
	cs := connectMRTR(t, mrtrTool(url), "", caps, &seen)
	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: "confirm"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(text(res), "action=accept") || len(seen) != 1 || seen[0].URL != url.URL || seen[0].ElicitationID != "apr_1" {
		t.Fatalf("url mode: %q %+v", text(res), seen)
	}
}

// A handler that never asks sees no responses and no state, and its result
// is unchanged.
func TestNoInputOnAFirstCall(t *testing.T) {
	var seen []*mcpsdk.ElicitParams
	plain := Tool{Name: "plain", Description: "d", InputSchema: EmptyObjectSchema(), ReadOnlyHint: true,
		Handler: func(ctx context.Context, _ map[string]any) (any, error) {
			return fmt.Sprintf("%d %q", len(InputResponses(ctx)), RequestState(ctx)), nil
		}}
	cs := connectMRTR(t, plain, "", nil, &seen)
	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: "plain"})
	if err != nil || text(res) != `0 ""` || len(seen) != 0 {
		t.Fatalf("%v %q %d", err, text(res), len(seen))
	}
}

// A handler can see whether the client takes URL elicitation, on the
// 2026-07-28 protocol and before it.
func TestClientCapabilitiesReachTheHandler(t *testing.T) {
	probe := Tool{Name: "caps", Description: "d", InputSchema: EmptyObjectSchema(), ReadOnlyHint: true,
		Handler: func(ctx context.Context, _ map[string]any) (any, error) {
			caps := ClientCapabilities(ctx)
			return fmt.Sprintf("elicitation=%v url=%v", caps != nil && caps.Elicitation != nil, caps != nil && caps.Elicitation != nil && caps.Elicitation.URL != nil), nil
		}}
	urlCaps := &mcpsdk.ClientCapabilities{Elicitation: &mcpsdk.ElicitationCapabilities{URL: &mcpsdk.URLElicitationCapabilities{}}}
	for _, version := range []string{"", "2025-11-25"} {
		var seen []*mcpsdk.ElicitParams
		cs := connectMRTR(t, probe, version, urlCaps, &seen)
		res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: "caps"})
		if err != nil || text(res) != "elicitation=true url=true" {
			t.Fatalf("protocol %q: %v %q", version, err, text(res))
		}
	}
	var seen []*mcpsdk.ElicitParams
	cs := connectMRTR(t, probe, "", &mcpsdk.ClientCapabilities{}, &seen)
	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: "caps"})
	// An ElicitationHandler alone declares form elicitation, not URL mode.
	if err != nil || text(res) != "elicitation=true url=false" {
		t.Fatalf("form only: %v %q", err, text(res))
	}
}
