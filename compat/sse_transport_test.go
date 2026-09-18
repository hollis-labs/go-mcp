package compat

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestNewSSEClientTransport_EndToEnd exercises the transport against a
// gateway-shaped SSE stream: an endpoint event naming an unreachable
// authority (proving the rewrite is what makes the connection usable, not
// an accident of the test server's address) and a keepalive event between
// two real messages.
func TestNewSSEClientTransport_EndToEnd(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/sse", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("ResponseWriter does not support flushing")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		// A wrong, unreachable authority: if repair fails, the later POST
		// this test issues will hit this host and fail to dial rather than
		// reach /message below.
		fmt.Fprint(w, "event: endpoint\ndata: http://unreachable.invalid:1/message\n\n")
		flusher.Flush()
		fmt.Fprint(w, "event: keepalive\ndata: {}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{}}\n\n")
		flusher.Flush()

		// Keep the stream open like a real long-lived gateway connection,
		// until the client disconnects. Returning immediately here races the
		// SDK's own sseClientConn.Read: its reader goroutine would drain the
		// body to EOF, push the message, and close done, all before this
		// test's Read call — and Read's select between "message ready" and
		// "done closed" picks pseudo-randomly between them.
		<-r.Context().Done()
	})
	posted := make(chan struct{}, 1)
	mux.HandleFunc("/message", func(w http.ResponseWriter, r *http.Request) {
		posted <- struct{}{}
		w.WriteHeader(http.StatusAccepted)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	transport := NewSSEClientTransport(srv.URL+"/sse", nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := transport.Connect(ctx)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer conn.Close()

	// The keepalive must never surface as a decode error, and the real
	// message must come through.
	msg, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if msg == nil {
		t.Fatal("Read returned a nil message")
	}

	// Writing back must reach this server's /message handler — it can only
	// do so if the endpoint authority was rewritten from unreachable.invalid
	// to srv's real address.
	writeErrCh := make(chan error, 1)
	go func() { writeErrCh <- conn.Write(ctx, msg) }()

	select {
	case <-posted:
		// endpoint authority was correctly repaired.
	case err := <-writeErrCh:
		t.Fatalf("Write returned before reaching /message (endpoint not repaired?): %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the POST to reach /message — endpoint authority was not repaired")
	}
}
