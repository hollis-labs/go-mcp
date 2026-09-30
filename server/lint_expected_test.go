package server

import (
	"reflect"
	"strings"
	"testing"
)

func defsNamed(names ...string) []ToolDefinition {
	var defs []ToolDefinition
	for _, n := range names {
		defs = append(defs, ToolDefinition{Name: n, Title: "T"})
	}
	return defs
}

func expectedIssues(defs []ToolDefinition, want ...string) []Issue {
	return LintCatalog(defs, WithExpectedNames(want...))
}

func TestWithExpectedNames_ExactMatchIsClean(t *testing.T) {
	if got := expectedIssues(defsNamed("b", "a", "c"), "c", "a", "b"); len(got) != 0 {
		t.Fatalf("issues = %+v", got)
	}
	// duplicates in the expected list are one name
	if got := expectedIssues(defsNamed("a"), "a", "a"); len(got) != 0 {
		t.Fatalf("issues = %+v", got)
	}
}

func TestWithExpectedNames_UnexpectedAndMissingAreSeparateIssues(t *testing.T) {
	got := expectedIssues(defsNamed("a", "extra", "b"), "a", "b", "gone")
	want := []Issue{
		{"extra", "unexpected tool: registered but not in the expected names"},
		{"gone", "missing tool: in the expected names but not registered"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("issues = %+v\nwant %+v", got, want)
	}
}

func TestWithExpectedNames_OrderIsDeterministic(t *testing.T) {
	got := expectedIssues(defsNamed("z2", "z1"), "m2", "m1")
	var order []string
	for _, i := range got {
		order = append(order, strings.SplitN(i.Message, ":", 2)[0]+" "+i.Tool)
	}
	want := []string{"unexpected tool z1", "unexpected tool z2", "missing tool m1", "missing tool m2"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
}

func TestWithExpectedNames_NoNamesExpectsAnEmptyCatalog(t *testing.T) {
	if got := LintCatalog(nil, WithExpectedNames()); len(got) != 0 {
		t.Fatalf("empty catalog vs no names: %+v", got)
	}
	got := LintCatalog(defsNamed("a"), WithExpectedNames())
	if len(got) != 1 || !strings.HasPrefix(got[0].Message, "unexpected tool") {
		t.Fatalf("issues = %+v", got)
	}
}

func TestWithExpectedNames_OffByDefault(t *testing.T) {
	if got := LintCatalog(defsNamed("a", "b")); len(got) != 0 {
		t.Fatalf("no option must not check names: %+v", got)
	}
}

func TestWithExpectedNames_LastCallWins(t *testing.T) {
	got := LintCatalog(defsNamed("a"), WithExpectedNames("x"), WithExpectedNames("a"))
	if len(got) != 0 {
		t.Fatalf("issues = %+v", got)
	}
}

// Findings sit between the per-tool/collision findings and the instructions and
// annotation findings, in the documented fixed order.
func TestWithExpectedNames_FitsTheDocumentedOrder(t *testing.T) {
	defs := []ToolDefinition{
		{Name: "Bad-Name", Title: "T"},
		{Name: "ro", Title: "T", Annotations: ToolAnnotations{ReadOnlyHint: true, DestructiveHint: true}},
	}
	got := LintCatalog(defs, WithExpectedNames("ro", "absent"), WithLintInstructions(strings.Repeat("x", 3000)))
	var kinds []string
	for _, i := range got {
		switch {
		case strings.Contains(i.Message, "does not match"):
			kinds = append(kinds, "charset")
		case strings.HasPrefix(i.Message, "unexpected tool"):
			kinds = append(kinds, "unexpected")
		case strings.HasPrefix(i.Message, "missing tool"):
			kinds = append(kinds, "missing")
		case strings.Contains(i.Message, "instructions are"):
			kinds = append(kinds, "instructions")
		case strings.Contains(i.Message, "both set"):
			kinds = append(kinds, "annotations")
		}
	}
	want := []string{"charset", "unexpected", "missing", "instructions", "annotations"}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("order = %v, want %v (issues %+v)", kinds, want, got)
	}
}

// The real use: pin a live server's catalog.
func TestWithExpectedNames_OnARegisteredServer(t *testing.T) {
	srv := NewServer("s", "t")
	regN(srv, "one", "two")
	if got := LintCatalog(srv.ToolDefinitions(), WithExpectedNames("one", "two")); len(got) != 0 {
		t.Fatalf("issues = %+v", got)
	}
	regN(srv, "three")
	got := LintCatalog(srv.ToolDefinitions(), WithExpectedNames("one", "two"))
	if len(got) != 1 || got[0].Tool != "three" {
		t.Fatalf("a newly added tool must be flagged: %+v", got)
	}
}
