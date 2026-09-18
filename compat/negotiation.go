package compat

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// protocolVersion20260728 mirrors the official SDK's own unexported
// constant of the same name. Duplicated here rather than imported because
// the SDK does not export it — see [Connect].
const protocolVersion20260728 = "2026-07-28"

// LegacyPolicy controls how [Connect] treats a peer that negotiates a
// protocol version below 2026-07-28.
type LegacyPolicy int

const (
	// AllowLegacy accepts the session — the same outcome as calling
	// (*mcp.Client).Connect directly. Report, if set, still runs.
	AllowLegacy LegacyPolicy = iota
	// RejectLegacy closes the session and returns an error instead of
	// handing back one negotiated below 2026-07-28.
	RejectLegacy
)

// NegotiationReport is passed to [ConnectOptions.Report] with the outcome
// of protocol-version negotiation.
type NegotiationReport struct {
	// ProtocolVersion is what the peer actually negotiated (the SDK's own
	// InitializeResult.ProtocolVersion).
	ProtocolVersion string
	// Legacy is true when ProtocolVersion is below 2026-07-28 — i.e. the
	// SDK's SEP-2575 server/discover probe failed and it fell back to the
	// legacy initialize handshake.
	Legacy bool
}

// ConnectOptions configures [Connect]'s legacy-negotiation handling.
type ConnectOptions struct {
	// Policy decides what happens when the peer negotiates a legacy
	// (pre-2026-07-28) protocol version. The zero value is AllowLegacy.
	Policy LegacyPolicy
	// Report, if non-nil, is called once with the negotiation outcome —
	// including a successful 2026-07-28 negotiation — so a caller can
	// log or alert on a fallback independent of whether Policy also
	// rejects it.
	Report func(NegotiationReport)
}

// Connect wraps (*mcp.Client).Connect to make the SDK's fallback to the
// legacy (pre-2026-07-28) initialize handshake explicit and, optionally,
// rejectable.
//
// The official SDK already negotiates protocol version down to 2025-11-25
// for older peers internally — see (*mcp.Client).Connect, which tries the
// SEP-2575 server/discover RPC first and silently falls back to legacy
// initialize on any non-modern response — but it exposes no public option
// to observe or refuse that fallback: [mcp.ClientSessionOptions] carries no
// public field to force a version, and nothing short of inspecting
// [mcp.ClientSession.InitializeResult] after the fact reveals which path
// was taken.
//
// This is a compat adapter, not part of the 2026-07-28 core: once every
// peer go-mcp talks to supports 2026-07-28, delete this file and call
// client.Connect directly. No core code depends on this existing.
func Connect(ctx context.Context, client *mcp.Client, t mcp.Transport, opts ConnectOptions) (*mcp.ClientSession, error) {
	cs, err := client.Connect(ctx, t, nil)
	if err != nil {
		return nil, err
	}

	version := ""
	if result := cs.InitializeResult(); result != nil {
		version = result.ProtocolVersion
	}
	report := classifyVersion(version)

	if opts.Report != nil {
		opts.Report(report)
	}

	if err := enforcePolicy(report, opts.Policy); err != nil {
		_ = cs.Close()
		return nil, err
	}
	return cs, nil
}

// classifyVersion reports whether the given negotiated protocol version is
// a legacy (pre-2026-07-28) one.
func classifyVersion(version string) NegotiationReport {
	return NegotiationReport{ProtocolVersion: version, Legacy: version < protocolVersion20260728}
}

// enforcePolicy returns a non-nil error when report and policy together
// call for rejecting the connection.
func enforcePolicy(report NegotiationReport, policy LegacyPolicy) error {
	if report.Legacy && policy == RejectLegacy {
		return fmt.Errorf("compat: peer negotiated legacy protocol version %q, want >= %q", report.ProtocolVersion, protocolVersion20260728)
	}
	return nil
}
