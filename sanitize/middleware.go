package sanitize

import (
	"context"
	"encoding/json"
	"log/slog"
	"sort"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Middleware returns an [mcp.Middleware] that runs [Sanitize] on every
// tools/call request's arguments before it reaches the tool handler, with
// warn-level slog telemetry on any clean. Install once at server setup:
//
//	srv.AddReceivingMiddleware(sanitize.Middleware(logger))
//
// Behavior:
//   - Only "tools/call" requests are inspected; every other method passes
//     straight through.
//   - If Sanitize reports no changes, the request is forwarded untouched
//     and no log line is emitted.
//   - If Sanitize reports changes, exactly one warn-level log line is
//     emitted before the handler runs, and the request's raw Arguments are
//     replaced with the cleaned, re-marshaled JSON.
//   - If logger is nil, the middleware uses slog.Default().
func Middleware(logger *slog.Logger) mcp.Middleware {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			call, ok := req.(*mcp.CallToolRequest)
			if !ok || call.Params == nil || len(call.Params.Arguments) == 0 {
				return next(ctx, method, req)
			}

			var args map[string]any
			if err := json.Unmarshal(call.Params.Arguments, &args); err != nil {
				// Not a JSON object we can sanitize — leave it for the
				// handler to reject on its own terms.
				return next(ctx, method, req)
			}
			if len(args) == 0 {
				return next(ctx, method, req)
			}

			cleaned, report := Sanitize(args)
			if !report.Changed() {
				return next(ctx, method, req)
			}

			encoded, err := json.Marshal(cleaned)
			if err != nil {
				// Fall back to the original, uncleaned arguments rather than
				// dropping the request.
				return next(ctx, method, req)
			}

			logger.Warn("mcp-sanitize: cleaned tool call",
				"tool", call.Params.Name,
				"fields_cleaned", report.FieldsCleaned,
				"recovered_fields", sortedKeys(report.RecoveredFields),
				"dropped_count", len(report.DroppedFragments),
			)
			call.Params.Arguments = encoded
			return next(ctx, method, req)
		}
	}
}

// sortedKeys returns the map keys in sorted order. Stable output keeps log
// lines deterministic across runs.
func sortedKeys(m map[string]string) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
