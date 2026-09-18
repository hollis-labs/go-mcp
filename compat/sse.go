package compat

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// NewSSEClientTransport returns an [mcp.Transport] for SSE-based MCP
// servers (the 2024-11-05 transport, removed from later spec versions but
// still spoken by upstream servers go-mcp doesn't control).
//
// It wraps the official SDK's [mcp.SSEClientTransport] with a sanitizing
// http.RoundTripper that fixes two real-world SSE gateway behaviors the
// SDK's stock reader does not tolerate — ported forward from Nanite PR
// #303's hand debugging of exactly these two bugs against a production
// gateway:
//
//  1. Non-message events. The SDK's SSE read loop pushes every event's data
//     at the JSON-RPC decoder, despite its own docs saying reads are SSE
//     "message" events. A gateway emitting `event: keepalive` with
//     `data: {}` every few seconds kills the session on the first one —
//     during initialize, which looks like a handshake failure rather than
//     a keepalive. Events other than "endpoint" and the default "message"
//     type are dropped here, before the SDK ever sees them.
//  2. An endpoint URL naming the wrong authority. A gateway can advertise
//     an absolute endpoint URL — e.g.
//     `http://gateway-host/servers/<id>/message?session_id=...` — missing
//     the port it's actually served on, or naming a different host
//     entirely. The SDK resolves it against the stream URL, and an
//     absolute URL wins outright, so every subsequent POST would go to the
//     wrong place. The authority is rewritten to the one actually dialed;
//     the path and query are the server's to choose.
//
// It also bounds how much of one not-yet-terminated event block it will
// buffer while scanning for the blank-line terminator (maxPartialEventBytes)
// -- a server that never sends one would otherwise grow that buffer without
// limit, exhausting memory before the downstream transport's own
// MaxEventSize cap ever sees a complete block to check.
//
// All three are done at the byte level, in the RoundTripper, because the
// SDK's SSEClientTransport takes an *http.Client and exposes no other seam.
//
// httpClient, if non-nil, supplies the base configuration (Timeout,
// CheckRedirect, cookie jar, ...); only its Transport is wrapped, not
// discarded. A nil httpClient behaves like http.DefaultClient, sanitized.
func NewSSEClientTransport(endpoint string, httpClient *http.Client) *mcp.SSEClientTransport {
	base := http.RoundTripper(http.DefaultTransport)
	client := &http.Client{}
	if httpClient != nil {
		*client = *httpClient
		if httpClient.Transport != nil {
			base = httpClient.Transport
		}
	}
	client.Transport = &sanitizingRoundTripper{inner: base}

	return &mcp.SSEClientTransport{
		Endpoint:   endpoint,
		HTTPClient: client,
	}
}

// sanitizingRoundTripper wraps a text/event-stream response body with
// [newSanitizingReader]. Every other response passes through untouched.
type sanitizingRoundTripper struct {
	inner http.RoundTripper
}

func (rt *sanitizingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := rt.inner.RoundTrip(req)
	if err != nil || resp == nil || !isEventStream(resp) {
		return resp, err
	}
	resp.Body = newSanitizingReader(resp.Body, req.URL)
	return resp, nil
}

func isEventStream(resp *http.Response) bool {
	mediaType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	return err == nil && mediaType == "text/event-stream"
}

// maxPartialEventBytes bounds how much of one not-yet-terminated SSE event
// block sanitizingReader will buffer while waiting for its blank-line
// terminator. Without this, a server that never sends one -- a
// misbehaving upstream, or a deliberately hostile one -- makes drainBlocks
// grow partial without limit, exhausting memory before the downstream SDK
// transport's own MaxEventSize cap ever sees a complete block to check
// against. This mirrors the same defense Nanite's own eventCapReader
// applied ahead of its (now-retired) hand-rolled SSE sanitizer, applied
// here since every caller of this package's transport needs it, not just
// Nanite. 1 MiB is comfortably larger than any real SSE event line
// (endpoint/keepalive events are tiny; message events carry one JSON-RPC
// frame, themselves bounded by the transport's MaxEventSize) while still
// bounding worst-case memory well below that cap.
const maxPartialEventBytes = 1024 * 1024

// sanitizingReader adapts an SSE stream to what the MCP SDK's SSE client
// assumes. See [NewSSEClientTransport] for the two behaviors it corrects.
type sanitizingReader struct {
	inner io.ReadCloser
	// authority is scheme://host[:port] of the stream we connected to, used
	// to repair an endpoint event that names a different one.
	authority *url.URL

	pending bytes.Buffer // complete, already-sanitized bytes waiting to be read
	partial bytes.Buffer // an event block not yet terminated by a blank line
	err     error
}

