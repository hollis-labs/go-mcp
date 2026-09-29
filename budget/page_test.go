package budget

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

func mkItems(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("item-%03d-%s", i, strings.Repeat("x", 20))
	}
	return out
}

func envBytes(t *testing.T, e Envelope) int {
	t.Helper()
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	return len(b)
}

func walk(t *testing.T, items []string, cfg Config, fp string) (pages []Envelope, seen []string) {
	t.Helper()
	cursor := ""
	for i := 0; i < 1000; i++ {
		env, err := ApplyPage(items, Page{Cursor: cursor, Fingerprint: fp, IssueCursors: true}, cfg, "")
		if err != nil {
			t.Fatal(err)
		}
		pages = append(pages, env)
		seen = append(seen, env.Items.([]string)...)
		if env.NextCursor == "" {
			if env.HasMore {
				t.Fatal("HasMore without NextCursor")
			}
			return
		}
		cursor = env.NextCursor
	}
	t.Fatal("did not terminate")
	return
}

func TestApplyPage_WalkEveryItemOnce(t *testing.T) {
	items := mkItems(60)
	cfg := Config{Limit: 25, MaxBytes: 700}
	pages, seen := walk(t, items, cfg, "fp")
	if len(seen) != 60 {
		t.Fatalf("saw %d items, want 60", len(seen))
	}
	for i, s := range seen {
		if s != items[i] {
			t.Fatalf("item %d = %q, want %q (order/duplicates)", i, s, items[i])
		}
	}
	if len(pages) < 3 {
		t.Fatalf("expected >=3 pages, got %d", len(pages))
	}
	for i, p := range pages {
		if p.Count != len(p.Items.([]string)) {
			t.Errorf("page %d Count mismatch", i)
		}
		if envBytes(t, p) > 700 {
			t.Errorf("page %d is %d bytes > cap", i, envBytes(t, p))
		}
		last := i == len(pages)-1
		if !last && (p.TruncatedBy != "maxBytes" || !p.HasMore || !p.Truncated) {
			t.Errorf("page %d: TruncatedBy=%q HasMore=%v", i, p.TruncatedBy, p.HasMore)
		}
		if last && (p.HasMore || p.TruncatedBy != "") {
			t.Errorf("last page flagged: %+v", p)
		}
	}
}

func TestApplyPage_TokensBindsAndIsNamed(t *testing.T) {
	items := mkItems(30)
	env, err := ApplyPage(items, Page{Fingerprint: "f", IssueCursors: true}, Config{Limit: 25, MaxBytes: 100000, MaxTokens: 100}, "")
	if err != nil {
		t.Fatal(err)
	}
	if env.TruncatedBy != "maxTokens" {
		t.Fatalf("TruncatedBy = %q", env.TruncatedBy)
	}
	b, _ := json.Marshal(env)
	if EstimateTokens(b) > 100 {
		t.Fatalf("estimated tokens %d > 100", EstimateTokens(b))
	}
}

func TestApplyPage_LimitNamed(t *testing.T) {
	env, err := ApplyPage(mkItems(30), Page{Fingerprint: "f", IssueCursors: true}, Config{Limit: 5}, "")
	if err != nil {
		t.Fatal(err)
	}
	if env.TruncatedBy != "limit" || env.Count != 5 || !env.HasMore || env.NextCursor == "" || env.Total != 30 {
		t.Fatalf("%+v", env)
	}
}

func TestApplyPage_OversizeFirstRowOnePerPage(t *testing.T) {
	items := mkItems(4)
	items[0] = strings.Repeat("Z", 500)
	items[2] = strings.Repeat("Y", 500)
	cfg := Config{Limit: 10, MaxBytes: 200}
	pages, seen := walk(t, items, cfg, "fp")
	if len(seen) != 4 {
		t.Fatalf("saw %v", seen)
	}
	if pages[0].Count != 1 || pages[0].TruncatedBy != "maxBytes" {
		t.Fatalf("first page %+v", pages[0])
	}
	for i, p := range pages {
		if p.Count == 0 {
			t.Errorf("page %d empty", i)
		}
	}
}

