package server

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hollis-labs/go-mcp/budget"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func regN(srv *Server, names ...string) {
	for _, n := range names {
		srv.RegisterTool(Tool{Name: n, Title: n, Description: n, InputSchema: EmptyObjectSchema(), ReadOnlyHint: true})
	}
}

func names(ts []*mcpsdk.Tool) []string {
	var out []string
	for _, t := range ts {
		out = append(out, t.Name)
	}
	return out
}

func eq(a, b []string) bool { return strings.Join(a, ",") == strings.Join(b, ",") }

func TestWithToolOrderPinnedThenRegistrationThenName(t *testing.T) {
	srv := NewServer("s", "t", WithToolOrder("c", "missing", "a"))
	regN(srv, "z", "a", "m", "c", "b")
	want := []string{"c", "a", "z", "m", "b"}
	got := []string{}
	for _, d := range srv.ToolDefinitions() {
		got = append(got, d.Name)
	}
	if !eq(got, want) {
		t.Fatalf("ToolDefinitions = %v, want %v", got, want)
	}
	res, err := connect(t, srv, nil).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !eq(names(res.Tools), want) {
		t.Fatalf("wire = %v, want %v", names(res.Tools), want)
	}
	// Re-registering keeps the position; remove-then-add goes to the end.
	regN(srv, "z")
	srv.RemoveTools("m")
	regN(srv, "m")
	got = nil
	for _, d := range srv.ToolDefinitions() {
		got = append(got, d.Name)
	}
	if !eq(got, []string{"c", "a", "z", "b", "m"}) {
		t.Fatalf("after re-register/remove: %v", got)
	}
}

func thirty(srv *Server) {
	for i := 0; i < 30; i++ {
		regN(srv, fmt.Sprintf("t%02d", 29-i)) // registered in reverse name order
	}
}

func TestToolsListPaginationEndToEnd(t *testing.T) { // acceptance (a)
	// Installed alongside sanitize and a caller middleware, to prove the
	// catalog middleware is effective in the combined install order.
	var sawList bool
	probe := func(next mcpsdk.MethodHandler) mcpsdk.MethodHandler {
		return func(ctx context.Context, m string, r mcpsdk.Request) (mcpsdk.Result, error) {
			if m == "tools/list" {
				sawList = true
			}
			return next(ctx, m, r)
		}
	}
	srv := NewServer("s", "t", WithSanitize(nil), WithReceivingMiddleware(probe),
		WithToolOrder("t05", "t01", "t17"), WithToolsListPagination(10, nil))
	thirty(srv)
	cs := connect(t, srv, nil)

	var all []string
	cursor := ""
	for page := 1; page <= 3; page++ {
		res, err := cs.ListTools(context.Background(), &mcpsdk.ListToolsParams{Cursor: cursor})
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		if len(res.Tools) != 10 {
			t.Fatalf("page %d has %d tools", page, len(res.Tools))
		}
		all = append(all, names(res.Tools)...)
		if (res.NextCursor == "") != (page == 3) {
			t.Fatalf("page %d NextCursor=%q", page, res.NextCursor)
		}
		cursor = res.NextCursor
	}
	if !sawList {
		t.Fatal("caller middleware did not run")
	}
	want := []string{"t05", "t01", "t17"}
	for i := 0; i < 30; i++ {
		n := fmt.Sprintf("t%02d", 29-i)
		if n != "t05" && n != "t01" && n != "t17" {
			want = append(want, n)
		}
	}
	if !eq(all, want) {
		t.Fatalf("walk = %v\nwant %v", all, want)
	}
}

func TestPaginateCatalogWalksEveryToolExactlyOnce(t *testing.T) {
	srv := NewServer("s", "t")
	thirty(srv)
	seen := map[string]bool{}
	cursor, n := "", 0
	for {
		page, next, err := srv.PaginateCatalog("", cursor, 7)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range page {
			if seen[d.Name] {
				t.Fatalf("dup %s", d.Name)
			}
			seen[d.Name] = true
			n++
		}
		if next == "" {
			break
		}
		cursor = next
	}
	if n != 30 {
		t.Fatalf("walked %d", n)
	}
}

