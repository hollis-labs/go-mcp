// Package server is a thin wrapper around the official MCP Go SDK
// (github.com/modelcontextprotocol/go-sdk), targeting the 2026-07-28 MCP
// specification. It adds go-mcp's simplified tool-registration surface: an
// untyped handler signature, and a required typed tool-annotation contract
// (rather than annotations left optional or inferred from a tool's name).
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/hollis-labs/go-mcp/budget"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ProtocolVersion is the MCP specification version this package targets.
const ProtocolVersion = "2026-07-28"

// ErrUnknownTool is returned by CallTool when no tool with the given name is
// registered.
var ErrUnknownTool = errors.New("unknown tool")

// ToolHandler implements a tool's behavior. args is the tool call's
// arguments, decoded from JSON into a map.
//
// The returned value becomes the tool result. A string is used verbatim as
// the result's text content -- for a tool whose natural output really is
// prose. Anything else is JSON-marshaled into CallToolResult.StructuredContent
// (per SEP-2106) *and* mirrored as JSON text content, so both a client
// reading typed structured output and one that only renders text content
// get the same data. Most tools should return a map, struct, or slice, not
// a pre-marshaled JSON string: marshaling it themselves (e.g. via
// budget.ToolJSON) produces a *string*, which this contract treats as
// opaque prose and never promotes to StructuredContent.
//
// A returned *budget.ToolError is reported as error content
// (CallToolResult.IsError) with its full structured shape -- code, field,
// retryable, next step, help tool -- preserved in StructuredContent, not
// collapsed to a bare message. A returned budget.StructuredError gets the
// same treatment via its own custom shape, for an error contract ToolError's
// fields don't fit. Any other error is reported as error content with just
// its Error() string.
type ToolHandler func(ctx context.Context, args map[string]any) (any, error)

// Tool describes a tool registration.
//
// The four hint fields are the MCP tool-annotation set. They are required:
// every registration states them explicitly, rather than leaving them
// optional (as the underlying spec does) or letting a caller infer them from
// the tool's name -- the latter was the exact shape of a permission-gating
// bug this contract exists to make structurally impossible.
type Tool struct {
	Name        string
	Description string
	InputSchema any
	Handler     ToolHandler

	// Title is an optional human-readable display name, distinct from Name
	// (which is for programmatic/logical use). Display-name precedence is
	// Title, then a client-side annotations title, then Name.
	Title string
	// OutputSchema optionally declares the JSON Schema that the handler's
	// StructuredContent result conforms to (see ToolHandler). Leave nil for
	// a tool whose output shape isn't worth committing to a schema yet --
	// StructuredContent is still populated either way. EmptyObjectSchema
	// and ObjectSchema work here exactly as they do for InputSchema.
	OutputSchema any

	// ReadOnlyHint reports whether the tool only reads, never modifying its
	// environment.
	ReadOnlyHint bool
	// DestructiveHint reports whether the tool may perform destructive
	// updates to its environment. Meaningful only when ReadOnlyHint is false.
	DestructiveHint bool
	// IdempotentHint reports whether calling the tool repeatedly with the
	// same arguments has no additional effect. Meaningful only when
	// ReadOnlyHint is false.
	IdempotentHint bool
	// OpenWorldHint reports whether the tool interacts with an open-ended
	// set of external entities (e.g. a web search) rather than a closed
	// domain (e.g. a memory store).
	OpenWorldHint bool
}

// ToolAnnotations is the typed MCP tool-annotation set, matching Tool's
// required hint fields.
type ToolAnnotations struct {
	ReadOnlyHint    bool `json:"readOnlyHint"`
	DestructiveHint bool `json:"destructiveHint"`
	IdempotentHint  bool `json:"idempotentHint"`
	OpenWorldHint   bool `json:"openWorldHint"`
}

// ToolDefinition is a tool's public shape, as returned by ToolDefinitions.
type ToolDefinition struct {
	Name         string          `json:"name"`
	Title        string          `json:"title,omitempty"`
	Description  string          `json:"description"`
	InputSchema  any             `json:"inputSchema"`
	OutputSchema any             `json:"outputSchema,omitempty"`
	Annotations  ToolAnnotations `json:"annotations"`
}

// Server wraps an official-SDK *mcpsdk.Server, adding go-mcp's
// tool-registration surface. The zero value is not usable; construct one
// with NewServer.
type Server struct {
	sdk     *mcpsdk.Server
	name    string
	version string

	mu       sync.RWMutex
	defs     map[string]ToolDefinition
	handlers map[string]ToolHandler
}

