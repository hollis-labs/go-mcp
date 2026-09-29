package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/hollis-labs/go-mcp/budget"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const leakedSummary = "Lead-in.</payload_summary>\n<parameter name=\"payload_body\">## Body</parameter>"

func echoTool() Tool {
	return Tool{
		Name:        "note_write",
		Description: "write a note",
		InputSchema: InputSchema(
			StringProp("payload_summary", "summary", true),
			StringProp("payload_body", "body", false),
			IntegerProp("limit", "limit", false),
		),
		Handler: func(_ context.Context, args map[string]any) (any, error) {
			return args, nil
		},
	}
}

func callArgs(t *testing.T, cs *mcpsdk.ClientSession, name string, args map[string]any) *mcpsdk.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}
	return res
}

func resultText(res *mcpsdk.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcpsdk.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// Acceptance (a): WithSanitize + StrictArgs + RegisterChecked over a real
// in-memory session.
func TestKitAcceptancePipeline(t *testing.T) {
	var logs syncBuf
	srv := NewServer("kit", "test",
		WithSanitize(slog.New(slog.NewTextHandler(&logs, nil))),
		WithToolMiddleware(StrictArgs()),
	)
	srv.RegisterChecked(echoTool(), Writes())
	cs := connect(t, srv, nil)

	// A misspelled argument is refused, naming the field and nearest name.
	res := callArgs(t, cs, "note_write", map[string]any{"payload_summary": "x", "payload_bodi": "y"})
	if !res.IsError {
		t.Fatalf("misspelled arg: IsError = false, result %q", resultText(res))
	}
	txt := resultText(res)
	if !strings.Contains(txt, "payload_bodi") || !strings.Contains(txt, "`payload_body`") {
		t.Fatalf("message should name the field and the nearest name: %s", txt)
	}
	var te budget.ToolError
	if err := json.Unmarshal([]byte(txt), &te); err != nil {
		t.Fatalf("error not a structured ToolError: %v (%s)", err, txt)
	}
	if te.Code != "invalid_argument" || te.Field != "payload_bodi" || te.NextStep == "" {
		t.Fatalf("ToolError = %+v", te)
	}

	// A missing required argument is refused too.
	res = callArgs(t, cs, "note_write", map[string]any{"payload_body": "y"})
	if !res.IsError || !strings.Contains(resultText(res), "payload_summary") {
		t.Fatalf("missing required: %v %q", res.IsError, resultText(res))
	}

	// A leaked fragment is cleaned before the guard sees it.
	res = callArgs(t, cs, "note_write", map[string]any{"payload_summary": leakedSummary, "payload_body": ""})
	if res.IsError {
		t.Fatalf("leaked fragment call failed: %s", resultText(res))
	}
	got := res.StructuredContent.(map[string]any)
	if got["payload_body"] != "## Body" || strings.Contains(got["payload_summary"].(string), "</payload_summary>") {
		t.Fatalf("not cleaned: %v", got)
	}
	if !strings.Contains(logs.String(), "cleaned tool call") {
		t.Fatalf("sanitize logged nothing: %q", logs.String())
	}

	// Transport keys pass.
	res = callArgs(t, cs, "note_write", map[string]any{"payload_summary": "x", "_traceparent": "00-abc"})
	if res.IsError {
		t.Fatalf("_traceparent refused: %s", resultText(res))
	}
	// Other underscore keys do not.
	res = callArgs(t, cs, "note_write", map[string]any{"payload_summary": "x", "_other": 1})
	if !res.IsError {
		t.Fatalf("_other should be refused")
	}
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestSanitizeIsOptIn(t *testing.T) {
	if sanitizeDefault {
		t.Fatal("sanitizeDefault must stay false")
	}
	srv := NewServer("kit", "test")
	srv.RegisterTool(echoTool())
	cs := connect(t, srv, nil)
	res := callArgs(t, cs, "note_write", map[string]any{"payload_summary": leakedSummary, "payload_body": ""})
	got := res.StructuredContent.(map[string]any)
	if got["payload_summary"] != leakedSummary || got["payload_body"] != "" {
		t.Fatalf("default server mutated args: %v", got)
	}
}

func TestWithSanitizeNilLoggerWritesToStderrNotStdout(t *testing.T) {
	origOut, origErr := os.Stdout, os.Stderr
	outR, outW, _ := os.Pipe()
	errR, errW, _ := os.Pipe()
	os.Stdout, os.Stderr = outW, errW
	restore := func() { os.Stdout, os.Stderr = origOut, origErr }
	defer restore()

	srv := NewServer("kit", "test", WithSanitize(nil))
	srv.RegisterTool(echoTool())
	cs := connect(t, srv, nil)
	callArgs(t, cs, "note_write", map[string]any{"payload_summary": leakedSummary, "payload_body": ""})

	restore()
	outW.Close()
	errW.Close()
	outB, _ := io.ReadAll(outR)
	errB, _ := io.ReadAll(errR)
	if len(outB) != 0 {
		t.Fatalf("stdout written: %q", outB)
	}
	if !strings.Contains(string(errB), "cleaned tool call") {
		t.Fatalf("stderr missing sanitize line: %q", errB)
	}
}

func TestWithSanitizeNilLoggerUsesWithLogger(t *testing.T) {
	var logs syncBuf
	srv := NewServer("kit", "test",
		WithSanitize(nil),
		WithLogger(slog.New(slog.NewTextHandler(&logs, nil))),
	)
	srv.RegisterTool(echoTool())
	cs := connect(t, srv, nil)
	callArgs(t, cs, "note_write", map[string]any{"payload_summary": leakedSummary, "payload_body": ""})
	if !strings.Contains(logs.String(), "cleaned tool call") {
		t.Fatalf("WithLogger logger not used: %q", logs.String())
	}
}

func TestToolMiddlewareOrderAndCallTool(t *testing.T) {
	var trace []string
	mk := func(name string) ToolMiddleware {
		return func(def ToolDefinition, next ToolHandler) ToolHandler {
			if def.Name != "t" {
				t.Errorf("def.Name = %q", def.Name)
			}
			return func(ctx context.Context, args map[string]any) (any, error) {
				trace = append(trace, name+">")
				r, err := next(ctx, args)
				trace = append(trace, "<"+name)
				return r, err
			}
		}
	}
	srv := NewServer("kit", "test", WithToolMiddleware(mk("a"), mk("b")), WithToolMiddleware(mk("c")))
	srv.RegisterTool(Tool{Name: "t", InputSchema: EmptyObjectSchema(), Handler: func(context.Context, map[string]any) (any, error) {
		trace = append(trace, "h")
		return "ok", nil
	}})
	if _, err := srv.CallTool(context.Background(), "t", nil); err != nil {
		t.Fatal(err)
	}
	want := "a> b> c> h <c <b <a"
	if got := strings.Join(trace, " "); got != want {
		t.Fatalf("CallTool order = %q, want %q", got, want)
	}
	trace = nil
	cs := connect(t, srv, nil)
	callArgs(t, cs, "t", nil)
	if got := strings.Join(trace, " "); got != want {
		t.Fatalf("wire order = %q, want %q", got, want)
	}
}

func TestReceivingMiddlewareOrderAfterSanitize(t *testing.T) {
	var trace []string
	mk := func(name string) mcpsdk.Middleware {
		return func(next mcpsdk.MethodHandler) mcpsdk.MethodHandler {
			return func(ctx context.Context, method string, req mcpsdk.Request) (mcpsdk.Result, error) {
				if method == "tools/call" {
					call := req.(*mcpsdk.CallToolRequest)
					trace = append(trace, name+":"+string(call.Params.Arguments))
				}
				return next(ctx, method, req)
			}
		}
	}
	srv := NewServer("kit", "test", WithReceivingMiddleware(mk("r1"), mk("r2")), WithSanitize(slog.New(slog.NewTextHandler(io.Discard, nil))))
	srv.RegisterTool(echoTool())
	cs := connect(t, srv, nil)
	callArgs(t, cs, "note_write", map[string]any{"payload_summary": leakedSummary, "payload_body": ""})
	if len(trace) != 2 || !strings.HasPrefix(trace[0], "r1:") || !strings.HasPrefix(trace[1], "r2:") {
		t.Fatalf("trace = %v", trace)
	}
	// Sanitize ran first (regardless of option order): r1 already sees clean args.
	if strings.Contains(trace[0], "parameter") {
		t.Fatalf("receiving middleware saw uncleaned args: %s", trace[0])
	}
}

func TestDuplicatePolicies(t *testing.T) {
	h := func(v string) ToolHandler {
		return func(context.Context, map[string]any) (any, error) { return v, nil }
	}
	reg := func(s *Server, v string) {
		s.RegisterTool(Tool{Name: "t", InputSchema: EmptyObjectSchema(), Handler: h(v)})
	}
	call := func(s *Server) any {
		r, _ := s.CallTool(context.Background(), "t", nil)
		return r
	}

	s := NewServer("k", "t")
	reg(s, "one")
	reg(s, "two")
	if call(s) != "two" || len(s.RegistrationErrors()) != 0 {
		t.Fatalf("replace: %v %v", call(s), s.RegistrationErrors())
	}

	s = NewServer("k", "t", WithDuplicateTools(DuplicatePanic))
	reg(s, "one")
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("DuplicatePanic did not panic")
			}
		}()
		reg(s, "two")
	}()
	if call(s) != "one" {
		t.Fatal("first registration should survive a panicking duplicate")
	}
	s.RemoveTools("t")
	reg(s, "three") // remove-then-register is not a duplicate
	if call(s) != "three" {
		t.Fatal("re-register after remove failed")
	}

	s = NewServer("k", "t", WithDuplicateTools(DuplicateRecord))
	reg(s, "one")
	reg(s, "two")
	if call(s) != "one" {
		t.Fatalf("record: first should win, got %v", call(s))
	}
	if errs := s.RegistrationErrors(); len(errs) != 1 || !strings.Contains(errs[0].Error(), `"t"`) {
		t.Fatalf("record errors = %v", errs)
	}
	s.RemoveTools("t")
	reg(s, "four")
	if call(s) != "four" || len(s.RegistrationErrors()) != 1 {
		t.Fatalf("record after remove: %v %v", call(s), s.RegistrationErrors())
	}
}