func TestApplyPage_AllOversizeTerminates(t *testing.T) {
	items := []string{strings.Repeat("a", 300), strings.Repeat("b", 300), strings.Repeat("c", 300)}
	pages, seen := walk(t, items, Config{MaxBytes: 50}, "fp")
	if len(pages) != 3 || len(seen) != 3 {
		t.Fatalf("pages=%d seen=%d", len(pages), len(seen))
	}
	for _, p := range pages[:2] {
		if p.Count != 1 || p.TruncatedBy != "maxBytes" {
			t.Fatalf("%+v", p)
		}
	}
}

func TestApplyPage_CursorMismatch(t *testing.T) {
	items := mkItems(30)
	a, err := ApplyPage(items, Page{Fingerprint: Fingerprint("q", "A"), IssueCursors: true}, Config{Limit: 5}, "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = ApplyPage(items, Page{Cursor: a.NextCursor, Fingerprint: Fingerprint("q", "B")}, Config{Limit: 5}, "")
	if !errors.Is(err, ErrCursorMismatch) || !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("err = %v", err)
	}
	_, err = ApplyPage(items, Page{Cursor: "garbage!", Fingerprint: "x"}, Config{}, "")
	if !errors.Is(err, ErrInvalidCursor) || errors.Is(err, ErrCursorMismatch) {
		t.Fatalf("err = %v", err)
	}
}

func TestApplyPage_IssueCursorsNeedsFingerprint(t *testing.T) {
	if _, err := ApplyPage(mkItems(3), Page{IssueCursors: true}, Config{}, ""); err == nil {
		t.Fatal("want error")
	}
}

func TestApplyPage_OffsetAndBounds(t *testing.T) {
	items := mkItems(10)
	env, err := ApplyPage(items, Page{Offset: 8}, Config{Limit: 5}, "")
	if err != nil {
		t.Fatal(err)
	}
	if env.Count != 2 || env.HasMore || env.NextCursor != "" || env.Truncated {
		t.Fatalf("%+v", env)
	}
	env, _ = ApplyPage(items, Page{Offset: 99}, Config{}, "")
	if env.Count != 0 || env.NextCursor != "" {
		t.Fatalf("%+v", env)
	}
	env, _ = ApplyPage(items, Page{Offset: 3}, Config{Limit: 2}, "")
	if env.NextCursor != "" || !env.HasMore {
		t.Fatalf("no IssueCursors should emit no cursor: %+v", env)
	}
}

func TestApplyPage_MaxLimitRaisesClamp(t *testing.T) {
	items := mkItems(60)
	env, err := ApplyPage(items, Page{}, Config{Limit: 50, MaxLimit: 50}, "")
	if err != nil {
		t.Fatal(err)
	}
	if env.Count != 50 {
		t.Fatalf("Count = %d", env.Count)
	}
	if got := Apply(items, Config{Limit: 50}, "").Count; got != MaxLimit {
		t.Fatalf("default clamp changed: %d", got)
	}
	if got := Apply(items, Config{Limit: 20, MaxLimit: 5}, "").Count; got != 5 {
		t.Fatalf("lower MaxLimit: %d", got)
	}
}

func TestSeal_CursorReflectsWhatShipped(t *testing.T) {
	items := mkItems(20)
	var asked []int
	next := func(k int) (string, error) {
		asked = append(asked, k)
		return fmt.Sprintf("after-%d", k), nil
	}
	env, err := Seal(items, true, Config{Limit: 25, MaxBytes: 500}, next, "")
	if err != nil {
		t.Fatal(err)
	}
	if env.Count == 0 || env.Count >= 20 {
		t.Fatalf("Count = %d", env.Count)
	}
	if env.NextCursor != fmt.Sprintf("after-%d", env.Count) {
		t.Fatalf("cursor %q for count %d", env.NextCursor, env.Count)
	}
	if asked[len(asked)-1] != env.Count {
		t.Fatalf("last next() call %v not for final count", asked)
	}
	if envBytes(t, env) > 500 {
		t.Fatalf("over cap: %d", envBytes(t, env))
	}
	if env.Total != 0 {
		t.Fatalf("Total should be unknown: %d", env.Total)
	}
}

