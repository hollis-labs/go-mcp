package compat

import (
	"errors"
	"io"
	"net/url"
	"strings"
	"testing"
)

type nopCloser struct{ io.Reader }

func (nopCloser) Close() error { return nil }

func sanitizeStream(t *testing.T, stream, streamURL string) string {
	t.Helper()
	u, err := url.Parse(streamURL)
	if err != nil {
		t.Fatalf("bad test url: %v", err)
	}
	r := newSanitizingReader(nopCloser{strings.NewReader(stream)}, u)
	out, err := io.ReadAll(r)
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("read: %v", err)
	}
	return string(out)
}

// The exact stream a real gateway sent, which killed the session on the first
// keepalive with `invalid message version tag ""; expected "2.0"`.
const gatewayStream = "event: endpoint\n" +
	"data: http://mcp-gateway/servers/abc/message?session_id=s1\n" +
	"retry: 5000\n\n" +
	"event: keepalive\ndata: {}\nretry: 5000\n\n" +
	"event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{}}\n\n" +
	"event: keepalive\ndata: {}\n\n"

func TestSanitize_dropsKeepalives(t *testing.T) {
	got := sanitizeStream(t, gatewayStream, "http://mcp-gateway:4444/servers/abc/sse")
	if strings.Contains(got, "keepalive") {
		t.Fatalf("keepalive survived:\n%s", got)
	}
	if !strings.Contains(got, `"jsonrpc":"2.0"`) {
		t.Fatalf("the real message was dropped:\n%s", got)
	}
}

func TestSanitize_repairsEndpointPort(t *testing.T) {
	got := sanitizeStream(t, gatewayStream, "http://mcp-gateway:4444/servers/abc/sse")
	if !strings.Contains(got, "http://mcp-gateway:4444/servers/abc/message?session_id=s1") {
		t.Fatalf("endpoint authority not repaired:\n%s", got)
	}
}

func TestSanitize_leavesACorrectEndpointAlone(t *testing.T) {
	stream := "event: endpoint\ndata: http://host:9/m?session_id=s\n\n"
	got := sanitizeStream(t, stream, "http://host:9/servers/abc/sse")
	if !strings.Contains(got, "http://host:9/m?session_id=s") {
		t.Fatalf("a correct endpoint was altered:\n%s", got)
	}
}

func TestSanitize_leavesARelativeEndpointAlone(t *testing.T) {
	// The SDK resolves relative endpoints against the stream URL correctly.
	stream := "event: endpoint\ndata: /servers/abc/message?session_id=s\n\n"
	got := sanitizeStream(t, stream, "http://host:4444/servers/abc/sse")
	if !strings.Contains(got, "data: /servers/abc/message?session_id=s") {
		t.Fatalf("a relative endpoint was rewritten:\n%s", got)
	}
}

func TestSanitize_keepsUnnamedEvents(t *testing.T) {
	// No `event:` line means the default type, which is a message.
	stream := "data: {\"jsonrpc\":\"2.0\",\"id\":2}\n\n"
	if got := sanitizeStream(t, stream, "http://h:1/sse"); !strings.Contains(got, `"id":2`) {
		t.Fatalf("default-typed event dropped:\n%s", got)
	}
}

func TestSanitize_survivesSplitReads(t *testing.T) {
	// A block arriving across several Reads must not be truncated or duplicated.
	r := newSanitizingReader(nopCloser{&slowReader{s: gatewayStream, n: 7}}, mustURL(t, "http://mcp-gateway:4444/servers/abc/sse"))
	out, err := io.ReadAll(r)
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("read: %v", err)
	}
	got := string(out)
	if strings.Contains(got, "keepalive") || !strings.Contains(got, `"jsonrpc":"2.0"`) {
		t.Fatalf("split reads mishandled:\n%s", got)
	}
	if strings.Count(got, `"jsonrpc":"2.0"`) != 1 {
		t.Fatalf("message duplicated:\n%s", got)
	}
}

type slowReader struct {
	s string
	n int
}

func (r *slowReader) Read(p []byte) (int, error) {
	if r.s == "" {
		return 0, io.EOF
	}
	n := r.n
	if n > len(r.s) {
		n = len(r.s)
	}
	if n > len(p) {
		n = len(p)
	}
	copy(p, r.s[:n])
	r.s = r.s[n:]
	return n, nil
}

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatalf("bad url: %v", err)
	}
	return u
}

// Some gateways terminate blocks with CRLF. Searching only for "\n\n" finds
// nothing in "\r\n\r\n" — the bytes are \r \n \r \n — so the reader buffered
// the whole stream and the client hung waiting for a reply that had already
// arrived.
const gatewayStreamCRLF = "event: endpoint\r\n" +
	"data: http://mcp-gateway/servers/abc/message?session_id=s1\r\n" +
	"retry: 5000\r\n\r\n" +
	"event: keepalive\r\ndata: {}\r\nretry: 5000\r\n\r\n" +
	"event: message\r\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{}}\r\n\r\n"

func TestSanitize_handlesCRLFStreams(t *testing.T) {
	got := sanitizeStream(t, gatewayStreamCRLF, "http://mcp-gateway:4444/servers/abc/sse")
	if strings.Contains(got, "keepalive") {
		t.Fatalf("keepalive survived:\n%q", got)
	}
	if !strings.Contains(got, `"jsonrpc":"2.0"`) {
		t.Fatalf("the message never came through — the exact hang:\n%q", got)
	}
	if !strings.Contains(got, "http://mcp-gateway:4444/servers/abc/message?session_id=s1") {
		t.Fatalf("endpoint not repaired on a CRLF stream:\n%q", got)
	}
}

func TestSanitize_CRLFSplitReads(t *testing.T) {
	r := newSanitizingReader(nopCloser{&slowReader{s: gatewayStreamCRLF, n: 5}},
		mustURL(t, "http://mcp-gateway:4444/servers/abc/sse"))
	out, err := io.ReadAll(r)
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("read: %v", err)
	}
	got := string(out)
	if strings.Contains(got, "keepalive") || strings.Count(got, `"jsonrpc":"2.0"`) != 1 {
		t.Fatalf("CRLF split reads mishandled:\n%q", got)
	}
}

func TestSanitize_preservesCRLFOnARepairedEndpoint(t *testing.T) {
	// The rewriter must not silently convert the server's line endings
	// mid-stream.
	got := sanitizeStream(t, gatewayStreamCRLF, "http://mcp-gateway:4444/servers/abc/sse")
	if !strings.Contains(got, "?session_id=s1\r\n") {
		t.Fatalf("line ending not preserved on the rewritten data line:\n%q", got)
	}
}
