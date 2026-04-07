package budget

import "testing"

func TestApply_EmptyList(t *testing.T) {
	env := Apply([]string{}, Config{}, "")
	if env.Count != 0 {
		t.Errorf("Count = %d, want 0", env.Count)
	}
	if env.Total != 0 {
		t.Errorf("Total = %d, want 0", env.Total)
	}
	if env.Truncated {
		t.Error("Truncated should be false for empty list")
	}
	if env.Hint != "" {
		t.Errorf("Hint = %q, want empty", env.Hint)
	}
}

func TestApply_UnderLimit(t *testing.T) {
	items := []string{"a", "b", "c"}
	env := Apply(items, Config{Limit: 10}, "")
	if env.Count != 3 {
		t.Errorf("Count = %d, want 3", env.Count)
	}
	if env.Total != 3 {
		t.Errorf("Total = %d, want 3", env.Total)
	}
	if env.Truncated {
		t.Error("Truncated should be false when under limit")
	}
	if env.Hint != "" {
		t.Errorf("Hint = %q, want empty", env.Hint)
	}
	got := env.Items.([]string)
	if len(got) != 3 {
		t.Errorf("Items length = %d, want 3", len(got))
	}
}

func TestApply_ExactLimit(t *testing.T) {
	items := make([]int, 10)
	for i := range items {
		items[i] = i
	}
	env := Apply(items, Config{Limit: 10}, "")
	if env.Count != 10 {
		t.Errorf("Count = %d, want 10", env.Count)
	}
	if env.Truncated {
		t.Error("Truncated should be false at exact limit")
	}
}

func TestApply_OverLimit_Truncated(t *testing.T) {
	items := make([]int, 47)
	for i := range items {
		items[i] = i
	}
	env := Apply(items, Config{Limit: 10}, "%d tasks found. Use volon_task_get for details.")
	if env.Count != 10 {
		t.Errorf("Count = %d, want 10", env.Count)
	}
	if env.Total != 47 {
		t.Errorf("Total = %d, want 47", env.Total)
	}
	if !env.Truncated {
		t.Error("Truncated should be true when over limit")
	}
	want := "47 tasks found. Use volon_task_get for details."
	if env.Hint != want {
		t.Errorf("Hint = %q, want %q", env.Hint, want)
	}
	got := env.Items.([]int)
	if len(got) != 10 {
		t.Errorf("Items length = %d, want 10", len(got))
	}
}

func TestApply_OverLimit_DefaultHint(t *testing.T) {
	items := make([]int, 30)
	env := Apply(items, Config{Limit: 5}, "")
	if !env.Truncated {
		t.Error("Truncated should be true")
	}
	if env.Hint == "" {
		t.Error("Hint should not be empty when truncated with no template")
	}
}

func TestApply_DefaultConfig(t *testing.T) {
	items := make([]int, 20)
	env := Apply(items, Config{}, "")
	// Default limit is 10
	if env.Count != DefaultLimit {
		t.Errorf("Count = %d, want %d (DefaultLimit)", env.Count, DefaultLimit)
	}
	if !env.Truncated {
		t.Error("Truncated should be true (20 > 10)")
	}
}

func TestApply_LimitClampedToMax(t *testing.T) {
	items := make([]int, 50)
	env := Apply(items, Config{Limit: 100}, "")
	// Limit should be clamped to MaxLimit (25)
	if env.Count != MaxLimit {
		t.Errorf("Count = %d, want %d (MaxLimit)", env.Count, MaxLimit)
	}
}

type testRecord struct {
	ID    string
	Title string
}

func TestApply_StructSlice(t *testing.T) {
	items := []testRecord{
		{ID: "1", Title: "First"},
		{ID: "2", Title: "Second"},
		{ID: "3", Title: "Third"},
	}
	env := Apply(items, Config{Limit: 2}, "%d records found.")
	if env.Count != 2 {
		t.Errorf("Count = %d, want 2", env.Count)
	}
	if env.Total != 3 {
		t.Errorf("Total = %d, want 3", env.Total)
	}
	if !env.Truncated {
		t.Error("expected truncated")
	}
	got := env.Items.([]testRecord)
	if got[0].ID != "1" || got[1].ID != "2" {
		t.Errorf("unexpected items: %+v", got)
	}
}
