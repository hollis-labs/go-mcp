package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
)

func TestStaticProvider_AcceptsKnownToken(t *testing.T) {
	p := NewStaticProvider(map[string]string{"good-token": "alice"})
	verifier := p.Verifier()

	req := httptest.NewRequest("POST", "/", nil)
	info, err := verifier(context.Background(), "good-token", req)
	if err != nil {
		t.Fatalf("Verifier() error = %v, want nil", err)
	}
	if info.UserID != "alice" {
		t.Errorf("UserID = %q, want alice", info.UserID)
	}
}

func TestStaticProvider_RejectsUnknownToken(t *testing.T) {
	p := NewStaticProvider(map[string]string{"good-token": "alice"})
	verifier := p.Verifier()

	req := httptest.NewRequest("POST", "/", nil)
	_, err := verifier(context.Background(), "wrong-token", req)
	if !errors.Is(err, sdkauth.ErrInvalidToken) {
		t.Errorf("error = %v, want ErrInvalidToken", err)
	}
}

func TestStaticProvider_Options_AllowsMissingExpiration(t *testing.T) {
	p := NewStaticProvider(map[string]string{"t": "u"})
	opts := p.Options()
	if !opts.AllowMissingExpiration {
		t.Error("AllowMissingExpiration = false, want true for static long-lived tokens")
	}
}

func TestStaticProvider_WithScopes(t *testing.T) {
	p := NewStaticProvider(map[string]string{"t": "u"}).WithScopes("read", "write")

	if got := p.Options().Scopes; len(got) != 2 || got[0] != "read" || got[1] != "write" {
		t.Errorf("Options().Scopes = %v, want [read write]", got)
	}

	info, err := p.Verifier()(context.Background(), "t", httptest.NewRequest("POST", "/", nil))
	if err != nil {
		t.Fatalf("Verifier() error = %v", err)
	}
	if len(info.Scopes) != 2 || info.Scopes[0] != "read" {
		t.Errorf("TokenInfo.Scopes = %v, want [read write]", info.Scopes)
	}
}

func TestHTTPMiddleware_NilProviderIsPassthrough(t *testing.T) {
	mw := HTTPMiddleware(nil)
	called := false
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !called {
		t.Error("nil Provider did not pass the request through")
	}
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200 (default recorder status)", rec.Code)
	}
}
