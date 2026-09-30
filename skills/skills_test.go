package skills_test

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/hollis-labs/go-mcp/budget"
	"github.com/hollis-labs/go-mcp/server"
	"github.com/hollis-labs/go-mcp/skills"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func sampleMap() skills.Source {
	return skills.MapSource(
		[]skills.Meta{
			{Name: "start-here", Description: "Orientation."},
			{Name: "runs", Description: "Inspect runs."},
		},
		map[string]string{"start-here": "# Start\nbody one", "runs": "# Runs\nbody two"},
	)
}

func sampleFS(t *testing.T) skills.Source {
	t.Helper()
	src, err := skills.FSSource(fstest.MapFS{
		"zeta.md":       {Data: []byte("# Zeta skill\n\ntext")},
		"alpha.md":      {Data: []byte("\n\n## Alpha skill\nmore")},
		"start-here.md": {Data: []byte("# Orientation\nbegin")},
		"README.txt":    {Data: []byte("not a skill")},
		"sub/nested.md": {Data: []byte("# Nested")},
		".md":           {Data: []byte("nameless")},
		"empty.md":      {Data: []byte("###\n")},
	})
	if err != nil {
		t.Fatalf("FSSource: %v", err)
	}
	return src
}

func listNames(t *testing.T, src skills.Source) []string {
	t.Helper()
	metas, err := src.List()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, m := range metas {
		out = append(out, m.Name)
	}
	return out
}

func TestMapSource_ListKeepsIndexOrderAndCopiesInputs(t *testing.T) {
	index := []skills.Meta{{Name: "b", Description: "B"}, {Name: "a", Description: "A"}}
	bodies := map[string]string{"a": "A body", "b": "B body"}
	src := skills.MapSource(index, bodies)

	index[0].Name = "mutated"
	bodies["a"] = "mutated"
	got, _ := src.List()
	if got[0].Name != "b" || got[1].Name != "a" {
		t.Fatalf("List = %+v; order is the index's and later edits must not leak in", got)
	}
	if body, _ := src.Get("a"); body != "A body" {
		t.Fatalf("Get = %q", body)
	}
	got[0].Name = "changed by caller"
	again, _ := src.List()
	if again[0].Name != "b" {
		t.Fatal("List must return a copy")
	}
}

