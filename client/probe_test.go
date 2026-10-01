package client

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gmcpserver "github.com/hollis-labs/go-mcp/server"
	httptransport "github.com/hollis-labs/go-mcp/transport/http"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// CW-20261001-0078. go-sdk v1.8.0's ClientSession.Ping sends no SEP-2575
// request _meta, and a stateless server on the 2026-07-28 protocol answers
// a bare ping with -32602. That answer came from a live server; the lazy
// health probe must not tear the connection down over it.

func statelessServer(t *testing.T, wrap func(http.Handler) http.Handler) *httptest.Server {
	t.Helper()
	srv := gmcpserver.NewServer("stateless", "0.0.0")
	srv.RegisterTool(gmcpserver.Tool{
		Name:         "echo",
		Description:  "echo",
		InputSchema:  gmcpserver.EmptyObjectSchema(),
		ReadOnlyHint: true,
		Handler: func(context.Context, map[string]any) (any, error) {
			return "ok", nil
		},
	})
	h := httptransport.NewHandler(srv, httptransport.HandlerOptions{})
	if wrap != nil {
		h = wrap(h)
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return ts
}

// probingClient dials the real SDK. The probe never comes due on its own;
// probeDue makes the next call run it.
func probingClient(t *testing.T, url string) *Client {
	t.Helper()
	c := newClient("stateless", ServerConfig{Transport: TransportHTTP, URL: url},
		testConfig(func(c *config) { c.probeInterval = time.Hour }), dialSDK)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func probeDue(c *Client) {
	c.mu.Lock()
	c.lastProbe = time.Now().Add(-2 * time.Hour)
	c.mu.Unlock()
}

func TestHealthProbe_StatelessServerRejectingBarePingStaysConnected(t *testing.T) {
	ts := statelessServer(t, nil)
	c := probingClient(t, ts.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, _, err := c.CallTool(ctx, "echo", nil); err != nil {
		t.Fatalf("first call: %v", err)
	}
	sess := c.SDKSession()

	// The premise: the server rejects the SDK's bare ping. If this stops
	// holding, the SDK decorates ping itself, this test no longer covers a
	// strict server, and tangent's protocol pin (CW-20261001-0003) is moot.
	pingErr := sess.Ping(ctx, nil)
	var werr *jsonrpc.Error
	if !errors.As(pingErr, &werr) || werr.Code != jsonrpc.CodeInvalidParams {
		t.Fatalf("bare SDK ping = %v, want the server's -32602 rejection", pingErr)
	}

	probeDue(c)
	res, meta, err := c.CallTool(ctx, "echo", nil)
	if err != nil {
		t.Fatalf("call after a rejected probe: %v", err)
	}
	if got := resultText(t, res); got != "ok" {
		t.Fatalf("echo = %q, want ok", got)
	}
	if !meta.HealthProbe || meta.Reconnected || meta.AttemptCount != 1 {
		t.Fatalf("meta = %+v, want a probe, no reconnect, one attempt", meta)
	}
	if c.SDKSession() != sess {
		t.Fatal("the probe replaced a connection whose server answered it")
	}

	if err := c.Ping(ctx); err != nil {
		t.Fatalf("Client.Ping against a live server: %v", err)
	}
	if c.SDKSession() != sess {
		t.Fatal("Client.Ping replaced a connection whose server answered it")
	}
}

// A 503 carries no JSON-RPC reply: the SDK reports it wrapped in its own
// transport-rejection error, which is not the server answering.
func TestHealthProbe_TransientStatusIsStillAFailure(t *testing.T) {
	ts := statelessServer(t, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			if bytes.Contains(body, []byte(`"method":"ping"`)) {
				http.Error(w, "upstream down", http.StatusServiceUnavailable)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			next.ServeHTTP(w, r)
		})
	})
	c := probingClient(t, ts.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, _, err := c.CallTool(ctx, "echo", nil); err != nil {
		t.Fatalf("first call: %v", err)
	}
	probeDue(c)
	_, meta, err := c.CallTool(ctx, "echo", nil)
	if err == nil {
		t.Fatal("call succeeded past a probe answered with 503")
	}
	if !meta.HealthProbe {
		t.Fatalf("meta = %+v, want the probe to have run", meta)
	}
	if c.SDKSession() != nil {
		t.Fatal("connection still open after a failed probe")
	}
	if err := c.Ping(ctx); err == nil {
		t.Fatal("Client.Ping succeeded against a 503")
	}
}

func TestHealthProbe_ServerGoneIsStillAFailure(t *testing.T) {
	ts := statelessServer(t, nil)
	c := probingClient(t, ts.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, _, err := c.CallTool(ctx, "echo", nil); err != nil {
		t.Fatalf("first call: %v", err)
	}
	ts.Close()
	probeDue(c)
	if _, meta, err := c.CallTool(ctx, "echo", nil); err == nil || !meta.HealthProbe {
		t.Fatalf("call against a stopped server: meta %+v, err %v; want a failed probe", meta, err)
	}
}

func TestServerAnswered(t *testing.T) {
	invalid := &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "missing or invalid _meta field"}
	rejected := &jsonrpc.Error{Code: sdkRejectedCode, Message: sdkRejectedMessage}
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"reply over a 2xx stream", fmt.Errorf("calling %q: %w", "ping", invalid), true},
		{"reply in a non-2xx body", fmt.Errorf("calling %q: %w", "ping",
			fmt.Errorf("%s: %w: %w: %v", "sending ping", invalid, rejected, "Bad Request")), true},
		{"method not found", fmt.Errorf("calling %q: %w", "ping",
			&jsonrpc.Error{Code: jsonrpc.CodeMethodNotFound, Message: "method not found"}), true},
		{"transient status", fmt.Errorf("calling %q: %w", "ping",
			fmt.Errorf("%w: %s: %v", rejected, "sending ping", "Service Unavailable")), false},
		{"transport failure", fmt.Errorf("calling %q: %w", "ping",
			fmt.Errorf("%s: %w: %w", "sending ping", rejected, errors.New("connection refused"))), false},
		{"connection closed", fmt.Errorf("%w: calling %q: %v", mcpsdk.ErrConnectionClosed, "ping", "client is closing"), false},
		{"caller gave up", context.Canceled, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := serverAnswered(tc.err); got != tc.want {
				t.Fatalf("serverAnswered(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
