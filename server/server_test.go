package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/hollis-labs/go-mcp/budget"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// connect wires srv to a fresh client over an in-memory transport pair and
// returns the connected client session, closing both ends on test cleanup.
func connect(t *testing.T, srv *Server, opts *mcpsdk.ClientOptions) *mcpsdk.ClientSession {
	t.Helper()
	ctx := context.Background()

	serverTransport, clientTransport := mcpsdk.NewInMemoryTransports()

	ss, err := srv.SDKServer().Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { _ = ss.Close() })

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test-client", Version: "0.0.0"}, opts)
	cs, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })

	return cs
}

func TestToolsListIsSortedByName(t *testing.T) {
	srv := NewServer("cerberus", "test")
	srv.RegisterTool(Tool{Name: "zeta_tool", Description: "z", InputSchema: EmptyObjectSchema(), ReadOnlyHint: true})
	srv.RegisterTool(Tool{Name: "alpha_tool", Description: "a", InputSchema: EmptyObjectSchema(), ReadOnlyHint: true})
	srv.RegisterTool(Tool{Name: "mid_tool", Description: "m", InputSchema: EmptyObjectSchema(), ReadOnlyHint: true})

	cs := connect(t, srv, nil)

	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(res.Tools) != 3 {
		t.Fatalf("tool count = %d, want 3", len(res.Tools))
	}
	if res.Tools[0].Name != "alpha_tool" || res.Tools[1].Name != "mid_tool" || res.Tools[2].Name != "zeta_tool" {
		names := []string{res.Tools[0].Name, res.Tools[1].Name, res.Tools[2].Name}
		t.Fatalf("unexpected tool order: %v", names)
	}

	// ToolDefinitions (the in-process accessor) is sorted the same way.
	defs := srv.ToolDefinitions()
	if len(defs) != 3 || defs[0].Name != "alpha_tool" || defs[1].Name != "mid_tool" || defs[2].Name != "zeta_tool" {
		t.Fatalf("ToolDefinitions unsorted: %#v", defs)
	}
}

func TestToolAnnotationsAlwaysDeclared(t *testing.T) {
	srv := NewServer("cerberus", "test")
	srv.RegisterTool(Tool{
		Name:            "delete_thing",
		Description:     "delete",
		InputSchema:     EmptyObjectSchema(),
		ReadOnlyHint:    false,
		DestructiveHint: true,
		IdempotentHint:  true,
		OpenWorldHint:   false,
	})

	cs := connect(t, srv, nil)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(res.Tools) != 1 {
		t.Fatalf("tool count = %d, want 1", len(res.Tools))
	}
	ann := res.Tools[0].Annotations
	if ann == nil {
		t.Fatal("annotations not declared on wire (nil)")
	}
	if ann.ReadOnlyHint != false || ann.IdempotentHint != true {
		t.Fatalf("unexpected plain-bool annotations: %+v", ann)
	}
	if ann.DestructiveHint == nil || *ann.DestructiveHint != true {
		t.Fatalf("destructiveHint not explicitly declared true: %+v", ann.DestructiveHint)
	}
	if ann.OpenWorldHint == nil || *ann.OpenWorldHint != false {
		t.Fatalf("openWorldHint not explicitly declared false: %+v", ann.OpenWorldHint)
	}

	// The in-process definition carries the same required, typed fields.
	defs := srv.ToolDefinitions()
	if defs[0].Annotations != (ToolAnnotations{DestructiveHint: true, IdempotentHint: true}) {
		t.Fatalf("unexpected ToolDefinition.Annotations: %+v", defs[0].Annotations)
	}
}

func TestToolCallRoundTrip(t *testing.T) {
	srv := NewServer("cerberus", "test")
	srv.RegisterTool(Tool{
		Name:         "echo",
		Description:  "echo",
		InputSchema:  ObjectSchema(map[string]any{"text": map[string]any{"type": "string"}}, "text"),
		ReadOnlyHint: true,
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			return args["text"].(string), nil
		},
	})

	cs := connect(t, srv, nil)
	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "echo",
		Arguments: map[string]any{"text": "hello"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error result: %+v", res.Content)
	}
	text, ok := res.Content[0].(*mcpsdk.TextContent)
	if !ok || text.Text != "hello" {
		t.Fatalf("unexpected content: %#v", res.Content)
	}
}

