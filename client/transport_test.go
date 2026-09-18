package client

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNormalizeTransport(t *testing.T) {
	tests := []struct {
		raw     string
		want    string
		wantErr bool
	}{
		{"", TransportStdio, false},
		{"stdio", TransportStdio, false},
		{"http", TransportHTTP, false},
		{"streamable_http", TransportHTTP, false},
		{"streamable-http", TransportHTTP, false},
		{"HTTP", TransportHTTP, false},
		{"sse", TransportSSE, false},
		{" sse ", TransportSSE, false},
		{"websocket", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			got, err := normalizeTransport(tt.raw)
			if (err != nil) != tt.wantErr {
				t.Fatalf("normalizeTransport(%q) error = %v, wantErr %v", tt.raw, err, tt.wantErr)
			}
			if err == nil && got != tt.want {
				t.Errorf("normalizeTransport(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestStaticHeaderRoundTripper_SetsHeadersOnEveryRequest(t *testing.T) {
	var gotAuth, gotAPIKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAPIKey = r.Header.Get("X-Api-Key")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := defaultHTTPClientBuilder(map[string]string{
		"Authorization": "Bearer secret",
		"X-Api-Key":     "abc123",
	}, 0)

	for i := 0; i < 2; i++ {
		resp, err := c.Get(srv.URL)
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		resp.Body.Close()
		if gotAuth != "Bearer secret" || gotAPIKey != "abc123" {
			t.Fatalf("request %d: got Authorization=%q X-Api-Key=%q", i, gotAuth, gotAPIKey)
		}
	}
}

func TestDefaultHTTPClientBuilder_NoHeadersNoTimeout(t *testing.T) {
	c := defaultHTTPClientBuilder(nil, 0)
	if c.Transport != nil {
		t.Errorf("Transport = %v, want nil when no headers configured", c.Transport)
	}
	if c.Timeout != 0 {
		t.Errorf("Timeout = %v, want 0 when TimeoutSeconds is 0", c.Timeout)
	}
}

func TestDefaultHTTPClientBuilder_TimeoutSet(t *testing.T) {
	c := defaultHTTPClientBuilder(nil, 5)
	if c.Timeout.Seconds() != 5 {
		t.Errorf("Timeout = %v, want 5s", c.Timeout)
	}
}
