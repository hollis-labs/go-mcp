package client

import (
	"errors"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestIsRecoverableError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"sdk connection closed sentinel", mcpsdk.ErrConnectionClosed, true},
		{"sdk session missing sentinel", mcpsdk.ErrSessionMissing, true},
		{"wrapped sentinel", errors.New("wrap: " + mcpsdk.ErrConnectionClosed.Error()), true}, // substring path, not errors.Is
		{"transport closed message", errors.New("mcp: transport closed unexpectedly"), true},
		{"connection lost message", errors.New("write tcp: connection lost"), true},
		{"session terminated message", errors.New("server reported session terminated"), true},
		{"unrelated error", errors.New("unknown tool \"foo\""), false},
		{"context deadline", errors.New("context deadline exceeded"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsRecoverableError(tt.err); got != tt.want {
				t.Errorf("IsRecoverableError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