func TestToolCallHandlerErrorReportedAsContent(t *testing.T) {
	srv := NewServer("cerberus", "test")
	srv.RegisterTool(Tool{
		Name:         "fail",
		Description:  "always fails",
		InputSchema:  EmptyObjectSchema(),
		ReadOnlyHint: true,
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			return "", errors.New("boom")
		},
	})

	cs := connect(t, srv, nil)
	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: "fail"})
	if err != nil {
		t.Fatalf("CallTool transport error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError = true, got result: %+v", res)
	}
	text, ok := res.Content[0].(*mcpsdk.TextContent)
	if !ok || text.Text != "boom" {
		t.Fatalf("unexpected error content: %#v", res.Content)
	}
}

func TestToolCallCancellationPropagatesToHandler(t *testing.T) {
	srv := NewServer("cerberus", "test")
	started := make(chan struct{})
	canceled := make(chan struct{})

	srv.RegisterTool(Tool{
		Name:         "block_tool",
		Description:  "block",
		InputSchema:  EmptyObjectSchema(),
		ReadOnlyHint: true,
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			close(started)
			<-ctx.Done()
			close(canceled)
			return "", ctx.Err()
		},
	})

	cs := connect(t, srv, nil)

	callCtx, cancel := context.WithCancel(context.Background())
	callErr := make(chan error, 1)
	go func() {
		_, err := cs.CallTool(callCtx, &mcpsdk.CallToolParams{Name: "block_tool"})
		callErr <- err
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("handler never started")
	}

	cancel()

	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("handler context was never cancelled")
	}

	select {
	case err := <-callErr:
		if err == nil {
			t.Fatal("expected CallTool to report an error after client-side cancellation")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("CallTool never returned after cancellation")
	}
}

func TestToolCallMalformedArgumentsReportsProtocolError(t *testing.T) {
	srv := NewServer("cerberus", "test")
	srv.RegisterTool(Tool{
		Name:         "echo",
		Description:  "echo",
		InputSchema:  EmptyObjectSchema(),
		ReadOnlyHint: true,
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			return "unreachable", nil
		},
	})

	cs := connect(t, srv, nil)
	// Arguments must be a JSON object; a string fails to decode into
	// map[string]any, exercising the malformed-arguments path.
	_, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "echo",
		Arguments: "not an object",
	})
	if err == nil {
		t.Fatal("expected a protocol-level error, got none")
	}
	var wireErr *jsonrpc.Error
	if !errors.As(err, &wireErr) {
		t.Fatalf("err is not *jsonrpc.Error: %v (%T)", err, err)
	}
	if wireErr.Code != int64(budget.ErrCodeInvalidInput) {
		t.Fatalf("code = %d, want %d (budget.ErrCodeInvalidInput)", wireErr.Code, budget.ErrCodeInvalidInput)
	}
}

func TestCallToolDirectBypassesProtocol(t *testing.T) {
	srv := NewServer("cerberus", "test")
	srv.RegisterTool(Tool{
		Name:         "echo",
		Description:  "echo",
		InputSchema:  EmptyObjectSchema(),
		ReadOnlyHint: true,
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			return "direct", nil
		},
	})

	text, err := srv.CallTool(context.Background(), "echo", nil)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if text != "direct" {
		t.Fatalf("text = %q, want direct", text)
	}
}

func TestCallToolUnknownTool(t *testing.T) {
	srv := NewServer("cerberus", "test")
	if _, err := srv.CallTool(context.Background(), "nope", nil); !errors.Is(err, ErrUnknownTool) {
		t.Fatalf("err = %v, want ErrUnknownTool", err)
	}
}

