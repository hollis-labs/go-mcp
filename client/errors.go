package client

import (
	"errors"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// IsRecoverableError reports whether err indicates a broken connection worth
// invalidating and re-dialing, rather than a permanent failure (a bad tool
// name, a malformed request, an auth rejection) that a reconnect can't fix.
//
// Ported from Hadron's isRecoverableExternalClientError, exported here
// because more than one consumer needs it (Nanite's own app-layer retry
// policies -- see the client package doc -- reuse this same classifier).
// It checks two SDK sentinel errors via errors.Is, plus a substring
// fallback on the lowercased message for phrasings the SDK doesn't always
// wrap in those sentinels. The substring fallback is a pragmatic
// concession, not a robust one: it depends on error-message text rather
// than solely typed errors, and inherits whatever imprecision that carries
// from the shape Hadron's production use already tolerates.
func IsRecoverableError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, mcpsdk.ErrConnectionClosed) || errors.Is(err, mcpsdk.ErrSessionMissing) {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, s := range []string{
		"transport closed",
		"connection closed",
		"session terminated",
		"session not found",
		"connection lost",
	} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

// IsProvablyUnsent reports whether err is a tools/call failure where the
// request provably never reached the server, so retrying cannot repeat a
// side effect. It is nil-safe.
//
// The official SDK's jsonrpc2 connection rejects a call with "client is
// closing" in exactly three cases -- Close already in progress, a prior
// write failure, or the read side already failed (a stdio server's
// subprocess died between calls) -- and all three checks run before the
// request is written. Every other failure, including the errors
// IsRecoverableError accepts, is ambiguous about delivery.
//
// The match is on message text, not a code: -32003 is also
// budget.ErrCodeForbidden, so a code alone would misclassify a forbidden
// response as unsent.
func IsProvablyUnsent(err error) bool {
	return err != nil && strings.Contains(err.Error(), "client is closing")
}
