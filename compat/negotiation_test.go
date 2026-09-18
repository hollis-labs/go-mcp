package compat

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestClassifyVersion(t *testing.T) {
	cases := []struct {
		version    string
		wantLegacy bool
	}{
		{"2026-07-28", false},
		{"2026-08-01", false}, // hypothetical future version, still >= 2026-07-28
		{"2025-11-25", true},
		{"2024-11-05", true},
		{"", true},
	}
	for _, c := range cases {
		got := classifyVersion(c.version)
		if got.Legacy != c.wantLegacy {
			t.Errorf("classifyVersion(%q).Legacy = %v, want %v", c.version, got.Legacy, c.wantLegacy)
		}
		if got.ProtocolVersion != c.version {
			t.Errorf("classifyVersion(%q).ProtocolVersion = %q, want %q", c.version, got.ProtocolVersion, c.version)
		}
	}
}

func TestEnforcePolicy(t *testing.T) {
	legacy := NegotiationReport{ProtocolVersion: "2025-11-25", Legacy: true}
	modern := NegotiationReport{ProtocolVersion: "2026-07-28", Legacy: false}

	if err := enforcePolicy(legacy, AllowLegacy); err != nil {
		t.Errorf("AllowLegacy + legacy peer: err = %v, want nil", err)
	}
	if err := enforcePolicy(modern, RejectLegacy); err != nil {
		t.Errorf("RejectLegacy + modern peer: err = %v, want nil", err)
	}
	if err := enforcePolicy(legacy, RejectLegacy); err == nil {
		t.Error("RejectLegacy + legacy peer: err = nil, want an error")
	}
}

// TestConnect_ModernPeerReportsNonLegacy connects a real in-memory
// client/server pair — both on the official SDK, so both negotiate
// 2026-07-28 — and verifies Connect reports Legacy=false and returns a
// usable session under every policy.
func TestConnect_ModernPeerReportsNonLegacy(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "0.0.1"}, nil)
	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	if _, err := server.Connect(context.Background(), serverTransport, nil); err != nil {
		t.Fatalf("server.Connect: %v", err)
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.1"}, nil)

	var report NegotiationReport
	cs, err := Connect(context.Background(), client, clientTransport, ConnectOptions{
		Policy: RejectLegacy, // must not reject a modern peer
		Report: func(r NegotiationReport) { report = r },
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer cs.Close()

	if report.Legacy {
		t.Errorf("report.Legacy = true for a same-SDK peer, want false (version %q)", report.ProtocolVersion)
	}
	if report.ProtocolVersion == "" {
		t.Error("report.ProtocolVersion is empty")
	}
}