// TestNotificationsForwardedToClientSession verifies that a handler using
// go-mcp's context-installed Notifier (NotifyProgress/NotifyMessage, see
// notify.go) reaches the real client session when served through the SDK,
// not just when driven directly against a fake Notifier (see
// TestNotifyHelpers).
//
// It asserts end-to-end delivery only for notifications/progress.
// notifications/message (MCP's logging feature) is deprecated as of
// 2026-07-28 (SEP-2577); in the official SDK v1.7.0, session.Log silently
// drops the notification even after logging/setLevel, independent of
// go-mcp's bridge -- confirmed by an isolated repro against the SDK alone.
// The call is still exercised here so a regression in the bridge itself
// (e.g. a panic, or session.Log erroring) is still caught.
func TestNotificationsForwardedToClientSession(t *testing.T) {
	var gotProgress []*mcpsdk.ProgressNotificationParams
	progressCh := make(chan struct{}, 1)

	srv := NewServer("cerberus", "test")
	srv.RegisterTool(Tool{
		Name:         "notify",
		Description:  "notify",
		InputSchema:  EmptyObjectSchema(),
		ReadOnlyHint: true,
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			if !NotifyMessage(ctx, "info", "starting") {
				t.Error("NotifyMessage: no notifier installed on handler ctx")
			}
			if !NotifyProgress(ctx, "tok-1", 1, 2, "halfway") {
				t.Error("NotifyProgress: no notifier installed on handler ctx")
			}
			return "done", nil
		},
	})

	cs := connect(t, srv, &mcpsdk.ClientOptions{
		ProgressNotificationHandler: func(ctx context.Context, req *mcpsdk.ProgressNotificationClientRequest) {
			gotProgress = append(gotProgress, req.Params)
			progressCh <- struct{}{}
		},
	})

	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: "notify"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %+v", res.Content)
	}

	select {
	case <-progressCh:
	case <-time.After(2 * time.Second):
		t.Fatal("progress notification never arrived")
	}

	if len(gotProgress) != 1 || gotProgress[0].ProgressToken != "tok-1" || gotProgress[0].Progress != 1 || gotProgress[0].Total != 2 {
		t.Fatalf("unexpected progress notifications: %+v", gotProgress)
	}
}

