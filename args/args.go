package args

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/hollis-labs/go-mcp/budget"
)

var _ error = (*budget.ToolError)(nil)

// String returns m[key] when it is a string, exactly as sent (no trimming, an
// empty string is returned as is); otherwise def.
func String(m map[string]any, key, def string) string {
	if s, ok := m[key].(string); ok {
		return s
	}
	return def
}

// Trimmed returns m[key] with surrounding whitespace removed when it is a
// string; otherwise "".
func Trimmed(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return strings.TrimSpace(s)
}

// NonBlank returns m[key] unchanged when it is a string with any
// non-whitespace content; otherwise def.
func NonBlank(m map[string]any, key, def string) string {
	if s, ok := m[key].(string); ok && strings.TrimSpace(s) != "" {
		return s
	}
	return def
}

// Bool returns m[key] when it is a bool; otherwise def.
func Bool(m map[string]any, key string, def bool) bool {
	if b, ok := m[key].(bool); ok {
		return b
	}
	return def
}

// number converts a JSON-number-ish value; ok is false for anything else.
func number(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case int32:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

func toInt(f float64) (int, bool) {
	if math.IsNaN(f) || math.IsInf(f, 0) || f > math.MaxInt32*4096 || f < -math.MaxInt32*4096 {
		return 0, false
	}
	return int(f), true
}

// Float returns m[key] when it is a number (float64, int, int64 or
// json.Number); otherwise def.
func Float(m map[string]any, key string, def float64) float64 {
	if f, ok := number(m[key]); ok {
		return f
	}
	return def
}

// Int returns m[key] as an int when it is a number, truncating toward zero
// (2.9 becomes 2); otherwise def. Use Whole to refuse fractions.
func Int(m map[string]any, key string, def int) int {
	if f, ok := number(m[key]); ok {
		if n, ok := toInt(f); ok {
			return n
		}
	}
	return def
}

// IntClamped is Int limited to [lo, hi]; def is clamped too.
func IntClamped(m map[string]any, key string, def, lo, hi int) int {
	return max(lo, min(hi, Int(m, key, def)))
}

// PositiveInt returns m[key] as an int only when it is a number whose value
// is greater than zero; zero, negatives and non-numbers yield def.
func PositiveInt(m map[string]any, key string, def int) int {
	if f, ok := number(m[key]); ok && f > 0 {
		if n, ok := toInt(f); ok && n > 0 {
			return n
		}
	}
	return def
}

// Whole returns m[key] as an int, refusing a fractional value instead of
// truncating it. An absent or non-numeric value yields def with a nil error;
// numeric strings count (see LenientFloat).
func Whole(m map[string]any, key string, def int) (int, error) {
	f, ok := lenientNumber(m[key])
	if !ok {
		return def, nil
	}
	n, ok := toInt(f)
	if !ok || f != float64(n) {
		return 0, fmt.Errorf("%s must be a whole number, got %v", key, f)
	}
	return n, nil
}

// Strings returns m[key] when it is a []string or a []any; elements that are
// not strings are skipped. Anything else yields nil.
func Strings(m map[string]any, key string) []string {
	switch v := m[key].(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// NonBlankStrings is Strings with each element trimmed and blanks dropped.
func NonBlankStrings(m map[string]any, key string) []string {
	var out []string
	for _, s := range Strings(m, key) {
		if t := strings.TrimSpace(s); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func lenientNumber(v any) (float64, bool) {
	if f, ok := number(v); ok {
		return f, true
	}
	if s, ok := v.(string); ok {
		f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		if err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
			return f, true
		}
	}
	return 0, false
}

// LenientFloat is Float that also accepts a numeric string such as " 1.5 ".
func LenientFloat(m map[string]any, key string, def float64) float64 {
	if f, ok := lenientNumber(m[key]); ok {
		return f
	}
	return def
}

// LenientInt is Int that also accepts a numeric string such as "50".
func LenientInt(m map[string]any, key string, def int) int {
	if f, ok := lenientNumber(m[key]); ok {
		if n, ok := toInt(f); ok {
			return n
		}
	}
	return def
}

// LenientBool is Bool that also accepts the strings strconv.ParseBool
// understands (trimmed) and numbers (non-zero is true).
func LenientBool(m map[string]any, key string, def bool) bool {
	switch v := m[key].(type) {
	case bool:
		return v
	case string:
		if b, err := strconv.ParseBool(strings.TrimSpace(v)); err == nil {
			return b
		}
	default:
		if f, ok := number(v); ok {
			return f != 0
		}
	}
	return def
}

// Require reports the keys that are absent, JSON null, or a blank string.
// It returns nil when every key is present. The error has code
// "invalid_argument" and names the missing keys; Field is set when exactly
// one is missing. Return it from a handler as is.
func Require(m map[string]any, keys ...string) *budget.ToolError {
	var missing []string
	for _, k := range keys {
		v, ok := m[k]
		if s, isStr := v.(string); !ok || v == nil || (isStr && strings.TrimSpace(s) == "") {
			missing = append(missing, k)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	e := budget.NewToolError("invalid_argument", "missing required argument(s): "+strings.Join(missing, ", ")).
		WithNextStep("Provide a non-empty value for " + strings.Join(missing, ", ") + " and retry.")
	if len(missing) == 1 {
		e.Field = missing[0]
	}
	return e
}