func TestPaginateCatalogCursorInvalidatedByCatalogChange(t *testing.T) { // acceptance (b)
	srv := NewServer("s", "t", WithToolsListPagination(10, nil))
	thirty(srv)
	cs := connect(t, srv, nil)
	p1, err := cs.ListTools(context.Background(), nil)
	if err != nil || p1.NextCursor == "" {
		t.Fatalf("page1: %v %q", err, p1.NextCursor)
	}
	before := srv.CatalogFingerprint()
	regN(srv, "extra")
	if srv.CatalogFingerprint() == before {
		t.Fatal("fingerprint unchanged")
	}
	_, err = cs.ListTools(context.Background(), &mcpsdk.ListToolsParams{Cursor: p1.NextCursor})
	var je *jsonrpc.Error
	if !errors.As(err, &je) || je.Code != jsonrpc.CodeInvalidParams {
		t.Fatalf("err = %v (%T), want invalid params", err, err)
	}
	if je.Code == int64(budget.ErrCodeInvalidInput) {
		t.Fatal("must not use the app-owned code")
	}
	if _, _, err := srv.PaginateCatalog("", p1.NextCursor, 10); !errors.Is(err, budget.ErrCursorMismatch) {
		t.Fatalf("PaginateCatalog err = %v", err)
	}
	if err := budget.DecodeCursor(p1.NextCursor, toolsListCursorKind, budget.Fingerprint("", srv.CatalogFingerprint()), nil); !errors.Is(err, budget.ErrCursorMismatch) {
		t.Fatalf("DecodeCursor err = %v", err)
	}
}