// TestNewServerOptionsPassThrough exercises Option's only reason to exist:
// mcpsdk.ServerOptions can be set solely at mcpsdk.NewServer time, so
// Instructions and CompletionHandler must flow through go-mcp's NewServer
// rather than being set on the *mcpsdk.Server SDKServer returns.
//
// Instructions and CompletionHandler are exercised over the default
// (2026-07-28, stateless server/discover) path. WithInitializedHandler is
// exercised separately, over a forced legacy handshake -- see
// TestNewServerOptionsInitializedHandlerLegacyHandshakeOnly for why.
func TestNewServerOptionsPassThrough(t *testing.T) {
	srv := NewServer("cerberus", "test",
		WithInstructions("call hadron_skills first"),
		WithCompletionHandler(func(_ context.Context, req *mcpsdk.CompleteRequest) (*mcpsdk.CompleteResult, error) {
			return &mcpsdk.CompleteResult{Completion: mcpsdk.CompletionResultDetails{Values: []string{"suggested"}}}, nil
		}),
	)
	srv.RegisterTool(Tool{Name: "noop", Description: "noop", InputSchema: EmptyObjectSchema(), ReadOnlyHint: true,
		Handler: func(context.Context, map[string]any) (any, error) { return "", nil },
	})
	srv.SDKServer().AddPrompt(&mcpsdk.Prompt{Name: "greeting"}, func(context.Context, *mcpsdk.GetPromptRequest) (*mcpsdk.GetPromptResult, error) {
		return &mcpsdk.GetPromptResult{Messages: []*mcpsdk.PromptMessage{}}, nil
	})

	cs := connect(t, srv, nil)

	if got := cs.InitializeResult().Instructions; got != "call hadron_skills first" {
		t.Fatalf("Instructions = %q, want %q", got, "call hadron_skills first")
	}

	res, err := cs.Complete(context.Background(), &mcpsdk.CompleteParams{
		Ref:      &mcpsdk.CompleteReference{Type: "ref/prompt", Name: "greeting"},
		Argument: mcpsdk.CompleteParamsArgument{Name: "name"},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(res.Completion.Values) != 1 || res.Completion.Values[0] != "suggested" {
		t.Fatalf("unexpected completion result: %+v", res.Completion)
	}
}

// TestNewServerOptionsInitializedHandlerLegacyHandshakeOnly documents and
// verifies a real protocol-path gap discovered while writing this test: a
// client that speaks 2026-07-28 opens with the stateless server/discover RPC
// (SEP-2575) and never sends "notifications/initialized" at all, so
// InitializedHandler never fires on that path. It only fires over the legacy
// initialize/initialized handshake, forced here via ProtocolVersion. It is
// NOT a usable "session registered" hook for a 2026-07-28-first server --
// callers needing one should use SDKServer().Connect directly (which returns
// the *mcpsdk.ServerSession synchronously) and ServerSession.Wait for the
// unregister-equivalent, instead of relying on this handler.
func TestNewServerOptionsInitializedHandlerLegacyHandshakeOnly(t *testing.T) {
	initialized := make(chan struct{}, 1)
	srv := NewServer("cerberus", "test",
		WithInitializedHandler(func(context.Context, *mcpsdk.InitializedRequest) {
			initialized <- struct{}{}
		}),
	)

	ctx := context.Background()
	serverTransport, clientTransport := mcpsdk.NewInMemoryTransports()
	ss, err := srv.SDKServer().Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { _ = ss.Close() })

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test-client", Version: "0.0.0"}, nil)
	cs, err := client.Connect(ctx, clientTransport, &mcpsdk.ClientSessionOptions{ProtocolVersion: "2025-11-25"})
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })

	select {
	case <-initialized:
	case <-time.After(2 * time.Second):
		t.Fatal("InitializedHandler never fired over the legacy handshake")
	}
}

// TestNewServerOptionsCapabilitiesProtocolVersionsAndLogger covers the
// second, narrower round of Option additions: Capabilities and
// SupportedProtocolVersions are exact protocol/capability-negotiation
// contract areas, and Logger is the observability hook -- all three are
// otherwise unreachable once SDKServer() has already built the underlying
// *mcpsdk.Server.
func TestNewServerOptionsCapabilitiesProtocolVersionsAndLogger(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))

	srv := NewServer("cerberus", "test",
		WithCapabilities(&mcpsdk.ServerCapabilities{}), // overrides the default {"logging":{}}
		WithSupportedProtocolVersions([]string{"2025-11-25"}),
		WithLogger(logger),
	)
	srv.RegisterTool(Tool{Name: "noop", Description: "noop", InputSchema: EmptyObjectSchema(), ReadOnlyHint: true,
		Handler: func(context.Context, map[string]any) (any, error) { return "", nil },
	})

	cs := connect(t, srv, nil)

	ir := cs.InitializeResult()
	if ir.Capabilities == nil || ir.Capabilities.Logging != nil {
		t.Fatalf("Capabilities override not applied, or logging still advertised: %+v", ir.Capabilities)
	}
	if ir.ProtocolVersion != "2025-11-25" {
		t.Fatalf("ProtocolVersion = %q, want the sole SupportedProtocolVersions entry %q", ir.ProtocolVersion, "2025-11-25")
	}

	if err := cs.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if logs.Len() == 0 {
		t.Fatal("WithLogger: no log output observed after a session connected and closed")
	}
}