func mustPanic(t *testing.T, name string, f func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatalf("%s: expected panic", name)
		}
	}()
	f()
}

func TestRegisterCheckedPanicsAndHints(t *testing.T) {
	s := NewServer("k", "t")
	base := Tool{Name: "t", InputSchema: EmptyObjectSchema()}
	mustPanic(t, "zero Behavior", func() { s.RegisterChecked(base, Behavior{}) })
	mustPanic(t, "blank Reads why", func() { s.RegisterChecked(base, Reads("")) })
	mustPanic(t, "blank Destroys why", func() { s.RegisterChecked(base, Destroys("")) })
	withHint := base
	withHint.OpenWorldHint = true
	mustPanic(t, "double declaration", func() { s.RegisterChecked(withHint, Writes()) })

	s.RegisterChecked(Tool{Name: "r", InputSchema: EmptyObjectSchema()}, Reads("lists").OpenWorld())
	s.RegisterChecked(Tool{Name: "w", InputSchema: EmptyObjectSchema()}, Writes().Idempotent())
	s.RegisterChecked(Tool{Name: "d", InputSchema: EmptyObjectSchema()}, Destroys("deletes rows"))
	want := map[string]ToolAnnotations{
		"r": {ReadOnlyHint: true, OpenWorldHint: true},
		"w": {IdempotentHint: true},
		"d": {DestructiveHint: true},
	}
	for _, d := range s.ToolDefinitions() {
		if d.Annotations != want[d.Name] {
			t.Errorf("%s annotations = %+v, want %+v", d.Name, d.Annotations, want[d.Name])
		}
	}
	if (Behavior{}).Valid() || !Writes().Valid() {
		t.Fatal("Valid wrong")
	}
	// The wire carries the same explicit hints.
	cs := connect(t, s, nil)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range res.Tools {
		if tl.Name == "d" && (tl.Annotations.DestructiveHint == nil || !*tl.Annotations.DestructiveHint) {
			t.Fatalf("d wire hints: %+v", tl.Annotations)
		}
		if tl.Name == "w" && (tl.Annotations.DestructiveHint == nil || *tl.Annotations.DestructiveHint) {
			t.Fatalf("w wire hints: %+v", tl.Annotations)
		}
	}
}