func TestMapSource_UnknownNameWrapsErrNotFound(t *testing.T) {
	_, err := sampleMap().Get("nope")
	if !errors.Is(err, skills.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestFSSource_ListsStartHereFirstThenAlphabetical(t *testing.T) {
	got := listNames(t, sampleFS(t))
	want := []string{"start-here", "alpha", "empty", "zeta"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("names = %v, want %v", got, want)
	}
}

func TestFSSource_IgnoresNonSkillFiles(t *testing.T) {
	src := sampleFS(t)
	for _, name := range []string{"README", "README.txt", "sub/nested", "nested", "sub", ""} {
		if _, err := src.Get(name); !errors.Is(err, skills.ErrNotFound) {
			t.Errorf("Get(%q) err = %v, want ErrNotFound", name, err)
		}
	}
}

func TestFSSource_DescriptionIsTheFirstLineWithHeadingMarksRemoved(t *testing.T) {
	metas, _ := sampleFS(t).List()
	desc := map[string]string{}
	for _, m := range metas {
		desc[m.Name] = m.Description
	}
	want := map[string]string{
		"zeta":       "Zeta skill",
		"alpha":      "Alpha skill",
		"start-here": "Orientation",
		"empty":      "empty", // only heading marks: falls back to the name
	}
	if !reflect.DeepEqual(desc, want) {
		t.Fatalf("descriptions = %v, want %v", desc, want)
	}
}

func TestFSSource_LongDescriptionIsCut(t *testing.T) {
	src, err := skills.FSSource(fstest.MapFS{"long.md": {Data: []byte(strings.Repeat("é", 500))}})
	if err != nil {
		t.Fatal(err)
	}
	metas, _ := src.List()
	if n := len([]rune(metas[0].Description)); n != 200 || !strings.HasSuffix(metas[0].Description, "…") {
		t.Fatalf("description has %d runes: %q", n, metas[0].Description)
	}
}

func TestFSSource_GetReturnsTheWholeFileVerbatim(t *testing.T) {
	body := "---\nfront: matter\n---\n# Title\nbody\n"
	src, err := skills.FSSource(fstest.MapFS{"with-front.md": {Data: []byte(body)}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := src.Get("with-front")
	if err != nil || got != body {
		t.Fatalf("Get = %q, %v; frontmatter is not parsed or stripped", got, err)
	}
}

// A symlink or a directory named like a skill is not a skill: os.DirFS follows
// symlinks out of the directory, so only regular files are read.
func TestFSSource_OnlyRegularFilesAreSkills(t *testing.T) {
	src, err := skills.FSSource(fstest.MapFS{
		"real.md":   {Data: []byte("# Real")},
		"link.md":   {Data: []byte("target"), Mode: fs.ModeSymlink},
		"folder.md": {Mode: fs.ModeDir | 0o755},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := listNames(t, src); !reflect.DeepEqual(got, []string{"real"}) {
		t.Fatalf("names = %v", got)
	}
	if _, err := src.Get("link"); !errors.Is(err, skills.ErrNotFound) {
		t.Fatalf("Get(link) err = %v", err)
	}
}

func TestFSSource_Errors(t *testing.T) {
	if _, err := skills.FSSource(nil); err == nil {
		t.Error("nil fs must be an error")
	}
	if _, err := skills.FSSource(fstest.MapFS{}); err == nil {
		t.Error("an fs with no skills must be an error")
	}
	if _, err := skills.FSSource(fstest.MapFS{"notes.txt": {Data: []byte("x")}, "d/x.md": {Data: []byte("x")}}); err == nil {
		t.Error("no top-level .md files must be an error")
	}
}

func TestFSSource_ReadsOnceAtConstruction(t *testing.T) {
	fsys := fstest.MapFS{"a.md": {Data: []byte("# A\nfirst")}}
	src, err := skills.FSSource(fsys)
	if err != nil {
		t.Fatal(err)
	}
	fsys["a.md"] = &fstest.MapFile{Data: []byte("changed")}
	delete(fsys, "a.md")
	if body, err := src.Get("a"); err != nil || body != "# A\nfirst" {
		t.Fatalf("Get = %q, %v; the source must not re-read its fs", body, err)
	}
}

func newServer(t *testing.T, src skills.Source, opts ...skills.Option) *server.Server {
	t.Helper()
	srv := server.NewServer("test", "0")
	if err := skills.Register(srv, "test_skills", "Skill index.", src, opts...); err != nil {
		t.Fatalf("Register: %v", err)
	}
	return srv
}

func toolError(t *testing.T, err error) *budget.ToolError {
	t.Helper()
	var te *budget.ToolError
	if !errors.As(err, &te) {
		t.Fatalf("err = %v (%T), want *budget.ToolError", err, err)
	}
	return te
}

func TestRegister_NoArgumentReturnsTheCatalog(t *testing.T) {
	srv := newServer(t, sampleMap())
	for _, args := range []map[string]any{nil, {}, {"name": ""}, {"name": "   "}, {"name": nil}} {
		got, err := srv.CallTool(context.Background(), "test_skills", args)
		if err != nil {
			t.Fatalf("args %v: %v", args, err)
		}
		raw, _ := json.Marshal(got)
		want := `{"items":[{"name":"start-here","description":"Orientation."},{"name":"runs","description":"Inspect runs."}],` +
			`"meta":{"count":2,"next":"test_skills","progressive_discovery":true}}`
		if string(raw) != want {
			t.Fatalf("args %v:\n got %s\nwant %s", args, raw, want)
		}
	}
}

func TestRegister_NameReturnsTheBody(t *testing.T) {
	srv := newServer(t, sampleMap())
	got, err := srv.CallTool(context.Background(), "test_skills", map[string]any{"name": " runs "})
	if err != nil || got != "# Runs\nbody two" {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestRegister_UnknownNameIsASkillNotFoundToolErrorPointingBack(t *testing.T) {
	srv := newServer(t, sampleMap())
	_, err := srv.CallTool(context.Background(), "test_skills", map[string]any{"name": "nope"})
	te := toolError(t, err)
	if te.Code != "skill_not_found" {
		t.Errorf("Code = %q", te.Code)
	}
	if te.HelpTool != "test_skills" {
		t.Errorf("HelpTool = %q, want the tool's own name", te.HelpTool)
	}
	if te.Field != "name" {
		t.Errorf("Field = %q", te.Field)
	}
	if !strings.Contains(te.NextStep, "test_skills") || !strings.Contains(te.NextStep, "no arguments") {
		t.Errorf("NextStep = %q", te.NextStep)
	}
	if !strings.Contains(te.Message, `"nope"`) || !strings.Contains(te.Message, "Available: [start-here, runs]") {
		t.Errorf("Message = %q; it must name the request and list the available skills", te.Message)
	}
	if te.Retryable {
		t.Error("an unknown name is not retryable as-is")
	}
}

func TestRegister_UnknownNameOnAnFSSource(t *testing.T) {
	srv := newServer(t, sampleFS(t))
	_, err := srv.CallTool(context.Background(), "test_skills", map[string]any{"name": "../secret"})
	te := toolError(t, err)
	if te.Code != "skill_not_found" || !strings.Contains(te.Message, "Available: [start-here, alpha, empty, zeta]") {
		t.Fatalf("err = %+v", te)
	}
}

func TestRegister_WithArgName(t *testing.T) {
	srv := newServer(t, sampleMap(), skills.WithArgName("topic"))
	if got, err := srv.CallTool(context.Background(), "test_skills", map[string]any{"topic": "runs"}); err != nil || got != "# Runs\nbody two" {
		t.Fatalf("got %v, %v", got, err)
	}
	// the default argument name is no longer read
	got, err := srv.CallTool(context.Background(), "test_skills", map[string]any{"name": "runs"})
	if err != nil {
		t.Fatal(err)
	}
	if _, isCatalog := got.(map[string]any); !isCatalog {
		t.Fatalf("an ignored argument must fall back to the catalog, got %v", got)
	}
	_, err = srv.CallTool(context.Background(), "test_skills", map[string]any{"topic": "nope"})
	if te := toolError(t, err); te.Field != "topic" {
		t.Errorf("Field = %q, want topic", te.Field)
	}
	def := srv.ToolDefinitions()[0]
	props := def.InputSchema.(map[string]any)["properties"].(map[string]any)
	if _, ok := props["topic"]; !ok || len(props) != 1 {
		t.Errorf("schema properties = %v", props)
	}
}

func TestRegister_NonStringArgumentIsInvalid(t *testing.T) {
	srv := newServer(t, sampleMap())
	for _, v := range []any{42, true, []any{"runs"}, map[string]any{"a": 1}} {
		_, err := srv.CallTool(context.Background(), "test_skills", map[string]any{"name": v})
		if te := toolError(t, err); te.Code != "invalid_argument" || te.Field != "name" {
			t.Errorf("value %v: %+v", v, te)
		}
	}
}

type failingSource struct {
	listErr, getErr error
	skills.Source
}

func (f failingSource) List() ([]skills.Meta, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.Source.List()
}

func (f failingSource) Get(name string) (string, error) {
	if f.getErr != nil {
		return "", f.getErr
	}
	return f.Source.Get(name)
}

func TestRegister_SourceFailuresAreInternalErrors(t *testing.T) {
	boom := errors.New("disk on fire")
	// List fails only after registration succeeded
	var mu sync.Mutex
	broken := false
	flaky := &flakySource{Source: sampleMap(), mu: &mu, broken: &broken, err: boom}
	srv := newServer(t, flaky)
	mu.Lock()
	broken = true
	mu.Unlock()

	_, err := srv.CallTool(context.Background(), "test_skills", nil)
	if te := toolError(t, err); te.Code != "internal_error" || !strings.Contains(te.Message, "disk on fire") {
		t.Errorf("catalog: %+v", te)
	}
	_, err = srv.CallTool(context.Background(), "test_skills", map[string]any{"name": "runs"})
	if te := toolError(t, err); te.Code != "internal_error" {
		t.Errorf("get: %+v", te)
	}
}

type flakySource struct {
	skills.Source
	mu     *sync.Mutex
	broken *bool
	err    error
}

func (f *flakySource) isBroken() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return *f.broken
}

func (f *flakySource) List() ([]skills.Meta, error) {
	if f.isBroken() {
		return nil, f.err
	}
	return f.Source.List()
}

func (f *flakySource) Get(name string) (string, error) {
	if f.isBroken() {
		return "", f.err
	}
	return f.Source.Get(name)
}

func TestRegister_VerifiesTheSource(t *testing.T) {
	tests := []struct {
		name string
		src  skills.Source
		want string
	}{
		{"index entry without a body", skills.MapSource([]skills.Meta{{Name: "a"}, {Name: "b"}}, map[string]string{"a": "x"}), `"b" is listed but cannot be read`},
		{"duplicate name", skills.MapSource([]skills.Meta{{Name: "a"}, {Name: "a"}}, map[string]string{"a": "x"}), `"a" is listed twice`},
		{"blank name", skills.MapSource([]skills.Meta{{Name: " "}}, map[string]string{" ": "x"}), "blank name"},
		{"list fails", failingSource{listErr: errors.New("nope"), Source: sampleMap()}, "list skills: nope"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := server.NewServer("s", "0")
			err := skills.Register(srv, "test_skills", "d", tc.src)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
			if len(srv.ToolDefinitions()) != 0 {
				t.Error("a rejected Register must not leave a tool behind")
			}
		})
	}
}

func TestRegister_RejectsBadArguments(t *testing.T) {
	src := sampleMap()
	srv := server.NewServer("s", "0")
	tests := []struct {
		name string
		err  error
	}{
		{"nil server", skills.Register(nil, "t", "d", src)},
		{"blank tool name", skills.Register(srv, " ", "d", src)},
		{"blank description", skills.Register(srv, "t", "", src)},
		{"nil source", skills.Register(srv, "t", "d", nil)},
		{"blank arg name", skills.Register(srv, "t", "d", src, skills.WithArgName(" "))},
	}
	for _, tc := range tests {
		if tc.err == nil {
			t.Errorf("%s: want an error", tc.name)
		}
	}
	if len(srv.ToolDefinitions()) != 0 {
		t.Error("rejected registrations must not leave tools behind")
	}
}

func TestRegister_RefusesAnExistingToolName(t *testing.T) {
	srv := server.NewServer("s", "0") // default duplicate policy would silently replace
	srv.RegisterTool(server.Tool{Name: "test_skills", Title: "T", Description: "d", InputSchema: server.EmptyObjectSchema(), ReadOnlyHint: true})
	err := skills.Register(srv, "test_skills", "d", sampleMap())
	if err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("err = %v", err)
	}
}

func TestRegister_ToolDefinitionIsReadOnlyIdempotentAndLintClean(t *testing.T) {
	srv := newServer(t, sampleMap(), skills.WithTitle("Skills"))
	defs := srv.ToolDefinitions()
	if len(defs) != 1 {
		t.Fatalf("defs = %d", len(defs))
	}
	d := defs[0]
	want := server.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}
	if d.Annotations != want {
		t.Errorf("annotations = %+v, want %+v", d.Annotations, want)
	}
	if d.Title != "Skills" || d.Description != "Skill index." || !d.AnnotationsChecked {
		t.Errorf("definition = %+v", d)
	}
	if issues := server.LintCatalog(defs, server.WithRequireChecked(), server.WithExpectedNames("test_skills")); len(issues) != 0 {
		t.Errorf("lint: %+v", issues)
	}
	if def := newServerDefaultTitle(t); def.Title != "test_skills" {
		t.Errorf("default title = %q, want the tool name", def.Title)
	}
}

func newServerDefaultTitle(t *testing.T) server.ToolDefinition {
	return newServer(t, sampleMap()).ToolDefinitions()[0]
}

// The same tool over a real MCP session: the catalog arrives as structured
// content, a body as text, and a bad name as an error result carrying the
// full ToolError shape.
func TestRegister_OverTheWire(t *testing.T) {
	srv := newServer(t, sampleMap())
	ctx := context.Background()
	serverT, clientT := mcpsdk.NewInMemoryTransports()
	ss, err := srv.SDKServer().Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ss.Close() }()
	cs, err := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "c", Version: "0"}, nil).Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cs.Close() }()

	tools, err := cs.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 1 || tools.Tools[0].Name != "test_skills" {
		t.Fatalf("tools = %+v, %v", tools, err)
	}
	if a := tools.Tools[0].Annotations; a == nil || !a.ReadOnlyHint || !a.IdempotentHint {
		t.Errorf("annotations = %+v", a)
	}

	res, err := cs.CallTool(ctx, &mcpsdk.CallToolParams{Name: "test_skills"})
	if err != nil || res.IsError {
		t.Fatalf("catalog: %+v, %v", res, err)
	}
	raw, _ := json.Marshal(res.StructuredContent)
	if !strings.Contains(string(raw), `"progressive_discovery":true`) || !strings.Contains(string(raw), `"next":"test_skills"`) {
		t.Errorf("structured catalog = %s", raw)
	}

	res, err = cs.CallTool(ctx, &mcpsdk.CallToolParams{Name: "test_skills", Arguments: map[string]any{"name": "runs"}})
	if err != nil || res.IsError || len(res.Content) != 1 || res.Content[0].(*mcpsdk.TextContent).Text != "# Runs\nbody two" {
		t.Fatalf("body: %+v, %v", res, err)
	}

	res, err = cs.CallTool(ctx, &mcpsdk.CallToolParams{Name: "test_skills", Arguments: map[string]any{"name": "nope"}})
	if err != nil || !res.IsError {
		t.Fatalf("unknown: %+v, %v", res, err)
	}
	raw, _ = json.Marshal(res.StructuredContent)
	for _, want := range []string{`"code":"skill_not_found"`, `"helpTool":"test_skills"`, `"field":"name"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("error content %s missing %s", raw, want)
		}
	}
}

func TestRegister_ConcurrentCalls(t *testing.T) {
	srv := newServer(t, sampleFS(t))
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, args := range []map[string]any{nil, {"name": "alpha"}, {"name": "missing"}} {
				_, _ = srv.CallTool(context.Background(), "test_skills", args)
			}
		}()
	}
	wg.Wait()
}
