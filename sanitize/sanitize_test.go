package sanitize

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// loadFixture reads testdata/<name>.json and returns the args map. Tests use
// this to keep fixtures byte-stable on disk for cross-tool inspection.
func loadFixture(t *testing.T, name string) map[string]any {
	t.Helper()
	path := filepath.Join("testdata", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	var args map[string]any
	if err := json.Unmarshal(data, &args); err != nil {
		t.Fatalf("unmarshal fixture %s: %v", path, err)
	}
	return args
}

// TestReportChanged verifies the Changed() helper returns true exactly when
// at least one of the three change-tracking slices/maps is non-empty.
func TestReportChanged(t *testing.T) {
	cases := []struct {
		name string
		r    Report
		want bool
	}{
		{"zero", Report{}, false},
		{"fields_cleaned", Report{FieldsCleaned: []string{"x"}}, true},
		{"recovered", Report{RecoveredFields: map[string]string{"x": "y"}}, true},
		{"dropped", Report{DroppedFragments: []string{"frag"}}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.r.Changed(); got != c.want {
				t.Fatalf("Changed() = %v, want %v", got, c.want)
			}
		})
	}
}

// TestSanitize_NilAndEmpty verifies the nil/empty-input contract: returns a
// non-nil empty map and an unchanged report.
func TestSanitize_NilAndEmpty(t *testing.T) {
	for _, in := range []map[string]any{nil, {}} {
		cleaned, report := Sanitize(in)
		if cleaned == nil {
			t.Fatalf("expected non-nil cleaned map for input %v", in)
		}
		if report.Changed() {
			t.Fatalf("expected unchanged report for input %v, got %+v", in, report)
		}
	}
}

// TestSanitize_OriginalNotMutated verifies that Sanitize never mutates its
// input map (locked API contract: clone-on-write).
func TestSanitize_OriginalNotMutated(t *testing.T) {
	args := loadFixture(t, "polluted-decision-memory.json")
	originalSummary := args["payload_summary"].(string)
	originalBody := args["payload_body"].(string)

	_, report := Sanitize(args)
	if !report.Changed() {
		t.Fatalf("expected change report on canonical fixture")
	}
	if got := args["payload_summary"].(string); got != originalSummary {
		t.Fatalf("input payload_summary mutated: got %q, want %q", got, originalSummary)
	}
	if got := args["payload_body"].(string); got != originalBody {
		t.Fatalf("input payload_body mutated")
	}
}

// TestPattern1_TrailingSelfNamedCloseTag checks pattern 1: the value ends with
// `</payload_summary>` and nothing else. The sanitizer strips the close tag
// (and trailing whitespace) and reports payload_summary as cleaned.
func TestPattern1_TrailingSelfNamedCloseTag(t *testing.T) {
	args := loadFixture(t, "polluted-trailing-close-tag.json")
	cleaned, report := Sanitize(args)

	if !report.Changed() {
		t.Fatalf("expected Changed() == true")
	}
	if !contains(report.FieldsCleaned, "payload_summary") {
		t.Fatalf("expected payload_summary in FieldsCleaned, got %v", report.FieldsCleaned)
	}
	got := cleaned["payload_summary"].(string)
	if strings.Contains(got, "</payload_summary>") {
		t.Fatalf("close tag not stripped: %q", got)
	}
	if !strings.HasSuffix(got, "self-named close tag and nothing else.") {
		t.Fatalf("trailing content not preserved: %q", got)
	}
	if got := cleaned["payload_body"].(string); got != "Clean body content for pattern 1." {
		t.Fatalf("payload_body should be untouched: %q", got)
	}
}

// TestPattern2_LeakedSiblingsRecovered exercises pattern 2 with the synthetic
// multi-sibling fixture: payload_summary contains both a payload_body and a
// tags <parameter> block, the destination fields are empty, and the sanitizer
// must inject CONTENT into both empty slots while stripping the markup from
// the source.
func TestPattern2_LeakedSiblingsRecovered(t *testing.T) {
	args := loadFixture(t, "polluted-multiple-siblings-leaked.json")
	cleaned, report := Sanitize(args)

	if !report.Changed() {
		t.Fatalf("expected Changed() == true")
	}
	if got := report.RecoveredFields["payload_body"]; !strings.HasPrefix(got, "## Body") {
		t.Fatalf("payload_body not recovered: got %q", got)
	}
	if got := cleaned["payload_body"].(string); !strings.HasPrefix(got, "## Body") {
		t.Fatalf("recovered payload_body not injected into args: got %q", got)
	}
	if got := report.RecoveredFields["tags"]; got != `["decision", "recovered"]` {
		t.Fatalf("tags not recovered: got %q", got)
	}
	// After tags string is recovered, pattern 4 should parse it into a
	// JSON array.
	tags, ok := cleaned["tags"].([]any)
	if !ok {
		t.Fatalf("tags not parsed into array: got %T %v", cleaned["tags"], cleaned["tags"])
	}
	if len(tags) != 2 || tags[0] != "decision" || tags[1] != "recovered" {
		t.Fatalf("tags array contents wrong: %v", tags)
	}
	// The source field should no longer carry the markup.
	if strings.Contains(cleaned["payload_summary"].(string), "<parameter") {
		t.Fatalf("source payload_summary still has parameter markup: %q", cleaned["payload_summary"])
	}
	if strings.Contains(cleaned["payload_summary"].(string), "</payload_summary>") {
		t.Fatalf("source payload_summary still has self close tag")
	}
}