func TestWithBehaviorRequired(t *testing.T) {
	s := NewServer("k", "t", WithBehaviorRequired())
	mustPanic(t, "RegisterTool", func() { s.RegisterTool(Tool{Name: "t", InputSchema: EmptyObjectSchema()}) })
	s.RegisterChecked(Tool{Name: "t", InputSchema: EmptyObjectSchema()}, Writes())
	if len(s.ToolDefinitions()) != 1 {
		t.Fatal("RegisterChecked should work under WithBehaviorRequired")
	}
}

// Acceptance (b): the table reproduces Nanite's cautious default and Torque's
// panic-on-missing behavior via UnknownPolicy.
func TestAnnotationTablePolicies(t *testing.T) {
	table := AnnotationTable{
		"read":  {ReadOnlyHint: true},
		"write": {}, // deliberate all-false
	}
	tool := Tool{Name: "read"}
	if err := table.Apply(&tool, UnknownPanic); err != nil || !tool.ReadOnlyHint {
		t.Fatalf("read: %v %+v", err, tool)
	}
	tool = Tool{Name: "write", DestructiveHint: true}
	if err := table.Apply(&tool, UnknownPanic); err != nil || tool.DestructiveHint {
		t.Fatalf("all-false entry must overwrite: %v %+v", err, tool)
	}

	unknown := Tool{Name: "mystery"}
	mustPanic(t, "UnknownPanic", func() { _ = table.Apply(&unknown, UnknownPanic) })
	mustPanic(t, "zero-value policy panics", func() { _ = table.Apply(&unknown, 0) })

	if err := table.Apply(&unknown, UnknownCautious); err != nil {
		t.Fatal(err)
	}
	if !unknown.DestructiveHint || !unknown.OpenWorldHint || unknown.ReadOnlyHint || unknown.IdempotentHint {
		t.Fatalf("cautious = %+v", unknown)
	}
	if CautiousAnnotations() != (ToolAnnotations{DestructiveHint: true, OpenWorldHint: true}) {
		t.Fatal("CautiousAnnotations")
	}

	untouched := Tool{Name: "mystery"}
	if err := table.Apply(&untouched, UnknownError); err == nil {
		t.Fatal("UnknownError should return an error")
	}
	if untouched.DestructiveHint || untouched.OpenWorldHint {
		t.Fatal("UnknownError must leave the tool untouched")
	}
}