// Option configures the official SDK's mcpsdk.ServerOptions at construction
// time, via NewServer. Options exist because mcpsdk.ServerOptions can only be
// supplied when the underlying *mcpsdk.Server is built -- there is no way to
// retrofit them onto one returned by SDKServer afterward.
//
// go-mcp intentionally does not wrap prompts, resources, or session
// lifecycle: SDKServer returns the underlying *mcpsdk.Server, and callers
// drive AddPrompt, AddResource, AddResourceTemplate, and session iteration
// (Sessions, ServerSession.Wait as the unregister-equivalent) directly
// against it. Options exists only to unblock the handful of ServerOptions
// fields -- Instructions, InitializedHandler, CompletionHandler, and so on --
// that have no other construction point.
type Option func(*mcpsdk.ServerOptions)

// WithInstructions sets the free-text instructions advertised to connecting
// clients during initialize.
func WithInstructions(instructions string) Option {
	return func(o *mcpsdk.ServerOptions) { o.Instructions = instructions }
}

// WithInitializedHandler installs a callback invoked when a session sends
// "notifications/initialized". This is NOT a general "session registered"
// hook: a 2026-07-28 client opens with the stateless server/discover RPC
// (SEP-2575) and never sends this notification at all, so the handler never
// fires over that path. It fires only over the legacy initialize/initialized
// handshake, kept for interop with pre-2026-07-28 peers. Code that needs to
// react to every new session, regardless of handshake style, should call
// SDKServer().Connect directly -- it returns the *mcpsdk.ServerSession
// synchronously as the connection is established -- and use
// ServerSession.Wait for the unregister-equivalent.
func WithInitializedHandler(h func(context.Context, *mcpsdk.InitializedRequest)) Option {
	return func(o *mcpsdk.ServerOptions) { o.InitializedHandler = h }
}

// WithCompletionHandler installs the server's "completion/complete" handler,
// serving argument-completion suggestions for prompts and resource
// templates.
func WithCompletionHandler(h func(context.Context, *mcpsdk.CompleteRequest) (*mcpsdk.CompleteResult, error)) Option {
	return func(o *mcpsdk.ServerOptions) { o.CompletionHandler = h }
}

// WithKeepAlive sets a regular ping interval; a peer that stops responding
// has its session closed. Zero (the default) disables keepalive pings.
func WithKeepAlive(interval time.Duration) Option {
	return func(o *mcpsdk.ServerOptions) { o.KeepAlive = interval }
}

// WithKeepAliveFailureThreshold sets how many consecutive keepalive ping
// failures are tolerated before a session is closed. Has no effect unless
// WithKeepAlive is also set to a non-zero interval; the official SDK's
// default (0 or 1) closes a session on the first failure.
func WithKeepAliveFailureThreshold(n int) Option {
	return func(o *mcpsdk.ServerOptions) { o.KeepAliveFailureThreshold = n }
}

// WithCapabilities overrides the server's default advertised capabilities
// ({"logging":{}} for historical reasons) rather than letting them be
// inferred from registered tools/prompts/resources and other options. See
// mcpsdk.ServerOptions.Capabilities for the exact inference rules a
// non-nil field here overrides.
func WithCapabilities(caps *mcpsdk.ServerCapabilities) Option {
	return func(o *mcpsdk.ServerOptions) { o.Capabilities = caps }
}

// WithSupportedProtocolVersions restricts the MCP protocol versions this
// server advertises and accepts, narrowing (never widening) the versions
// the official SDK otherwise supports. Exact protocol-version negotiation
// is a named MCP contract area: a caller that must speak only a specific
// version range -- rather than accept whatever the SDK's own default
// supports -- has no other construction point for it.
func WithSupportedProtocolVersions(versions []string) Option {
	return func(o *mcpsdk.ServerOptions) { o.SupportedProtocolVersions = versions }
}

// WithLogger enables logging of server activity to the given logger.
// Diagnostics belong off the protocol channel (stderr for stdio, not
// stdout), which is exactly what a caller-supplied *slog.Logger is for.
func WithLogger(logger *slog.Logger) Option {
	return func(o *mcpsdk.ServerOptions) { o.Logger = logger }
}