// TestToolCallStructuredOutput is the headline case ToolHandler's (any,
// error) signature exists for: a handler returning a Go value (not a
// pre-marshaled string) must produce BOTH a typed StructuredContent (per
// SEP-2106) and a mirrored JSON-text Content block, so a client reading
// either gets the same data.
func TestToolCallStructuredOutput(t *testing.T) {
	type workspace struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}

	srv := NewServer("cerberus", "test")
	srv.RegisterTool(Tool{
		Name:         "workspace_get",
		Description:  "get a workspace",
		InputSchema:  EmptyObjectSchema(),
		ReadOnlyHint: true,
		Handler: func(context.Context, map[string]any) (any, error) {
			return workspace{ID: "ws-1", Name: "default"}, nil
		},
	})

	cs := connect(t, srv, nil)
	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: "workspace_get"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %+v", res.Content)
	}

	sc, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("StructuredContent = %#v (%T), want a decoded object", res.StructuredContent, res.StructuredContent)
	}
	if sc["id"] != "ws-1" || sc["name"] != "default" {
		t.Fatalf("unexpected StructuredContent: %+v", sc)
	}

	text, ok := res.Content[0].(*mcpsdk.TextContent)
	if !ok {
		t.Fatalf("Content[0] is not text: %#v", res.Content[0])
	}
	var fromText workspace
	if err := json.Unmarshal([]byte(text.Text), &fromText); err != nil {
		t.Fatalf("Content text is not valid JSON mirroring StructuredContent: %v (%s)", err, text.Text)
	}
	if fromText != (workspace{ID: "ws-1", Name: "default"}) {
		t.Fatalf("Content text disagrees with StructuredContent: %+v", fromText)
	}
}

// TestToolCallStringResultStaysPlainText confirms the ToolHandler contract's
// other half: a handler that returns a plain string (prose, not data) is
// used verbatim as text content and never promoted to StructuredContent --
// there is nothing meaningfully "structured" about an opaque string.
func TestToolCallStringResultStaysPlainText(t *testing.T) {
	srv := NewServer("cerberus", "test")
	srv.RegisterTool(Tool{
		Name:         "greet",
		Description:  "greet",
		InputSchema:  EmptyObjectSchema(),
		ReadOnlyHint: true,
		Handler: func(context.Context, map[string]any) (any, error) {
			return "hello, agent", nil
		},
	})

	cs := connect(t, srv, nil)
	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: "greet"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.StructuredContent != nil {
		t.Fatalf("StructuredContent = %#v, want nil for a plain string result", res.StructuredContent)
	}
	text, ok := res.Content[0].(*mcpsdk.TextContent)
	if !ok || text.Text != "hello, agent" {
		t.Fatalf("unexpected content: %#v", res.Content)
	}
}

// TestToolCallToolErrorPreservesStructuredShape confirms a *budget.ToolError
// returned as a handler's error is not collapsed to a bare message: the
// calling agent gets the full typed shape (code, field, retryable, next
// step, help tool) in StructuredContent, matching a successful structured
// result's treatment.
func TestToolCallToolErrorPreservesStructuredShape(t *testing.T) {
	srv := NewServer("cerberus", "test")
	srv.RegisterTool(Tool{
		Name:         "run_get",
		Description:  "get a run",
		InputSchema:  EmptyObjectSchema(),
		ReadOnlyHint: true,
		Handler: func(context.Context, map[string]any) (any, error) {
			return nil, budget.NewToolError("not_found", "run RUN-404 not found").
				WithField("run_id").
				WithNextStep("call hadron_runs_list to find a valid run_id").
				WithHelpTool("hadron_runs_list")
		},
	})

	cs := connect(t, srv, nil)
	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: "run_get"})
	if err != nil {
		t.Fatalf("CallTool transport error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError = true, got result: %+v", res)
	}

	sc, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("StructuredContent = %#v (%T), want the decoded ToolError shape", res.StructuredContent, res.StructuredContent)
	}
	if sc["code"] != "not_found" || sc["field"] != "run_id" || sc["nextStep"] == "" || sc["helpTool"] != "hadron_runs_list" {
		t.Fatalf("ToolError fields lost in StructuredContent: %+v", sc)
	}
	if sc["retryable"] != false {
		t.Fatalf(`sc["retryable"] = %v, want false (always present, not omitted)`, sc["retryable"])
	}

	text, ok := res.Content[0].(*mcpsdk.TextContent)
	if !ok {
		t.Fatalf("Content[0] is not text: %#v", res.Content[0])
	}
	var fromText budget.ToolError
	if err := json.Unmarshal([]byte(text.Text), &fromText); err != nil {
		t.Fatalf("Content text is not valid JSON: %v (%s)", err, text.Text)
	}
	if fromText.Code != "not_found" || fromText.NextStep == "" {
		t.Fatalf("Content text disagrees with StructuredContent: %+v", fromText)
	}
}