func TestValidateAnnotationsNeverFlagsAllZero(t *testing.T) {
	defs := []ToolDefinition{
		{Name: "zero"},
		{Name: "ro", Annotations: ToolAnnotations{ReadOnlyHint: true}},
		{Name: "bad", Annotations: ToolAnnotations{ReadOnlyHint: true, DestructiveHint: true}},
	}
	issues := ValidateAnnotations(defs)
	if len(issues) != 1 || issues[0].Tool != "bad" {
		t.Fatalf("issues = %+v", issues)
	}
}

func TestStrictArgsOverAllSchemaTypes(t *testing.T) {
	m := ObjectSchema(map[string]any{"name": map[string]any{"type": "string"}}, "name")
	raw, _ := json.Marshal(m)
	js := &jsonschema.Schema{
		Type:       "object",
		Properties: map[string]*jsonschema.Schema{"name": {Type: "string"}},
		Required:   []string{"name"},
	}
	for label, schema := range map[string]any{"map": m, "raw": json.RawMessage(raw), "bytes": raw, "jsonschema": js} {
		t.Run(label, func(t *testing.T) {
			h := StrictArgs()(ToolDefinition{Name: "t", InputSchema: schema}, func(context.Context, map[string]any) (any, error) {
				return "ran", nil
			})
			if r, err := h(context.Background(), map[string]any{"name": "x"}); err != nil || r != "ran" {
				t.Fatalf("valid call: %v %v", r, err)
			}
			_, err := h(context.Background(), map[string]any{"name": "x", "nmea": 1})
			var te *budget.ToolError
			if !asToolError(err, &te) || te.Field != "nmea" {
				t.Fatalf("unknown arg: %v", err)
			}
			_, err = h(context.Background(), nil)
			if !asToolError(err, &te) || te.Field != "name" {
				t.Fatalf("missing arg: %v", err)
			}
		})
	}
}

func asToolError(err error, target **budget.ToolError) bool {
	return errors.As(err, target)
}

func TestStrictArgsPanicsOnUnintrospectableSchema(t *testing.T) {
	for label, schema := range map[string]any{
		"nil":           nil,
		"string":        "object",
		"no properties": map[string]any{"type": "object"},
	} {
		mustPanic(t, label, func() {
			StrictArgs()(ToolDefinition{Name: "t", InputSchema: schema}, func(context.Context, map[string]any) (any, error) { return nil, nil })
		})
	}
}

