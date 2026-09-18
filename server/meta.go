package server

import "context"

type metaKey struct{}

// WithMeta installs a tool call's protocol-level "_meta" object into ctx,
// for MetaFromContext to read inside the handler. adaptHandler installs
// this automatically from the incoming request; exported so a caller
// driving a handler directly (e.g. in a test) can supply one too.
func WithMeta(ctx context.Context, meta map[string]any) context.Context {
	if len(meta) == 0 {
		return ctx
	}
	return context.WithValue(ctx, metaKey{}, meta)
}

// MetaFromContext returns the tool call's protocol-level "_meta" object, as
// sent by the caller -- for example, an idempotency key a client attaches
// via _meta rather than as a tool argument, a convention this portfolio
// already uses (see Hadron's "hadron/idempotencyKey"). Returns nil if the
// call carried no _meta, or for a direct in-process call via
// Server.CallTool, which bypasses the protocol layer entirely and so has no
// _meta to carry.
func MetaFromContext(ctx context.Context) map[string]any {
	meta, _ := ctx.Value(metaKey{}).(map[string]any)
	return meta
}
