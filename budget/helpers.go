package budget

import (
	"encoding/json"
	"fmt"
)

// Clamp bounds v between min and max (inclusive).
func Clamp(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

// ExtractLimit gets the "limit" parameter from a map, clamped between 1 and
// MaxLimit. If the key is missing or not a valid number, defaultVal is
// returned (itself clamped).
func ExtractLimit(params map[string]any, defaultVal int) int {
	return extractInt(params, "limit", defaultVal, 1, MaxLimit)
}

// ExtractPagination gets limit and offset from a params map. Limit is clamped
// to [1, MaxLimit] and offset is clamped to [0, max int]. Missing keys use
// DefaultLimit and 0 respectively.
func ExtractPagination(params map[string]any) (limit, offset int) {
	limit = extractInt(params, "limit", DefaultLimit, 1, MaxLimit)
	offset = extractInt(params, "offset", 0, 0, 1<<31-1)
	return limit, offset
}

// ToolJSON marshals v to a JSON string. Retained for callers building their
// own text content by hand; a server.ToolHandler returning v directly gets
// this -- and a StructuredContent field carrying the typed value, not just
// its text rendering -- automatically. On marshal failure it returns a JSON
// error object.
func ToolJSON(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf(`{"error":"marshal_failed","message":%q}`, err.Error())
	}
	return string(data)
}

// extractInt pulls an integer value from params[key], falling back to
// defaultVal if the key is missing or not convertible. The result is
// clamped to [min, max].
func extractInt(params map[string]any, key string, defaultVal, min, max int) int {
	if params == nil {
		return Clamp(defaultVal, min, max)
	}
	raw, ok := params[key]
	if !ok {
		return Clamp(defaultVal, min, max)
	}

	var v int
	switch n := raw.(type) {
	case int:
		v = n
	case int64:
		v = int(n)
	case float64:
		v = int(n)
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return Clamp(defaultVal, min, max)
		}
		v = int(i)
	default:
		return Clamp(defaultVal, min, max)
	}
	return Clamp(v, min, max)
}