func TestStrictArgsOptions(t *testing.T) {
	def := ToolDefinition{Name: "t", InputSchema: InputSchema(StringProp("id", "id", false))}
	var seen map[string]any
	next := func(_ context.Context, a map[string]any) (any, error) { seen = a; return nil, nil }
	ctx := context.Background()

	// Default: exact keys only.
	h := StrictArgs()(def, next)
	if _, err := h(ctx, map[string]any{"_tracestate": "x"}); err != nil {
		t.Fatalf("_tracestate: %v", err)
	}
	if _, err := h(ctx, map[string]any{"_meta": 1}); err == nil {
		t.Fatal("_meta should be refused by default")
	}

	// Prefix + strip, without mutating the caller's map.
	h = StrictArgs(WithTransportPrefix("_"), WithStripTransportKeys())(def, next)
	in := map[string]any{"id": "1", "_meta": 1, "_traceparent": "z"}
	if _, err := h(ctx, in); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || seen["id"] != "1" {
		t.Fatalf("stripped args = %v", seen)
	}
	if len(in) != 3 {
		t.Fatalf("caller's map mutated: %v", in)
	}

	// Custom exact keys replace the default list.
	h = StrictArgs(WithTransportKeys("x-req"))(def, next)
	if _, err := h(ctx, map[string]any{"x-req": 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := h(ctx, map[string]any{"_traceparent": 1}); err == nil {
		t.Fatal("replaced key list should no longer exempt _traceparent")
	}

	// Retired guidance, custom code.
	h = StrictArgs(
		WithRetiredArgs(map[string]map[string]string{"t": {"old_id": "It was renamed to `id`."}}),
		WithErrorCode("arg_invalid"),
	)(def, next)
	_, err := h(ctx, map[string]any{"old_id": "1"})
	var te *budget.ToolError
	errors.As(err, &te)
	if te == nil || te.Code != "arg_invalid" || !strings.Contains(te.Message, "It was renamed to `id`.") {
		t.Fatalf("retired: %v", err)
	}

	// Violation handler keeps an app's wire shape.
	h = StrictArgs(WithViolationHandler(func(_ context.Context, d ToolDefinition, v Violation) (any, error) {
		return map[string]any{"tool": d.Name, "unknown": v.Unknown, "accepted": v.Accepted}, nil
	}))(def, next)
	r, err := h(ctx, map[string]any{"idd": 1})
	if err != nil || r.(map[string]any)["tool"] != "t" {
		t.Fatalf("handler: %v %v", r, err)
	}
}

func TestSuggestArgNames(t *testing.T) {
	accepted := []string{"namespace", "payload_data", "payload_data_schema_hash", "limit"}
	cases := []struct {
		in   string
		want []string
	}{
		{"namespcae", []string{"namespace"}},
		{"data", []string{"payload_data", "payload_data_schema_hash"}},
		{"Limit", []string{"limit"}},
		{"zzzzzz", nil},
	}
	for _, c := range cases {
		got := suggestArgNames(c.in, accepted)
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("suggest(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestValidateSchema(t *testing.T) {
	def := ToolDefinition{Name: "t", InputSchema: ObjectSchema(map[string]any{
		"limit": map[string]any{"type": "integer", "default": 10},
		"q":     map[string]any{"type": "string"},
	}, "q")}
	var seen map[string]any
	h := ValidateSchema()(def, func(_ context.Context, a map[string]any) (any, error) { seen = a; return "ok", nil })
	ctx := context.Background()

	if _, err := h(ctx, map[string]any{"q": "x"}); err != nil {
		t.Fatal(err)
	}
	if seen["limit"] != float64(10) && seen["limit"] != 10 {
		t.Fatalf("default not applied: %v", seen)
	}
	if _, err := h(ctx, map[string]any{"q": "x", "limit": "50"}); err == nil {
		t.Fatal("numeric string for an integer should fail full validation")
	}
	if _, err := h(ctx, map[string]any{"q": "x", "extra": 1}); err == nil {
		t.Fatal("additionalProperties:false should be enforced")
	}
	if _, err := h(ctx, nil); err == nil {
		t.Fatal("missing required should fail")
	}
	mustPanic(t, "bad schema", func() { ValidateSchema()(ToolDefinition{Name: "b", InputSchema: 42}, nil) })
}
