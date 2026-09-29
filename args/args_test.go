package args

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/hollis-labs/go-mcp/budget"
)

type M = map[string]any

func TestStringFamily(t *testing.T) {
	m := M{"s": "  padded ", "blank": "   ", "empty": "", "n": 1.0}
	// String: exact type, no trim (Hadron/FE/SE).
	if got := String(m, "s", "d"); got != "  padded " {
		t.Errorf("String = %q", got)
	}
	if got := String(m, "empty", "d"); got != "" {
		t.Errorf("String empty = %q, want empty (present)", got)
	}
	if got := String(m, "n", "d"); got != "d" {
		t.Errorf("String wrong type = %q", got)
	}
	if got := String(nil, "s", "d"); got != "d" {
		t.Errorf("String nil map = %q", got)
	}
	// Trimmed (Tether/Station).
	if got := Trimmed(m, "s"); got != "padded" {
		t.Errorf("Trimmed = %q", got)
	}
	if got := Trimmed(m, "n"); got != "" {
		t.Errorf("Trimmed non-string = %q", got)
	}
	// NonBlank: blank => def, otherwise value untouched (Loom stringArg).
	if got := NonBlank(m, "blank", "fb"); got != "fb" {
		t.Errorf("NonBlank blank = %q", got)
	}
	if got := NonBlank(m, "s", "fb"); got != "  padded " {
		t.Errorf("NonBlank = %q", got)
	}
	if got := NonBlank(m, "missing", "fb"); got != "fb" {
		t.Errorf("NonBlank missing = %q", got)
	}
}

func TestNumbers(t *testing.T) {
	m := M{
		"f": 2.9, "i": 7, "i64": int64(8), "jn": json.Number("9"), "jnf": json.Number("9.5"),
		"s": "50", "neg": -3.0, "zero": 0.0, "nan": "x", "b": true,
	}
	intCases := []struct {
		key  string
		want int
	}{{"f", 2}, {"i", 7}, {"i64", 8}, {"jn", 9}, {"jnf", 9}, {"s", -1}, {"neg", -3}, {"nan", -1}, {"b", -1}, {"missing", -1}}
	for _, c := range intCases {
		if got := Int(m, c.key, -1); got != c.want {
			t.Errorf("Int(%s) = %d, want %d", c.key, got, c.want)
		}
	}
	if got := Float(m, "jnf", 0); got != 9.5 {
		t.Errorf("Float json.Number = %v", got)
	}
	if got := Float(m, "s", 1.25); got != 1.25 {
		t.Errorf("Float string must not coerce: %v", got)
	}
	if got := Float(m, "i", 0); got != 7 {
		t.Errorf("Float int = %v", got)
	}
	// Station: clamp, def clamped too.
	if got := IntClamped(m, "f", 5, 1, 2); got != 2 {
		t.Errorf("IntClamped hi = %d", got)
	}
	if got := IntClamped(m, "neg", 5, 1, 100); got != 1 {
		t.Errorf("IntClamped lo = %d", got)
	}
	if got := IntClamped(m, "missing", 500, 1, 100); got != 100 {
		t.Errorf("IntClamped def clamped = %d", got)
	}
	if got := IntClamped(m, "s", 5, 1, 100); got != 5 {
		t.Errorf("IntClamped string = %d", got)
	}
	// Cerberus: > 0 only.
	if got := PositiveInt(m, "f", 30); got != 2 {
		t.Errorf("PositiveInt = %d", got)
	}
	if got := PositiveInt(m, "zero", 30); got != 30 {
		t.Errorf("PositiveInt zero = %d", got)
	}
	if got := PositiveInt(m, "neg", 30); got != 30 {
		t.Errorf("PositiveInt neg = %d", got)
	}
	if got := PositiveInt(M{"h": 0.4}, "h", 30); got != 30 {
		t.Errorf("PositiveInt 0.4 truncates to 0, want default: %d", got)
	}
}

