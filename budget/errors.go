package budget

import "fmt"

// ErrorCode is an application-owned JSON-RPC error code, for signaling a
// protocol-level MCP error (as opposed to a tool-execution error reported in
// a successful CallToolResult's content -- see [ToolError] for that case).
//
// The MCP 2026-07-28 specification allocates -32000..-32019 for
// implementation-defined errors and reserves -32020..-32099 for the
// specification's own future use. ErrorCode values must stay within the
// app-owned band; [NewProtocolError] panics if given a code outside it.
type ErrorCode int

// App-owned error codes (-32000..-32019). Add new codes here, in order,
// rather than reusing a retired one: a retired code stays reserved so old
// client-side handling never silently matches a different error kind.
const (
	// ErrCodeInternal is an unclassified server-side failure.
	ErrCodeInternal ErrorCode = -32000
	// ErrCodeInvalidInput reports that caller-supplied arguments failed
	// validation before the tool ran.
	ErrCodeInvalidInput ErrorCode = -32001
	// ErrCodeNotFound reports that a referenced entity does not exist.
	ErrCodeNotFound ErrorCode = -32002
	// ErrCodeForbidden reports that the caller lacks permission for the
	// requested operation.
	ErrCodeForbidden ErrorCode = -32003
	// ErrCodeUnavailable reports that a required dependency is temporarily
	// down or unreachable.
	ErrCodeUnavailable ErrorCode = -32004
	// ErrCodeRateLimited reports that the caller exceeded a rate or budget
	// limit.
	ErrCodeRateLimited ErrorCode = -32005
)

// maxAppOwnedCode and minAppOwnedCode bound the app-owned error-code band
// (-32000..-32019); -32020..-32099 is reserved for the MCP specification.
const (
	maxAppOwnedCode ErrorCode = -32000
	minAppOwnedCode ErrorCode = -32019
)

// ProtocolError is a structured, protocol-level MCP error: an app-owned
// error code, a message, and optional machine-readable data. It implements
// error, and its fields match the shape MCP transports expect for a
// JSON-RPC error object (code/message/data), so a caller building its own
// protocol-level error response (as opposed to embedding an error in tool
// result content) can construct one from a ProtocolError's fields directly.
type ProtocolError struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
	Data    any       `json:"data,omitempty"`
}

// Error implements the error interface.
func (e *ProtocolError) Error() string {
	return fmt.Sprintf("mcp error %d: %s", e.Code, e.Message)
}

// NewProtocolError builds a ProtocolError. It panics if code falls outside
// the app-owned range (-32000..-32019) -- a go-mcp caller programming error,
// not a runtime condition callers need to handle.
func NewProtocolError(code ErrorCode, message string, data any) *ProtocolError {
	if code > maxAppOwnedCode || code < minAppOwnedCode {
		panic(fmt.Sprintf("budget: error code %d is outside the app-owned range (%d..%d)", code, minAppOwnedCode, maxAppOwnedCode))
	}
	return &ProtocolError{Code: code, Message: message, Data: data}
}