func TestSeal_NoCursorWhenNothingMore(t *testing.T) {
	called := false
	env, err := Seal(mkItems(3), false, Config{}, func(int) (string, error) { called = true; return "x", nil }, "")
	if err != nil || called || env.HasMore || env.NextCursor != "" || env.Truncated {
		t.Fatalf("%+v called=%v err=%v", env, called, err)
	}
}

func TestSeal_EmptyPageNoCursor(t *testing.T) {
	env, err := Seal([]string{}, true, Config{}, func(int) (string, error) { return "x", nil }, "")
	if err != nil || env.NextCursor != "" || !env.HasMore {
		t.Fatalf("%+v err=%v", env, err)
	}
}

func TestSeal_NextErrorPropagates(t *testing.T) {
	boom := errors.New("boom")
	_, err := Seal(mkItems(3), true, Config{}, func(int) (string, error) { return "", boom }, "")
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
}

func TestSeal_KeysetWalk(t *testing.T) {
	items := mkItems(60)
	fp := Fingerprint("sort", "id")
	var seen []string
	after := ""
	for i := 0; i < 100; i++ {
		start := 0
		if after != "" {
			k, err := DecodeKeyset(after, fp)
			if err != nil {
				t.Fatal(err)
			}
			for j, it := range items {
				if it == k.ID {
					start = j + 1
				}
			}
		}
		window := items[start:min(start+25, len(items))] // the "store" window
		hasMore := start+len(window) < len(items)
		env, err := Seal(window, hasMore, Config{Limit: 25, MaxBytes: 800}, func(k int) (string, error) {
			return EncodeKeyset(Keyset{SortValue: "s", ID: window[k-1]}, fp)
		}, "")
		if err != nil {
			t.Fatal(err)
		}
		seen = append(seen, env.Items.([]string)...)
		if env.NextCursor == "" {
			break
		}
		after = env.NextCursor
	}
	if len(seen) != 60 {
		t.Fatalf("saw %d", len(seen))
	}
	for i := range seen {
		if seen[i] != items[i] {
			t.Fatalf("item %d", i)
		}
	}
}

func TestApply_UncappedByteIdentical(t *testing.T) {
	items := mkItems(40)
	got := ToolJSON(Apply(items, Config{Limit: 7}, "%d found"))
	want := ToolJSON(Envelope{Items: items[:7], Count: 7, Total: 40, Truncated: true, Hint: "40 found"})
	if got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
	if strings.Contains(got, "hasMore") || strings.Contains(got, "truncatedBy") {
		t.Fatal("paging fields leaked into uncapped Apply")
	}
}

func TestApply_CappedEnforces(t *testing.T) {
	items := mkItems(40)
	env := Apply(items, Config{Limit: 25, MaxBytes: 400}, "")
	if env.Count == 0 || env.Count >= 25 || env.TruncatedBy != "maxBytes" || !env.HasMore || env.NextCursor != "" {
		t.Fatalf("%+v", env)
	}
	if envBytes(t, env) > 400 {
		t.Fatal("over cap")
	}
	if env.Total != 40 || !env.Truncated {
		t.Fatalf("%+v", env)
	}
}

func TestApply_CapNotBindingLeavesLegacyFlags(t *testing.T) {
	env := Apply(mkItems(3), Config{MaxBytes: 100000}, "")
	if env.Truncated || env.HasMore || env.TruncatedBy != "" || env.Count != 3 {
		t.Fatalf("%+v", env)
	}
}

func TestBudget_PropertyRandomPaging(t *testing.T) {
	r := rand.New(rand.NewSource(7)) //nolint:gosec // deterministic test data
	for iter := 0; iter < 200; iter++ {
		n := 1 + r.Intn(80)
		items := make([]string, n)
		for i := range items {
			items[i] = strings.Repeat("v", r.Intn(150))
		}
		cfg := Config{Limit: 1 + r.Intn(30), MaxLimit: 40, MaxBytes: 30 + r.Intn(600)}
		_, seen := walk(t, items, cfg, "p")
		if len(seen) != n {
			t.Fatalf("iter %d: saw %d of %d", iter, len(seen), n)
		}
	}
}