// NewServer creates a Server advertising the given name and version. Options
// populate the underlying official-SDK ServerOptions; see Option.
func NewServer(name, version string, opts ...Option) *Server {
	var so mcpsdk.ServerOptions
	for _, opt := range opts {
		opt(&so)
	}
	return &Server{
		sdk:      mcpsdk.NewServer(&mcpsdk.Implementation{Name: name, Version: version}, &so),
		name:     name,
		version:  version,
		defs:     make(map[string]ToolDefinition),
		handlers: make(map[string]ToolHandler),
	}
}

// Info returns the server's advertised name and version.
func (s *Server) Info() (name, version string) {
	return s.name, s.version
}

// SDKServer returns the underlying official-SDK server, for callers (such as
// transport/http) that need to drive it directly.
func (s *Server) SDKServer() *mcpsdk.Server {
	return s.sdk
}

// RegisterTool registers a tool, or replaces a previous registration with
// the same name.
func (s *Server) RegisterTool(t Tool) {
	annotations := ToolAnnotations{
		ReadOnlyHint:    t.ReadOnlyHint,
		DestructiveHint: t.DestructiveHint,
		IdempotentHint:  t.IdempotentHint,
		OpenWorldHint:   t.OpenWorldHint,
	}

	s.mu.Lock()
	s.defs[t.Name] = ToolDefinition{
		Name:         t.Name,
		Title:        t.Title,
		Description:  t.Description,
		InputSchema:  t.InputSchema,
		OutputSchema: t.OutputSchema,
		Annotations:  annotations,
	}
	s.handlers[t.Name] = t.Handler
	s.mu.Unlock()

	destructive := t.DestructiveHint
	openWorld := t.OpenWorldHint
	sdkTool := &mcpsdk.Tool{
		Name:         t.Name,
		Title:        t.Title,
		Description:  t.Description,
		InputSchema:  t.InputSchema,
		OutputSchema: t.OutputSchema,
		Annotations: &mcpsdk.ToolAnnotations{
			ReadOnlyHint: t.ReadOnlyHint,
			// Always set explicitly (never left nil): the SDK defaults an
			// absent hint to true, which is exactly the silent-assumption
			// this contract exists to rule out.
			DestructiveHint: &destructive,
			IdempotentHint:  t.IdempotentHint,
			OpenWorldHint:   &openWorld,
		},
	}
	s.sdk.AddTool(sdkTool, adaptHandler(t.Name, t.Handler))
}

// ToolDefinitions returns all registered tools' public definitions, sorted
// deterministically by name.
func (s *Server) ToolDefinitions() []ToolDefinition {
	s.mu.RLock()
	defs := make([]ToolDefinition, 0, len(s.defs))
	for _, d := range s.defs {
		defs = append(defs, d)
	}
	s.mu.RUnlock()

	sort.Slice(defs, func(i, j int) bool { return defs[i].Name < defs[j].Name })
	return defs
}

// RemoveTools removes tools with the given names, symmetric with
// RegisterTool. It is not an error to remove a name that isn't registered.
// Removing a name that was never registered through RegisterTool (for
// example, one added directly via SDKServer().AddTool) only affects the
// wire-visible tool set; it leaves go-mcp's own bookkeeping untouched, since
// there is nothing there to remove.
func (s *Server) RemoveTools(names ...string) {
	s.mu.Lock()
	for _, name := range names {
		delete(s.defs, name)
		delete(s.handlers, name)
	}
	s.mu.Unlock()
	s.sdk.RemoveTools(names...)
}

// CallTool invokes a registered tool's handler directly, in-process,
// bypassing the MCP protocol layer, and returns its raw result value
// unconverted -- a string as a string, anything else as the Go value the
// handler returned, not JSON text. It is intended for direct programmatic
// use and tests. RPC callers are served through the wrapped SDK server, via
// Run or an HTTP transport, which additionally attach resultType, cache,
// annotation metadata, and the StructuredContent/text-content split that
// this direct path does not.
func (s *Server) CallTool(ctx context.Context, name string, args map[string]any) (any, error) {
	s.mu.RLock()
	h, ok := s.handlers[name]
	s.mu.RUnlock()
	if !ok {
		return nil, ErrUnknownTool
	}
	return h(ctx, args)
}

// Run serves the MCP protocol over stdio until ctx is done or the client
// disconnects.
func (s *Server) Run(ctx context.Context) error {
	return s.sdk.Run(ctx, &mcpsdk.StdioTransport{})
}

