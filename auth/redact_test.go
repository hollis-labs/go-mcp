package auth

import "testing"

func TestRedact(t *testing.T) {
	in := map[string]string{
		"Authorization": "Bearer sekret",
		"X-Empty":       "",
		"X-Api-Key":     "abc123",
	}
	got := Redact(in)

	if got["Authorization"] != RedactedValue {
		t.Errorf("Authorization = %q, want redacted", got["Authorization"])
	}
	if got["X-Api-Key"] != RedactedValue {
		t.Errorf("X-Api-Key = %q, want redacted", got["X-Api-Key"])
	}
	if got["X-Empty"] != "" {
		t.Errorf("X-Empty = %q, want empty (not redacted)", got["X-Empty"])
	}

	// Original map is untouched.
	if in["Authorization"] != "Bearer sekret" {
		t.Errorf("Redact mutated its input")
	}
}

func TestMergeRedacted_KeepsStoredOnRedactedEcho(t *testing.T) {
	stored := map[string]string{"Authorization": "Bearer real-token"}
	incoming := map[string]string{"Authorization": RedactedValue}

	got := MergeRedacted(incoming, stored)
	if got["Authorization"] != "Bearer real-token" {
		t.Errorf("Authorization = %q, want stored value preserved", got["Authorization"])
	}
}

func TestMergeRedacted_AppliesRealUpdate(t *testing.T) {
	stored := map[string]string{"Authorization": "Bearer old-token"}
	incoming := map[string]string{"Authorization": "Bearer new-token"}

	got := MergeRedacted(incoming, stored)
	if got["Authorization"] != "Bearer new-token" {
		t.Errorf("Authorization = %q, want new value applied", got["Authorization"])
	}
}

func TestMergeRedacted_DropsRedactedWithNoStoredValue(t *testing.T) {
	stored := map[string]string{}
	incoming := map[string]string{"Authorization": RedactedValue}

	got := MergeRedacted(incoming, stored)
	if _, ok := got["Authorization"]; ok {
		t.Errorf("Authorization present = %q, want absent (no stored value to preserve)", got["Authorization"])
	}
}

func TestMergeRedacted_PassesThroughNewKeys(t *testing.T) {
	stored := map[string]string{"Authorization": "Bearer token"}
	incoming := map[string]string{
		"Authorization": RedactedValue,
		"X-Trace-Id":    "abc",
	}

	got := MergeRedacted(incoming, stored)
	if got["X-Trace-Id"] != "abc" {
		t.Errorf("X-Trace-Id = %q, want passthrough", got["X-Trace-Id"])
	}
	if len(got) != 2 {
		t.Errorf("len(got) = %d, want 2", len(got))
	}
}
