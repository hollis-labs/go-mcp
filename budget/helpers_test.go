package budget

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestClamp(t *testing.T) {
	tests := []struct {
		v, min, max, want int
	}{
		{5, 1, 10, 5},   // within range
		{0, 1, 10, 1},   // below min
		{15, 1, 10, 10}, // above max
		{1, 1, 10, 1},   // at min
		{10, 1, 10, 10}, // at max
		{-5, 0, 25, 0},  // negative
	}
	for _, tt := range tests {
		got := Clamp(tt.v, tt.min, tt.max)
		if got != tt.want {
			t.Errorf("Clamp(%d, %d, %d) = %d, want %d", tt.v, tt.min, tt.max, got, tt.want)
		}
	}
}

func TestExtractLimit(t *testing.T) {
	tests := []struct {
		name    string
		params  map[string]any
		def     int
		want    int
	}{
		{"nil params", nil, 10, 10},
		{"missing key", map[string]any{}, 10, 10},
		{"valid int", map[string]any{"limit": 5}, 10, 5},
		{"valid float64", map[string]any{"limit": float64(15)}, 10, 15},
		{"over max", map[string]any{"limit": 100}, 10, MaxLimit},
		{"zero", map[string]any{"limit": 0}, 10, 1},
		{"negative", map[string]any{"limit": -5}, 10, 1},
		{"string value", map[string]any{"limit": "abc"}, 10, 10},
		{"json number", map[string]any{"limit": json.Number("7")}, 10, 7},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractLimit(tt.params, tt.def)
			if got != tt.want {
				t.Errorf("ExtractLimit(%v, %d) = %d, want %d", tt.params, tt.def, got, tt.want)
			}
		})
	}
}

func TestExtractPagination(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		limit, offset := ExtractPagination(nil)
		if limit != DefaultLimit {
			t.Errorf("limit = %d, want %d", limit, DefaultLimit)
		}
		if offset != 0 {
			t.Errorf("offset = %d, want 0", offset)
		}
	})

	t.Run("custom values", func(t *testing.T) {
		params := map[string]any{"limit": 20, "offset": 5}
		limit, offset := ExtractPagination(params)
		if limit != 20 {
			t.Errorf("limit = %d, want 20", limit)
		}
		if offset != 5 {
			t.Errorf("offset = %d, want 5", offset)
		}
	})

	t.Run("negative offset clamped", func(t *testing.T) {
		params := map[string]any{"offset": -10}
		_, offset := ExtractPagination(params)
		if offset != 0 {
			t.Errorf("offset = %d, want 0", offset)
		}
	})
}

func TestToolJSON(t *testing.T) {
	t.Run("map", func(t *testing.T) {
		got := ToolJSON(map[string]string{"key": "val"})
		if got != `{"key":"val"}` {
			t.Errorf("ToolJSON = %s", got)
		}
	})

	t.Run("envelope", func(t *testing.T) {
		env := Envelope{
			Items:     []string{"a"},
			Count:     1,
			Total:     1,
			Truncated: false,
		}
		got := ToolJSON(env)
		if !strings.Contains(got, `"count":1`) {
			t.Errorf("ToolJSON envelope missing count: %s", got)
		}
		if !strings.Contains(got, `"items":["a"]`) {
			t.Errorf("ToolJSON envelope missing items: %s", got)
		}
	})

	t.Run("nil", func(t *testing.T) {
		got := ToolJSON(nil)
		if got != "null" {
			t.Errorf("ToolJSON(nil) = %s, want null", got)
		}
	})
}