func newSanitizingReader(inner io.ReadCloser, streamURL *url.URL) *sanitizingReader {
	return &sanitizingReader{inner: inner, authority: streamURL}
}

func (r *sanitizingReader) Read(p []byte) (int, error) {
	for r.pending.Len() == 0 {
		if r.err != nil {
			return 0, r.err
		}
		buf := make([]byte, 4096)
		n, err := r.inner.Read(buf)
		if n > 0 {
			r.partial.Write(buf[:n])
			r.drainBlocks()
			if r.partial.Len() > maxPartialEventBytes {
				r.err = fmt.Errorf("sse: event block exceeded %d bytes without a terminating blank line", maxPartialEventBytes)
				return 0, r.err
			}
		}
		if err != nil {
			r.err = err
			// Flush whatever is left so a final unterminated block is not lost.
			if r.partial.Len() > 0 {
				r.pending.Write(r.partial.Bytes())
				r.partial.Reset()
			}
			if r.pending.Len() == 0 {
				return 0, err
			}
		}
	}
	return r.pending.Read(p)
}

// drainBlocks moves every complete event block from partial to pending,
// dropping or rewriting as needed. An SSE block ends at a blank line.
func (r *sanitizingReader) drainBlocks() {
	for {
		data := r.partial.Bytes()
		// A block ends at a blank line, which is "\n\n" with LF endings and
		// "\r\n\r\n" with CRLF. Searching only for "\n\n" finds nothing in
		// "\r\n\r\n" — the bytes are \r \n \r \n — so a CRLF server's stream
		// is buffered forever and the client hangs waiting for a reply that
		// already arrived. Some gateways send CRLF.
		idx, width := blockEnd(data)
		if idx < 0 {
			return
		}
		block := string(data[:idx+width])
		r.partial.Next(idx + width)
		if out, keep := r.sanitizeBlock(block); keep {
			r.pending.WriteString(out)
		}
	}
}

// blockEnd finds the first blank-line terminator, returning its offset and
// length so both CRLF and LF streams are handled. Returns -1 when the buffer
// holds no complete block yet.
func blockEnd(data []byte) (int, int) {
	crlf := bytes.Index(data, []byte("\r\n\r\n"))
	lf := bytes.Index(data, []byte("\n\n"))
	switch {
	case crlf < 0 && lf < 0:
		return -1, 0
	case crlf < 0:
		return lf, 2
	case lf < 0:
		return crlf, 4
	case crlf <= lf:
		return crlf, 4
	default:
		// An LF-terminated block earlier in the buffer than any CRLF one.
		return lf, 2
	}
}

// sanitizeBlock decides the fate of one event block.
func (r *sanitizingReader) sanitizeBlock(block string) (string, bool) {
	name := ""
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimRight(line, "\r")
		if v, ok := strings.CutPrefix(line, "event:"); ok {
			name = strings.TrimSpace(v)
			break
		}
	}

	switch name {
	case "", "message":
		// The default event type. Keep as-is — this is a JSON-RPC message.
		return block, true
	case "endpoint":
		return r.repairEndpoint(block), true
	default:
		// keepalive, ping, or anything else a server invents. Not JSON-RPC,
		// and the SDK would hand it to the decoder regardless.
		return "", false
	}
}

// repairEndpoint rewrites the endpoint event's data URL onto the authority we
// actually connected to, when the server names a different one.
func (r *sanitizingReader) repairEndpoint(block string) string {
	if r.authority == nil {
		return block
	}
	lines := strings.Split(block, "\n")
	for i, line := range lines {
		bare := strings.TrimRight(line, "\r")
		v, ok := strings.CutPrefix(bare, "data:")
		if !ok {
			continue
		}
		// Preserve the line ending the server used.
		eol := ""
		if strings.HasSuffix(line, "\r") {
			eol = "\r"
		}
		raw := strings.TrimSpace(v)
		parsed, err := url.Parse(raw)
		if err != nil || !parsed.IsAbs() {
			// Relative endpoints resolve correctly on their own.
			return block
		}
		if parsed.Host == r.authority.Host {
			return block
		}
		parsed.Scheme = r.authority.Scheme
		parsed.Host = r.authority.Host
		lines[i] = "data: " + parsed.String() + eol
		return strings.Join(lines, "\n")
	}
	return block
}

func (r *sanitizingReader) Close() error { return r.inner.Close() }
