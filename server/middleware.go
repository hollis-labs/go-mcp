package server

import (
	"log/slog"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// sanitizeDefault is whether NewServer installs the sanitize middleware when
// the caller says nothing. It is false: sanitization rewrites tool-call
// arguments, so it is opt-in (WithSanitize). This is the single place that
// default lives.
const sanitizeDefault = false

// options is what an Option configures: the SDK's ServerOptions plus the
// kit's own settings.
type options struct {
	sdk mcpsdk.ServerOptions

	toolMW           []ToolMiddleware
	receiving        []mcpsdk.Middleware
	sanitize         bool
	sanitizeLogger   *slog.Logger
	dup              DuplicatePolicy
	behaviorRequired bool
}

// ToolMiddleware wraps a tool's handler at registration time. def is the
// tool's public definition (schema and annotations included), so a
// middleware can derive per-tool state once, when it wraps, rather than per
// call. The first middleware registered is the outermost.
type ToolMiddleware func(def ToolDefinition, next ToolHandler) ToolHandler

// WithToolMiddleware appends tool middleware applied to every tool
// registered afterwards through RegisterTool / RegisterChecked. Unlike
// receiving middleware it also runs on Server.CallTool. It sees arguments
// after the SDK decoded them and after any receiving middleware ran.
func WithToolMiddleware(mw ...ToolMiddleware) Option {
	return func(o *options) { o.toolMW = append(o.toolMW, mw...) }
}

// WithReceivingMiddleware installs SDK receiving middleware on the
// underlying server, in order (the first runs outermost), after WithSanitize's
// when both are set.
func WithReceivingMiddleware(mw ...mcpsdk.Middleware) Option {
	return func(o *options) { o.receiving = append(o.receiving, mw...) }
}

// WithSanitize explicitly opts in to the sanitize middleware, which cleans
// leaked markup out of tools/call arguments before they reach a handler.
// It mutates data, so NewServer never installs it on its own.
//
// logger receives the warn-level "cleaned tool call" line. nil means the
// logger given to WithLogger, else a text logger on stderr; never stdout,
// which is the JSON-RPC channel under stdio.
func WithSanitize(logger *slog.Logger) Option {
	return func(o *options) {
		o.sanitize = true
		o.sanitizeLogger = logger
	}
}

// DuplicatePolicy says what RegisterTool does when a tool with the same name
// is already registered. Removing a tool with RemoveTools first makes the
// name free again under every policy. Only registrations made through this
// Server are seen; tools added directly to SDKServer() are not.
type DuplicatePolicy uint8

const (
	// DuplicateReplace replaces the earlier registration (the default, and
	// the behavior before this option existed).
	DuplicateReplace DuplicatePolicy = iota
	// DuplicatePanic panics on a duplicate.
	DuplicatePanic
	// DuplicateRecord keeps the first registration, ignores the duplicate,
	// and records an error retrievable with RegistrationErrors.
	DuplicateRecord
)

// WithDuplicateTools sets the duplicate-registration policy.
func WithDuplicateTools(p DuplicatePolicy) Option {
	return func(o *options) { o.dup = p }
}

// WithBehaviorRequired makes plain RegisterTool panic, so every tool must be
// declared with RegisterChecked and an explicit Behavior.
func WithBehaviorRequired() Option {
	return func(o *options) { o.behaviorRequired = true }
}