func TestWhole(t *testing.T) {
	ok := []struct {
		v    any
		want int
	}{{3.0, 3}, {4, 4}, {json.Number("5"), 5}, {"50", 50}, {" 7 ", 7}, {-2.0, -2}}
	for _, c := range ok {
		got, err := Whole(M{"k": c.v}, "k", 99)
		if err != nil || got != c.want {
			t.Errorf("Whole(%v) = %d, %v; want %d", c.v, got, err, c.want)
		}
	}
	for _, v := range []any{2.5, "2.5", json.Number("0.1")} {
		if _, err := Whole(M{"k": v}, "k", 0); err == nil {
			t.Errorf("Whole(%v): want error", v)
		}
	}
	if got, err := Whole(M{}, "k", 42); err != nil || got != 42 {
		t.Errorf("Whole absent = %d, %v", got, err)
	}
	if got, err := Whole(M{"k": "abc"}, "k", 42); err != nil || got != 42 {
		t.Errorf("Whole non-numeric = %d, %v", got, err)
	}
}

func TestBoolAndLenient(t *testing.T) {
	m := M{"t": true, "st": "true", "sT": " TRUE ", "sf": "0", "n1": 1.0, "n0": 0.0, "junk": "maybe", "num": "50", "fl": "1.5"}
	if !Bool(m, "t", false) || Bool(m, "st", false) || !Bool(m, "st", true) {
		t.Error("Bool must be exact-type")
	}
	lb := []struct {
		key      string
		def, out bool
	}{{"t", false, true}, {"st", false, true}, {"sT", false, true}, {"sf", true, false}, {"n1", false, true}, {"n0", true, false}, {"junk", true, true}, {"junk", false, false}, {"missing", true, true}}
	for _, c := range lb {
		if got := LenientBool(m, c.key, c.def); got != c.out {
			t.Errorf("LenientBool(%s, def %v) = %v", c.key, c.def, got)
		}
	}
	if got := LenientInt(m, "num", -1); got != 50 {
		t.Errorf("LenientInt numeric string = %d", got)
	}
	if got := LenientInt(m, "fl", -1); got != 1 {
		t.Errorf("LenientInt fractional string = %d", got)
	}
	if got := LenientInt(m, "junk", -1); got != -1 {
		t.Errorf("LenientInt junk = %d", got)
	}
	if got := LenientInt(M{"k": json.Number("12")}, "k", -1); got != 12 {
		t.Errorf("LenientInt json.Number = %d", got)
	}
	if got := LenientFloat(m, "fl", 0); got != 1.5 {
		t.Errorf("LenientFloat = %v", got)
	}
	if got := LenientFloat(M{"k": "NaN"}, "k", 3); got != 3 {
		t.Errorf("LenientFloat NaN = %v", got)
	}
}

func TestStrings(t *testing.T) {
	m := M{
		"any": []any{"a", 1.0, "  ", " b "}, "typed": []string{"x", "y"}, "str": "solo", "none": nil,
	}
	if got := Strings(m, "any"); !reflect.DeepEqual(got, []string{"a", "  ", " b "}) {
		t.Errorf("Strings any = %q", got)
	}
	if got := Strings(m, "typed"); !reflect.DeepEqual(got, []string{"x", "y"}) {
		t.Errorf("Strings typed = %q", got)
	}
	if Strings(m, "str") != nil || Strings(m, "none") != nil || Strings(m, "missing") != nil {
		t.Error("Strings non-list must be nil")
	}
	if got := NonBlankStrings(m, "any"); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("NonBlankStrings = %q", got)
	}
}

func TestRequire(t *testing.T) {
	m := M{"a": "x", "b": "  ", "c": nil, "d": 0.0, "e": false}
	if e := Require(m, "a", "d", "e"); e != nil {
		t.Fatalf("present values (incl. zero and false) must pass: %v", e)
	}
	e := Require(m, "a", "b", "c", "missing")
	if e == nil || e.Code != "invalid_argument" || e.Field != "" {
		t.Fatalf("multi: %+v", e)
	}
	for _, k := range []string{"b", "c", "missing"} {
		if !strings.Contains(e.Message, k) {
			t.Errorf("message %q lacks %s", e.Message, k)
		}
	}
	if e := Require(m, "b"); e == nil || e.Field != "b" {
		t.Fatalf("single: %+v", e)
	}
	var _ error = (*budget.ToolError)(nil)
}
