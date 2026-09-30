package server

import (
	"context"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Multi-round-trip requests (SEP-2322, protocol 2026-07-28). A tool that
// needs something from the client before it can finish -- an elicitation
// (form or URL), a sampling call, the roots -- does not call the client in
// the middle of the request, which the 2026-07-28 protocol refuses. It
// returns InputRequired; the client fulfills the requests and calls the tool
// again with the responses and the same state, which the handler reads with
// InputResponses and RequestState.
//
// The SDK bridges older clients: for a session on an earlier protocol it
// sends the requests to the client itself and calls the handler again with
// the responses, so one handler serves both.
//
// RequestState travels through the client. A server that is not
// authenticated must treat what comes back as untrusted: encrypt or sign it,
// and verify it before acting on it.

// InputRequired is a tool result that asks the client for input before the
// call can complete. Requests maps IDs of the server's choosing to input
// requests (*mcpsdk.ElicitParams, *mcpsdk.CreateMessageParams or
// *mcpsdk.ListRootsParams); State is echoed back on the retry. An
// InputRequired with no Requests tells the client to retry later
// (load-shedding).
type InputRequired struct {
	Requests mcpsdk.InputRequestMap
	State    string
}

type inputKey struct{}

type input struct {
	responses mcpsdk.InputResponseMap
	state     string
}

// withInput carries a retry's responses and state to its handler.
func withInput(ctx context.Context, responses mcpsdk.InputResponseMap, state string) context.Context {
	if len(responses) == 0 && state == "" {
		return ctx
	}
	return context.WithValue(ctx, inputKey{}, input{responses: responses, state: state})
}

// InputResponses are the client's responses to the InputRequired this call
// is a retry of, keyed by the request IDs; nil on a first call.
func InputResponses(ctx context.Context) mcpsdk.InputResponseMap {
	in, _ := ctx.Value(inputKey{}).(input)
	return in.responses
}

// RequestState is the state the client echoed back from the InputRequired
// this call is a retry of; empty on a first call. It came through the
// client: verify it before trusting it.
func RequestState(ctx context.Context) string {
	in, _ := ctx.Value(inputKey{}).(input)
	return in.state
}

// inputRequiredResult is the result an InputRequired becomes: the requests
// and state, and no content (the SDK refuses both at once).
func inputRequiredResult(result any) (*mcpsdk.CallToolResult, bool) {
	switch ir := result.(type) {
	case InputRequired:
		return &mcpsdk.CallToolResult{InputRequests: orEmpty(ir.Requests), RequestState: ir.State}, true
	case *InputRequired:
		if ir == nil {
			return nil, false
		}
		return &mcpsdk.CallToolResult{InputRequests: orEmpty(ir.Requests), RequestState: ir.State}, true
	}
	return nil, false
}

// orEmpty keeps an empty request map non-nil, so the result still reads as
// input-required (load-shedding) rather than complete.
func orEmpty(m mcpsdk.InputRequestMap) mcpsdk.InputRequestMap {
	if m == nil {
		return mcpsdk.InputRequestMap{}
	}
	return m
}

type capabilitiesKey struct{}

func withClientCapabilities(ctx context.Context, caps *mcpsdk.ClientCapabilities) context.Context {
	if caps == nil {
		return ctx
	}
	return context.WithValue(ctx, capabilitiesKey{}, caps)
}

// ClientCapabilities are the calling client's capabilities, from the
// request's _meta on protocol 2026-07-28 and from the session's initialize
// before it; nil when the client declared none. A handler reads it to know
// whether an input request will be understood, for example
// ClientCapabilities(ctx).Elicitation.URL before asking for a URL
// elicitation.
func ClientCapabilities(ctx context.Context) *mcpsdk.ClientCapabilities {
	caps, _ := ctx.Value(capabilitiesKey{}).(*mcpsdk.ClientCapabilities)
	return caps
}