// TestToolCallPlainErrorStaysPlainText confirms an ordinary error (not a
// *budget.ToolError) keeps today's behavior exactly: just its Error()
// string in text content, no StructuredContent -- ToolError is opt-in, not
// a requirement placed on every handler.
func TestToolCallPlainErrorStaysPlainText(t *testing.T) {
	srv := NewServer("cerberus", "test")
	srv.RegisterTool(Tool{
		Name:         "fail_plain",
		Description:  "always fails, plainly",
		InputSchema:  EmptyObjectSchema(),
		ReadOnlyHint: true,
		Handler: func(context.Context, map[string]any) (any, error) {
			return nil, errors.New("boom")
		},
	})

	cs := connect(t, srv, nil)
	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: "fail_plain"})
	if err != nil {
		t.Fatalf("CallTool transport error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError = true")
	}
	if res.StructuredContent != nil {
		t.Fatalf("StructuredContent = %#v, want nil for a plain error", res.StructuredContent)
	}
	text, ok := res.Content[0].(*mcpsdk.TextContent)
	if !ok || text.Text != "boom" {
		t.Fatalf("unexpected error content: %#v", res.Content)
	}
}

// TestRegisterToolTitleAndOutputSchemaPassThrough confirms Tool.Title and
// Tool.OutputSchema -- otherwise-dropped official-SDK Tool fields -- reach
// both the wire (via ListTools) and the in-process ToolDefinition.
func TestRegisterToolTitleAndOutputSchemaPassThrough(t *testing.T) {
	outputSchema := ObjectSchema(map[string]any{"id": map[string]any{"type": "string"}}, "id")

	srv := NewServer("cerberus", "test")
	srv.RegisterTool(Tool{
		Name:         "workspace_get",
		Title:        "Get Workspace",
		Description:  "get a workspace",
		InputSchema:  EmptyObjectSchema(),
		OutputSchema: outputSchema,
		ReadOnlyHint: true,
		Handler: func(context.Context, map[string]any) (any, error) {
			return map[string]any{"id": "ws-1"}, nil
		},
	})

	cs := connect(t, srv, nil)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(res.Tools) != 1 {
		t.Fatalf("tool count = %d, want 1", len(res.Tools))
	}
	if res.Tools[0].Title != "Get Workspace" {
		t.Fatalf("Title = %q, want %q", res.Tools[0].Title, "Get Workspace")
	}
	if res.Tools[0].OutputSchema == nil {
		t.Fatal("OutputSchema not declared on wire")
	}

	defs := srv.ToolDefinitions()
	if defs[0].Title != "Get Workspace" {
		t.Fatalf("ToolDefinition.Title = %q, want %q", defs[0].Title, "Get Workspace")
	}
	if defs[0].OutputSchema == nil {
		t.Fatal("ToolDefinition.OutputSchema not set")
	}
}

// customStructuredError implements budget.StructuredError with a shape that
// deliberately doesn't fit budget.ToolError's fields -- no human-readable
// message field at all, mirroring a real consumer (Hadron's workflow error
// envelope) that omits one for data-minimization reasons.
type customStructuredError struct {
	Code string
}

func (e customStructuredError) Error() string         { return "custom: " + e.Code }
func (e customStructuredError) ToolErrorContent() any { return map[string]string{"error_code": e.Code} }