// TestPattern2_DoesNotOverwriteExistingCleanCopy is the smoking-gun guarantee:
// when args["payload_body"] already has clean content, the sanitizer must NOT
// overwrite it with the leaked-from-summary copy. The leaked markup is still
// dropped from the source field.
func TestPattern2_DoesNotOverwriteExistingCleanCopy(t *testing.T) {
	args := loadFixture(t, "polluted-decision-memory.json")
	originalBody := args["payload_body"].(string)

	cleaned, report := Sanitize(args)

	if !report.Changed() {
		t.Fatalf("expected Changed() == true on canonical fixture")
	}
	if got := cleaned["payload_body"].(string); got != originalBody {
		t.Fatalf("payload_body was overwritten by sanitizer: got len=%d, want len=%d", len(got), len(originalBody))
	}
	if _, ok := report.RecoveredFields["payload_body"]; ok {
		t.Fatalf("payload_body must not appear in RecoveredFields when args slot was already populated")
	}
	if !contains(report.FieldsCleaned, "payload_summary") {
		t.Fatalf("payload_summary should be in FieldsCleaned: %v", report.FieldsCleaned)
	}
	gotSummary := cleaned["payload_summary"].(string)
	if strings.Contains(gotSummary, "</payload_summary>") {
		t.Fatalf("payload_summary self-close tag not stripped")
	}
	if strings.Contains(gotSummary, `<parameter name="payload_body">`) {
		t.Fatalf("leaked sibling block not stripped from payload_summary")
	}
	// The cleaned summary should end with the original sentence — not
	// trailing whitespace, not markup.
	if !strings.HasSuffix(gotSummary, "with no time for parallel migration.") {
		t.Fatalf("cleaned summary doesn't end at the natural sentence boundary: %q", gotSummary[len(gotSummary)-80:])
	}
	// At least one dropped fragment should be recorded.
	if len(report.DroppedFragments) == 0 {
		t.Fatalf("expected DroppedFragments non-empty")
	}
}

// TestPattern3_TrailingGenericCloseTag checks the heuristic for a generic
// close tag at the tail. The fixture wraps a synthetic </note> at end-of-
// string with no matching open-tag.
func TestPattern3_TrailingGenericCloseTag(t *testing.T) {
	args := map[string]any{
		"namespace":       "user/test/memory",
		"memory_key":      "decisions.test.generic_close",
		"payload_summary": "Some content that ends with a stray close tag.\n</note>",
		"payload_body":    "Clean body.",
	}
	cleaned, report := Sanitize(args)
	if !report.Changed() {
		t.Fatalf("expected Changed() == true")
	}
	if !contains(report.FieldsCleaned, "payload_summary") {
		t.Fatalf("expected payload_summary cleaned, got %v", report.FieldsCleaned)
	}
	got := cleaned["payload_summary"].(string)
	if strings.Contains(got, "</note>") {
		t.Fatalf("</note> not stripped: %q", got)
	}
}

// TestPattern3_LeavesLegitimateInlineTags ensures the heuristic doesn't strip
// short balanced inline tags (e.g., "<code>foo</code>" at end of value).
func TestPattern3_LeavesLegitimateInlineTags(t *testing.T) {
	args := map[string]any{
		"namespace":       "user/test/memory",
		"memory_key":      "decisions.test.legit_tag",
		"payload_summary": "Some prose with an inline <code>foo</code>",
		"payload_body":    "Body",
	}
	_, report := Sanitize(args)
	if report.Changed() {
		t.Fatalf("did not expect changes for legitimate inline tag, got %+v", report)
	}
}