// adaptHandler wraps a go-mcp ToolHandler as an official-SDK raw ToolHandler:
// it decodes arguments, installs a Notifier bridged to the client session,
// and maps the (any, error) result onto a CallToolResult -- see ToolHandler
// for exactly how a string, a structured value, and a *budget.ToolError
// each land there. Cancellation (notifications/cancelled) and resultType
// (the MRTR complete/input_required pattern) are handled by the SDK's own
// dispatch and are not this function's concern.
func adaptHandler(name string, h ToolHandler) mcpsdk.ToolHandler {
	return func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		var args map[string]any
		if len(req.Params.Arguments) > 0 {
			if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
				// Malformed arguments are a protocol-level failure (the
				// request itself is invalid), not a tool-execution error, so
				// this reports a structured JSON-RPC error in go-mcp's
				// app-owned code range rather than embedding it in tool
				// result content.
				protoErr := budget.NewProtocolError(budget.ErrCodeInvalidInput,
					fmt.Sprintf("tool %q: invalid arguments: %v", name, err), nil)
				return nil, &jsonrpc.Error{Code: int64(protoErr.Code), Message: protoErr.Message}
			}
		}

		handlerCtx := WithNotifier(ctx, sessionNotifier(ctx, req.Session))
		handlerCtx = WithMeta(handlerCtx, req.Params.Meta)

		result, err := h(handlerCtx, args)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil {
			var toolErr *budget.ToolError
			if errors.As(err, &toolErr) {
				return marshaledResult(name, toolErr, true)
			}
			var structuredErr budget.StructuredError
			if errors.As(err, &structuredErr) {
				return marshaledResult(name, structuredErr.ToolErrorContent(), true)
			}
			return &mcpsdk.CallToolResult{
				Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: err.Error()}},
				IsError: true,
			}, nil
		}

		if result == nil {
			return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{}}, nil
		}
		if text, ok := result.(string); ok {
			return &mcpsdk.CallToolResult{
				Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: text}},
			}, nil
		}
		return marshaledResult(name, result, false)
	}
}

// marshaledResult JSON-marshals a non-string tool result (a success value or
// a *budget.ToolError) into both StructuredContent (the typed value, per
// SEP-2106) and a mirrored JSON-text Content block, so a client reading
// either gets the same data. A marshal failure here is go-mcp's own
// inability to serialize what the tool produced -- a protocol-level failure,
// not a tool-execution error -- so it is reported as a JSON-RPC error rather
// than folded into result content.
func marshaledResult(name string, v any, isError bool) (*mcpsdk.CallToolResult, error) {
	data, err := json.Marshal(v)
	if err != nil {
		protoErr := budget.NewProtocolError(budget.ErrCodeInternal,
			fmt.Sprintf("tool %q: marshal result: %v", name, err), nil)
		return nil, &jsonrpc.Error{Code: int64(protoErr.Code), Message: protoErr.Message}
	}
	return &mcpsdk.CallToolResult{
		Content:           []mcpsdk.Content{&mcpsdk.TextContent{Text: string(data)}},
		StructuredContent: v,
		IsError:           isError,
	}, nil
}

// sessionNotifier bridges go-mcp's context-installed Notifier to the
// client's MCP session, so handlers written against WithNotifier/Notify (see
// notify.go) work unchanged when served through the SDK.
func sessionNotifier(ctx context.Context, session *mcpsdk.ServerSession) func(Notification) {
	return func(n Notification) {
		params, _ := n.Params.(map[string]any)
		switch n.Method {
		case "notifications/progress":
			pp := &mcpsdk.ProgressNotificationParams{ProgressToken: params["progressToken"]}
			if v, ok := params["progress"].(float64); ok {
				pp.Progress = v
			}
			if v, ok := params["total"].(float64); ok {
				pp.Total = v
			}
			if v, ok := params["message"].(string); ok {
				pp.Message = v
			}
			_ = session.NotifyProgress(ctx, pp)
		case "notifications/message":
			level, _ := params["level"].(string)
			// Best-effort: logging/setLevel + notifications/message are
			// deprecated as of 2026-07-28 (SEP-2577) and the SDK silently
			// drops the notification until the client has set a level.
			_ = session.Log(ctx, &mcpsdk.LoggingMessageParams{
				Level: mcpsdk.LoggingLevel(level),
				Data:  params["message"],
			})
		}
	}
}
