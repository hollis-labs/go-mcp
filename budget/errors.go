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

// ToolError is a structured, tool-execution error: reported inside a
// successful CallToolResult's content (IsError=true), not as a
// protocol-level JSON-RPC error -- see ProtocolError for that case. Return
// one as a ToolHandler's error and the server package gives it the same
// structured-content treatment as a successful result, instead of
// collapsing it to a bare error string.
//
// Code is a short, app-owned, freeform string (e.g. "not_found",
// "insufficient_scope") -- deliberately not a fixed enum, since each app's
// tool vocabulary differs and go-mcp does not own it.
//
// The point of the rest of the fields is that the caller already knows the
// error and its context; giving the calling agent a concrete next step
// costs nothing extra and saves it a guess. NextStep is where that goes:
// state what the agent should do now ("call hadron_runs_list to find a
// valid run_id"), not just what went wrong.
type ToolError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Field     string `json:"field,omitempty"`
	Retryable bool   `json:"retryable"`
	NextStep  string `json:"nextStep,omitempty"`
	HelpTool  string `json:"helpTool,omitempty"`
}

// Error implements the error interface.
func (e *ToolError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// NewToolError builds a ToolError with just code and message set. Chain
// WithField/WithRetryable/WithNextStep/WithHelpTool to add the rest.
func NewToolError(code, message string) *ToolError {
	return &ToolError{Code: code, Message: message}
}

// WithField sets the affected argument or entity field, when applicable.
func (e *ToolError) WithField(field string) *ToolError {
	e.Field = field
	return e
}

// WithRetryable marks whether retrying the same call is safe. Defaults to
// false: don't imply retry safety unless the caller knows it holds.
func (e *ToolError) WithRetryable(retryable bool) *ToolError {
	e.Retryable = retryable
	return e
}

// WithNextStep sets what the calling agent should do now -- the field this
// type exists for. Phrase it as an action, not a restatement of the error.
func (e *ToolError) WithNextStep(step string) *ToolError {
	e.NextStep = step
	return e
}

// WithHelpTool names another tool the agent can call for more context or
// correction (e.g. a list/search tool to find a valid id).
func (e *ToolError) WithHelpTool(tool string) *ToolError {
	e.HelpTool = tool
	return e
}

// StructuredError lets a ToolHandler report a fully custom structured error
// shape while still getting go-mcp's error-content treatment
// (StructuredContent populated, IsError set) -- for a caller whose error
// contract doesn't fit ToolError's fields, for example one that deliberately
// omits a human-readable message for data-minimization reasons. Prefer
// ToolError when its fields fit; reach for this only when they genuinely
// don't.
type StructuredError interface {
	error
	// ToolErrorContent returns the value to marshal into StructuredContent
	// (and its mirrored text block) in place of err.Error().
	ToolErrorContent() any
}

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