func TestPaginateCatalogCursorInvalidatedByProfileChange(t *testing.T) {
	srv := NewServer("s", "t")
	thirty(srv)
	_, cur, err := srv.PaginateCatalog("alpha", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := srv.PaginateCatalog("alpha", cur, 10); err != nil {
		t.Fatal(err)
	}
	if _, _, err := srv.PaginateCatalog("beta", cur, 10); !errors.Is(err, budget.ErrCursorMismatch) {
		t.Fatalf("err = %v", err)
	}
	// A cursor of another kind is refused too.
	other, _ := budget.EncodeCursor("other", budget.Fingerprint("alpha", srv.CatalogFingerprint()), catalogCursor{Offset: 1})
	if _, _, err := srv.PaginateCatalog("alpha", other, 10); !errors.Is(err, budget.ErrInvalidCursor) {
		t.Fatalf("err = %v", err)
	}
}

func TestToolsListProfileOfScopesWireCursor(t *testing.T) {
	prof := "a"
	srv := NewServer("s", "t", WithToolsListPagination(2, func(context.Context) string { return prof }))
	regN(srv, "a", "b", "c")
	cs := connect(t, srv, nil)
	p1, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	prof = "b"
	if _, err := cs.ListTools(context.Background(), &mcpsdk.ListToolsParams{Cursor: p1.NextCursor}); err == nil {
		t.Fatal("cursor from another profile accepted")
	}
}

func TestApplyCacheableAggregatesMinimumTTLAndStrictestScope(t *testing.T) { // acceptance (d)
	srv := NewServer("s", "t", WithToolsListPagination(5, nil))
	for i := 0; i < 10; i++ {
		ttl := 60000
		if i == 2 {
			ttl = 0
		}
		scope := "public"
		if i == 7 {
			scope = "private"
		}
		srv.RegisterTool(Tool{Name: fmt.Sprintf("t%d", i), Title: "x", Description: "d", InputSchema: EmptyObjectSchema(), ReadOnlyHint: true, TTLMs: ttl, CacheScope: scope})
	}
	cs := connect(t, srv, nil)
	p1, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if p1.TTLMs != 0 || p1.CacheScope != "public" {
		t.Fatalf("page1 ttl=%d scope=%q", p1.TTLMs, p1.CacheScope)
	}
	p2, err := cs.ListTools(context.Background(), &mcpsdk.ListToolsParams{Cursor: p1.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if p2.TTLMs != 60000 || p2.CacheScope != "private" {
		t.Fatalf("page2 ttl=%d scope=%q", p2.TTLMs, p2.CacheScope)
	}
}

func TestAlwaysLoadPublishedAsMetaNeverFalse(t *testing.T) { // acceptance (f)
	srv := NewServer("s", "t")
	srv.RegisterTool(Tool{Name: "on", Description: "d", InputSchema: EmptyObjectSchema(), ReadOnlyHint: true, AlwaysLoad: true})
	regN(srv, "off")
	res, err := connect(t, srv, nil).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range res.Tools {
		v, has := tl.Meta[AlwaysLoadMetaKey]
		if tl.Name == "on" && v != true {
			t.Fatalf("on: meta=%v", tl.Meta)
		}
		if tl.Name == "off" && has {
			t.Fatalf("off: meta=%v", tl.Meta)
		}
	}
}

func TestWithInstructionsOverLimitPanics(t *testing.T) { // acceptance (e)
	defer func() {
		if recover() == nil {
			t.Fatal("no panic")
		}
	}()
	NewServer("s", "t", WithInstructions(strings.Repeat("é", MaxInstructionsLen+1)))
}

func TestWithInstructionsAtLimitSucceeds(t *testing.T) {
	NewServer("s", "t", WithInstructions(strings.Repeat("é", MaxInstructionsLen)))
	if ValidateInstructions(strings.Repeat("x", MaxInstructionsLen+1)) == nil {
		t.Fatal("expected error")
	}
}

func lintFixture() []ToolDefinition {
	mk := func(name, title string) ToolDefinition {
		return ToolDefinition{Name: name, Title: title, Description: "d"}
	}
	bad := mk("contradiction", "T")
	bad.Annotations = ToolAnnotations{ReadOnlyHint: true, DestructiveHint: true}
	return []ToolDefinition{
		mk("Report", "T"), mk("report", "T"), // uppercase + case-insensitive pair
		mk(strings.Repeat("a", 65), "T"),
		mk("untitled", "  "),
		bad,
		mk("fine", "T"),
	}
}

func TestLintCatalogCharsetLengthTitleCollision(t *testing.T) { // acceptance (c)
	issues := LintCatalog(lintFixture())
	if len(issues) != 5 {
		t.Fatalf("issues = %+v", issues)
	}
	want := []string{"does not match", "names differ only by case", "characters, over the 64", "title is blank", "both set"}
	joined := ""
	for _, i := range issues {
		joined += i.Message + "\n"
	}
	for _, w := range want {
		if !strings.Contains(joined, w) {
			t.Errorf("missing %q in\n%s", w, joined)
		}
	}
}

func TestLintCatalogExtendsValidateAnnotationsWithoutDuplicating(t *testing.T) {
	defs := lintFixture()
	got := LintCatalog(defs)
	ann := ValidateAnnotations(defs)
	if len(ann) != 1 || got[len(got)-1] != ann[0] {
		t.Fatalf("got %+v ann %+v", got, ann)
	}
}

func TestLintCatalogOptions(t *testing.T) {
	defs := []ToolDefinition{{Name: "Ok-Name", Title: "T"}}
	if n := len(LintCatalog(defs, WithNameCharset(regexp.MustCompile(`^[A-Za-z-]+$`)), WithMaxNameLength(3), WithRequireChecked(), WithLintInstructions(strings.Repeat("x", 3000)))); n != 3 {
		t.Fatalf("issues = %d", n)
	}
	if n := len(LintCatalog(defs, WithNameCharset(regexp.MustCompile(`^[A-Za-z-]+$`)))); n != 0 {
		t.Fatalf("issues = %d", n)
	}
}

func TestRegisterCheckedSetsAnnotationsChecked(t *testing.T) {
	srv := NewServer("s", "t")
	srv.RegisterChecked(Tool{Name: "checked", Title: "T", Description: "d", InputSchema: EmptyObjectSchema()}, Writes())
	regN(srv, "plain")
	var flagged []string
	for _, i := range LintCatalog(srv.ToolDefinitions(), WithRequireChecked()) {
		flagged = append(flagged, i.Tool)
	}
	if !eq(flagged, []string{"plain"}) {
		t.Fatalf("flagged = %v", flagged)
	}
}

func TestLintCatalogDoesNotFlagCollisionsDuplicatePolicyAlreadyResolved(t *testing.T) {
	srv := NewServer("s", "t")
	regN(srv, "same")
	regN(srv, "same")
	defs := srv.ToolDefinitions()
	if len(defs) != 1 {
		t.Fatalf("defs = %d", len(defs))
	}
	if issues := LintCatalog(defs); len(issues) != 0 {
		t.Fatalf("issues = %+v", issues)
	}
}
