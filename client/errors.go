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