func TestToolCallStructuredErrorCustomShape(t *testing.T) {
	srv := NewServer("cerberus", "test")
	srv.RegisterTool(Tool{Name: "fail_custom", Description: "fail", InputSchema: EmptyObjectSchema(), ReadOnlyHint: true,
		Handler: func(context.Context, map[string]any) (any, error) {
			return nil, customStructuredError{Code: "policy_denied"}
		},
	})

	cs := connect(t, srv, nil)
	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: "fail_custom"})
	if err != nil {
		t.Fatalf("CallTool transport error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError = true")
	}
	sc, ok := res.StructuredContent.(map[string]any)
	if !ok || sc["error_code"] != "policy_denied" {
		t.Fatalf("StructuredContent = %#v, want the custom shape", res.StructuredContent)
	}
}

// TestRemoveToolsIsSymmetricWithRegisterTool covers RemoveTools: a removed
// tool disappears from both the wire (ListTools) and go-mcp's own
// bookkeeping (ToolDefinitions, CallTool), not just one or the other.
func TestRemoveToolsIsSymmetricWithRegisterTool(t *testing.T) {
	srv := NewServer("cerberus", "test")
	srv.RegisterTool(Tool{Name: "keep", Description: "keep", InputSchema: EmptyObjectSchema(), ReadOnlyHint: true,
		Handler: func(context.Context, map[string]any) (any, error) { return "kept", nil },
	})
	srv.RegisterTool(Tool{Name: "drop", Description: "drop", InputSchema: EmptyObjectSchema(), ReadOnlyHint: true,
		Handler: func(context.Context, map[string]any) (any, error) { return "dropped", nil },
	})

	srv.RemoveTools("drop", "never-registered")

	if defs := srv.ToolDefinitions(); len(defs) != 1 || defs[0].Name != "keep" {
		t.Fatalf("ToolDefinitions after RemoveTools = %#v, want only \"keep\"", defs)
	}
	if _, err := srv.CallTool(context.Background(), "drop", nil); !errors.Is(err, ErrUnknownTool) {
		t.Fatalf("CallTool(\"drop\") error = %v, want ErrUnknownTool", err)
	}

	cs := connect(t, srv, nil)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(res.Tools) != 1 || res.Tools[0].Name != "keep" {
		t.Fatalf("wire ListTools after RemoveTools = %#v, want only \"keep\"", res.Tools)
	}
}

// TestMetaFromContextReadsCallToolMeta covers the seam a caller-attached
// "_meta" idempotency key (or similar out-of-band field) needs: a
// ToolHandler can read what the client actually sent, not just its
// arguments, and a direct in-process CallTool (which carries no protocol
// _meta at all) sees nil rather than a stale value from a previous call.
func TestMetaFromContextReadsCallToolMeta(t *testing.T) {
	seen := make(chan map[string]any, 1)
	srv := NewServer("cerberus", "test")
	srv.RegisterTool(Tool{Name: "echo_meta", Description: "echo_meta", InputSchema: EmptyObjectSchema(), ReadOnlyHint: true,
		Handler: func(ctx context.Context, _ map[string]any) (any, error) {
			seen <- MetaFromContext(ctx)
			return "ok", nil
		},
	})

	cs := connect(t, srv, nil)
	_, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "echo_meta",
		Meta: mcpsdk.Meta{"hadron/idempotencyKey": "call-1"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	select {
	case meta := <-seen:
		if meta["hadron/idempotencyKey"] != "call-1" {
			t.Fatalf("MetaFromContext = %#v, want hadron/idempotencyKey=call-1", meta)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handler never observed a context")
	}

	if _, err := srv.CallTool(context.Background(), "echo_meta", nil); err != nil {
		t.Fatalf("direct CallTool: %v", err)
	}
	select {
	case meta := <-seen:
		if meta != nil {
			t.Fatalf("direct in-process CallTool leaked protocol _meta: %#v", meta)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("direct call never invoked the handler")
	}
}
