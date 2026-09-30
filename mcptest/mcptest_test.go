package mcptest_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/go-mcp/mcptest"
	"github.com/hollis-labs/go-mcp/server"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeT records Fatalf instead of halting, so a test can assert that Connect
// failed. Connect must return on its own after Fatalf.
type fakeT struct {
	mu     sync.Mutex
	failed []string
}

func (f *fakeT) Helper() {}
func (f *fakeT) Fatalf(format string, args ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failed = append(f.failed, fmt.Sprintf(format, args...))
}
func (f *fakeT) msgs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.failed...)
}

// handshakeMethod reports whether method is part of the connect handshake.
func handshakeMethod(m string) bool { return m == "initialize" || m == "server/discover" }

// gate returns receiving middleware that runs on handshake requests only.
func gate(f func(ctx context.Context) error) mcpsdk.Middleware {
	return func(next mcpsdk.MethodHandler) mcpsdk.MethodHandler {
		return func(ctx context.Context, method string, req mcpsdk.Request) (mcpsdk.Result, error) {
			if handshakeMethod(method) {
				if err := f(ctx); err != nil {
					return nil, err
				}
			}
			return next(ctx, method, req)
		}
	}
}

func wantFailure(t *testing.T, ft *fakeT, cs *mcpsdk.ClientSession, cleanup func(), sub string) {
	t.Helper()
	msgs := ft.msgs()
	if len(msgs) == 0 {
		t.Fatalf("Connect did not fail; want a Fatalf containing %q", sub)
	}
	if !strings.Contains(strings.Join(msgs, "\n"), sub) {
		t.Fatalf("Fatalf messages %q do not contain %q", msgs, sub)
	}
	if cs != nil || cleanup != nil {
		t.Fatalf("Connect returned non-zero values after failure: cs=%v cleanup=%v", cs, cleanup != nil)
	}
}

func TestConnectSucceedsOnZeroToolServer(t *testing.T) {
	srv := server.NewServer("zero", "1.2.3")
	ft := &fakeT{}
	cs, cleanup := mcptest.Connect(ft, srv)
	if len(ft.msgs()) != 0 {
		t.Fatalf("unexpected failure: %q", ft.msgs())
	}
	defer cleanup()
	res := cs.InitializeResult()
	if res == nil || res.ServerInfo == nil || res.ServerInfo.Name != "zero" || res.ServerInfo.Version != "1.2.3" {
		t.Fatalf("InitializeResult = %+v, want server zero/1.2.3", res)
	}
	if _, err := cs.ListTools(context.Background(), nil); err != nil {
		t.Fatalf("session unusable after Connect: %v", err)
	}
}

func TestConnectSessionSurvivesHandshakeContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ft := &fakeT{}
	cs, cleanup := mcptest.Connect(ft, server.NewServer("s", "1"), mcptest.WithContext(ctx))
	if len(ft.msgs()) != 0 {
		t.Fatalf("unexpected failure: %q", ft.msgs())
	}
	defer cleanup()
	cancel() // WithContext bounds the handshake only
	if _, err := cs.ListTools(context.Background(), nil); err != nil {
		t.Fatalf("session died with the handshake context: %v", err)
	}
}

func TestCleanupClosesBothSessions(t *testing.T) {
	closed := make(chan struct{})
	srv := server.NewServer("s", "1")
	ft := &fakeT{}
	cs, cleanup := mcptest.Connect(ft, srv)
	if len(ft.msgs()) != 0 {
		t.Fatalf("unexpected failure: %q", ft.msgs())
	}
	go func() { _ = cs.Wait(); close(closed) }()

	cleanup()
	cleanup() // idempotent

	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("client session still open after cleanup")
	}
	if _, err := cs.ListTools(context.Background(), nil); err == nil {
		t.Fatal("ListTools succeeded after cleanup; client session not closed")
	}
	// The server side is closed too: its session list is empty.
	deadline := time.Now().Add(5 * time.Second)
	for {
		n := 0
		for range srv.SDKServer().Sessions() {
			n++
		}
		if n == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("server still has %d session(s) after cleanup", n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// --- Self-tests: Connect must be able to FAIL. ---

func TestSelfNeverCompletesInitialize(t *testing.T) {
	srv := server.NewServer("hang", "1", server.WithReceivingMiddleware(gate(func(ctx context.Context) error {
		<-ctx.Done() // never answers the handshake
		return ctx.Err()
	})))
	ft := &fakeT{}
	start := time.Now()
	cs, cleanup := mcptest.Connect(ft, srv, mcptest.WithHandshakeTimeout(200*time.Millisecond))
	wantFailure(t, ft, cs, cleanup, "handshake")
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("Connect took %v despite a 200ms handshake timeout", d)
	}
}

func TestSelfHandshakeRejectedByServer(t *testing.T) {
	srv := server.NewServer("reject", "1", server.WithReceivingMiddleware(gate(func(context.Context) error {
		return errors.New("refusing to initialize")
	})))
	ft := &fakeT{}
	cs, cleanup := mcptest.Connect(ft, srv)
	wantFailure(t, ft, cs, cleanup, "handshake")
}

func TestSelfProtocolVersionMismatch(t *testing.T) {
	// The server answers initialize with a protocol version no client speaks.
	srv := server.NewServer("old", "1", server.WithReceivingMiddleware(func(next mcpsdk.MethodHandler) mcpsdk.MethodHandler {
		return func(ctx context.Context, method string, req mcpsdk.Request) (mcpsdk.Result, error) {
			if method == "server/discover" { // force the legacy initialize path
				return nil, errors.New("discover unsupported")
			}
			res, err := next(ctx, method, req)
			if ir, ok := res.(*mcpsdk.InitializeResult); ok && err == nil {
				ir.ProtocolVersion = "1999-01-01"
			}
			return res, err
		}
	}))
	ft := &fakeT{}
	cs, cleanup := mcptest.Connect(ft, srv)
	wantFailure(t, ft, cs, cleanup, "1999-01-01")
}

func TestSelfServerClosesDuringHandshake(t *testing.T) {
	var srv *server.Server
	srv = server.NewServer("closer", "1", server.WithReceivingMiddleware(gate(func(context.Context) error {
		for ss := range srv.SDKServer().Sessions() {
			go func() { _ = ss.Close() }() // not inline: Close waits for this handler
		}
		return errors.New("closed")
	})))
	ft := &fakeT{}
	cs, cleanup := mcptest.Connect(ft, srv, mcptest.WithHandshakeTimeout(5*time.Second))
	wantFailure(t, ft, cs, cleanup, "handshake")
}

func TestSelfContextAlreadyCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ft := &fakeT{}
	cs, cleanup := mcptest.Connect(ft, server.NewServer("s", "1"), mcptest.WithContext(ctx))
	wantFailure(t, ft, cs, cleanup, "")
}

