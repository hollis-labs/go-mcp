package budget

import (
	"encoding/json"
	"testing"
)

func TestNewProtocolError(t *testing.T) {
	err := NewProtocolError(ErrCodeNotFound, "task not found", map[string]string{"id": "42"})
	if err.Code != ErrCodeNotFound {
		t.Errorf("Code = %d, want %d", err.Code, ErrCodeNotFound)
	}
	if err.Error() == "" {
		t.Error("Error() returned empty string")
	}
}

func TestNewProtocolError_PanicsOutsideAppOwnedRange(t *testing.T) {
	cases := []ErrorCode{0, 1, -32020, -32099, -32700}
	for _, code := range cases {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("code %d: expected panic, got none", code)
				}
			}()
			NewProtocolError(code, "boom", nil)
		}()
	}
}

func TestNewProtocolError_AppOwnedRangeAccepted(t *testing.T) {
	for code := ErrCodeInternal; code >= minAppOwnedCode; code-- {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("code %d: unexpected panic: %v", code, r)
				}
			}()
			NewProtocolError(code, "ok", nil)
		}()
	}
}

func TestNewToolError(t *testing.T) {
	err := NewToolError("not_found", "run RUN-001 not found").
		WithField("run_id").
		WithRetryable(false).
		WithNextStep("call hadron_runs_list to find a valid run_id").
		WithHelpTool("hadron_runs_list")

	if err.Code != "not_found" || err.Message != "run RUN-001 not found" {
		t.Fatalf("unexpected code/message: %+v", err)
	}
	if err.Field != "run_id" {
		t.Errorf("Field = %q, want run_id", err.Field)
	}
	if err.Retryable {
		t.Error("Retryable = true, want false")
	}
	if err.NextStep == "" {
		t.Error("NextStep is empty")
	}
	if err.HelpTool != "hadron_runs_list" {
		t.Errorf("HelpTool = %q, want hadron_runs_list", err.HelpTool)
	}
	if err.Error() == "" {
		t.Error("Error() returned empty string")
	}

	// ToolError must round-trip through the JSON encoding a caller
	// returning one gets embedded in CallToolResult.StructuredContent.
	data, marshalErr := json.Marshal(err)
	if marshalErr != nil {
		t.Fatalf("Marshal: %v", marshalErr)
	}
	var parsed map[string]any
	if unmarshalErr := json.Unmarshal(data, &parsed); unmarshalErr != nil {
		t.Fatalf("Unmarshal: %v", unmarshalErr)
	}
	if parsed["code"] != "not_found" {
		t.Errorf(`parsed["code"] = %v, want "not_found"`, parsed["code"])
	}
	if parsed["nextStep"] != err.NextStep {
		t.Errorf(`parsed["nextStep"] = %v, want %q`, parsed["nextStep"], err.NextStep)
	}
}

func TestNewToolError_DefaultsOmitEmptyOptionalFields(t *testing.T) {
	err := NewToolError("internal", "boom")
	data, marshalErr := json.Marshal(err)
	if marshalErr != nil {
		t.Fatalf("Marshal: %v", marshalErr)
	}
	var parsed map[string]any
	if unmarshalErr := json.Unmarshal(data, &parsed); unmarshalErr != nil {
		t.Fatalf("Unmarshal: %v", unmarshalErr)
	}
	for _, field := range []string{"field", "nextStep", "helpTool"} {
		if _, present := parsed[field]; present {
			t.Errorf("unset field %q should be omitted, got %v", field, parsed[field])
		}
	}
	if _, present := parsed["retryable"]; !present {
		t.Error(`"retryable" should always be present (no omitempty), even when false`)
	}
}
