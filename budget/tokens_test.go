package budget

import "testing"

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		name  string
		input []byte
		want  int
	}{
		{"empty", []byte{}, 0},
		{"one byte", []byte("a"), 1},
		{"four bytes", []byte("abcd"), 1},
		{"five bytes", []byte("abcde"), 2},
		{"eight bytes", []byte("abcdefgh"), 2},
		{"typical JSON", []byte(`{"id":"TASK-001","title":"Fix bug","status":"doing"}`), 13},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EstimateTokens(tt.input)
			if got != tt.want {
				t.Errorf("EstimateTokens(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestEstimateTokensFromString(t *testing.T) {
	s := "hello world" // 11 bytes -> (11+3)/4 = 3
	got := EstimateTokensFromString(s)
	if got != 3 {
		t.Errorf("EstimateTokensFromString(%q) = %d, want 3", s, got)
	}
}

func TestEstimateTokens_DefaultBudget(t *testing.T) {
	// A response at the byte budget should be roughly at the token budget
	payload := make([]byte, DefaultMaxBytes)
	tokens := EstimateTokens(payload)
	if tokens != DefaultMaxTokens {
		t.Errorf("EstimateTokens(%d bytes) = %d tokens, want %d (DefaultMaxTokens)",
			DefaultMaxBytes, tokens, DefaultMaxTokens)
	}
}