// TestPattern4_TagsStringWithJSONArray covers the synthetic tags-as-string
// fixture: the value contains a JSON-array literal with a leaked
// <parameter> blob trailing it.
func TestPattern4_TagsStringWithJSONArray(t *testing.T) {
	args := loadFixture(t, "polluted-tags-as-string.json")
	cleaned, report := Sanitize(args)
	if !report.Changed() {
		t.Fatalf("expected Changed() == true")
	}
	tags, ok := cleaned["tags"].([]any)
	if !ok {
		t.Fatalf("tags not parsed into []any, got %T %v", cleaned["tags"], cleaned["tags"])
	}
	if len(tags) != 2 || tags[0] != "decision" || tags[1] != "test" {
		t.Fatalf("tags wrong: %v", tags)
	}
	if !contains(report.FieldsCleaned, "tags") {
		t.Fatalf("tags should be in FieldsCleaned: %v", report.FieldsCleaned)
	}
}

// TestPattern4_TagsStringNotJSON drops the value when it can't be parsed as
// a JSON string array.
func TestPattern4_TagsStringNotJSON(t *testing.T) {
	args := map[string]any{
		"tags": "this is not a json array",
	}
	cleaned, report := Sanitize(args)
	if !report.Changed() {
		t.Fatalf("expected Changed() == true")
	}
	tags, ok := cleaned["tags"].([]any)
	if !ok {
		t.Fatalf("tags should be replaced with empty []any, got %T", cleaned["tags"])
	}
	if len(tags) != 0 {
		t.Fatalf("tags should be empty, got %v", tags)
	}
}

// TestPattern4_TagsAlreadyArray leaves correctly-typed tags alone.
func TestPattern4_TagsAlreadyArray(t *testing.T) {
	args := map[string]any{
		"tags": []any{"a", "b"},
	}
	cleaned, report := Sanitize(args)
	if report.Changed() {
		t.Fatalf("expected Changed() == false for pre-typed tags array, got %+v", report)
	}
	if !reflect.DeepEqual(cleaned["tags"], []any{"a", "b"}) {
		t.Fatalf("tags array mutated: %v", cleaned["tags"])
	}
}

// TestSanitize_CleanCallControl is the no-op control: a perfectly-formed
// args map round-trips with Changed() == false and equal contents.
func TestSanitize_CleanCallControl(t *testing.T) {
	args := loadFixture(t, "clean-call.json")

	// Normalize tags for fair comparison: JSON unmarshal yields []any,
	// the sanitizer leaves []any alone — so the inputs and outputs should
	// be deep-equal.
	cleaned, report := Sanitize(args)
	if report.Changed() {
		t.Fatalf("clean call must not be changed, got %+v", report)
	}
	if !reflect.DeepEqual(cleaned, args) {
		t.Fatalf("clean call cleaned != input:\nclean: %#v\ninput: %#v", cleaned, args)
	}
}

// TestCleanFreeText_EmptyParamName covers the API contract: paramName == ""
// disables pattern 1 (since there's no self-named close-tag to look for).
func TestCleanFreeText_EmptyParamName(t *testing.T) {
	in := "Some content with </note> at the end."
	clean, recovered, changed := CleanFreeText(in, "")
	if changed {
		t.Fatalf("expected no change without paramName for an embedded inline tag, got clean=%q recovered=%v", clean, recovered)
	}
}

// TestCleanFreeText_OpenOnlySibling exercises the open-only variant where the
// leaked block runs to end-of-input (no closing </parameter>).
func TestCleanFreeText_OpenOnlySibling(t *testing.T) {
	in := "Original text.</payload_summary>\n<parameter name=\"payload_body\">## Body running to EOF"
	clean, recovered, changed := CleanFreeText(in, "payload_summary")
	if !changed {
		t.Fatalf("expected changed == true")
	}
	if got := recovered["payload_body"]; !strings.HasPrefix(got, "## Body") {
		t.Fatalf("expected payload_body recovered, got %q", got)
	}
	if !strings.HasSuffix(clean, "Original text.") {
		t.Fatalf("expected clean to end at original sentence, got %q", clean)
	}
}

// TestSanitize_NonStringField ensures Sanitize tolerates non-string slots
// (numbers, bools, nested maps) without panicking.
func TestSanitize_NonStringField(t *testing.T) {
	args := map[string]any{
		"count":      42,
		"flag":       true,
		"nested":     map[string]any{"k": "v"},
		"clean_text": "Just some clean prose.",
	}
	cleaned, report := Sanitize(args)
	if report.Changed() {
		t.Fatalf("expected unchanged, got %+v", report)
	}
	if !reflect.DeepEqual(cleaned, args) {
		t.Fatalf("non-string fields mutated")
	}
}