func TestSelfContextDeadlineAbortsHangingHandshake(t *testing.T) {
	srv := server.NewServer("hang", "1", server.WithReceivingMiddleware(gate(func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})))
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	ft := &fakeT{}
	start := time.Now()
	// Long handshake timeout: only the caller's ctx can end this in time.
	cs, cleanup := mcptest.Connect(ft, srv, mcptest.WithContext(ctx), mcptest.WithHandshakeTimeout(time.Minute))
	wantFailure(t, ft, cs, cleanup, "handshake")
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("Connect ignored the context deadline: took %v", d)
	}
}

func TestSelfNilServer(t *testing.T) {
	ft := &fakeT{}
	cs, cleanup := mcptest.Connect(ft, nil)
	wantFailure(t, ft, cs, cleanup, "nil server")
}

// --- Options reach the underlying client. ---

// serverSawParams waits for the server's session to hold the client's
// initialize params (what the client actually sent) and returns them.
func serverSawParams(t *testing.T, s *server.Server) *mcpsdk.InitializeParams {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for ss := range s.SDKServer().Sessions() {
			if p := ss.InitializeParams(); p != nil {
				return p
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("server never saw client initialize params")
	return nil
}

func TestWithClientIdentityReachesServer(t *testing.T) {
	s := server.NewServer("s", "1")
	ft := &fakeT{}
	_, cleanup := mcptest.Connect(ft, s, mcptest.WithClientIdentity("my-client", "9.9.9"))
	if len(ft.msgs()) != 0 {
		t.Fatalf("unexpected failure: %q", ft.msgs())
	}
	defer cleanup()
	p := serverSawParams(t, s)
	if p.ClientInfo == nil || p.ClientInfo.Name != "my-client" || p.ClientInfo.Version != "9.9.9" {
		t.Fatalf("server saw client %+v, want my-client/9.9.9", p.ClientInfo)
	}
}

func TestDefaultClientIdentity(t *testing.T) {
	s := server.NewServer("s", "1")
	ft := &fakeT{}
	_, cleanup := mcptest.Connect(ft, s)
	if len(ft.msgs()) != 0 {
		t.Fatalf("unexpected failure: %q", ft.msgs())
	}
	defer cleanup()
	p := serverSawParams(t, s)
	if p.ClientInfo == nil || p.ClientInfo.Name != mcptest.DefaultClientName || p.ClientInfo.Version != mcptest.DefaultClientVersion {
		t.Fatalf("default client identity = %+v", p.ClientInfo)
	}
}

func TestWithClientOptionsReachesClient(t *testing.T) {
	// A client-side handler is advertised as a capability during the
	// handshake only if the options reached mcpsdk.NewClient.
	opts := &mcpsdk.ClientOptions{
		ElicitationHandler: func(context.Context, *mcpsdk.ElicitRequest) (*mcpsdk.ElicitResult, error) {
			return &mcpsdk.ElicitResult{Action: "cancel"}, nil
		},
	}
	ft := &fakeT{}
	with := server.NewServer("s", "1")
	_, cleanup := mcptest.Connect(ft, with, mcptest.WithClientOptions(opts))
	defer cleanup()
	without := server.NewServer("s", "1")
	_, cleanup2 := mcptest.Connect(ft, without)
	defer cleanup2()
	if len(ft.msgs()) != 0 {
		t.Fatalf("unexpected failure: %q", ft.msgs())
	}
	if c := serverSawParams(t, with).Capabilities; c == nil || c.Elicitation == nil {
		t.Fatalf("elicitation capability not advertised with WithClientOptions: %+v", c)
	}
	if c := serverSawParams(t, without).Capabilities; c != nil && c.Elicitation != nil {
		t.Fatalf("elicitation capability advertised without options: %+v", c)
	}
}
