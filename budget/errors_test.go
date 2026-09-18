package budget

import "testing"

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
