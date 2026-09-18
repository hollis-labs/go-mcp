package server

import (
	"context"
	"reflect"
	"testing"
)

func TestEmptyObjectSchema(t *testing.T) {
	want := map[string]interface{}{
		"type":                 "object",
		"properties":           map[string]interface{}{},
		"additionalProperties": false,
	}
	if got := EmptyObjectSchema(); !reflect.DeepEqual(got, want) {
		t.Fatalf("EmptyObjectSchema() = %#v, want %#v", got, want)
	}
}

func TestObjectSchema(t *testing.T) {
	props := map[string]interface{}{"name": map[string]interface{}{"type": "string"}}
	got := ObjectSchema(props, "name")
	want := map[string]interface{}{
		"type":                 "object",
		"properties":           props,
		"additionalProperties": false,
		"required":             []string{"name"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ObjectSchema() = %#v, want %#v", got, want)
	}
}

func TestObjectSchemaNoRequired(t *testing.T) {
	got := ObjectSchema(map[string]interface{}{"name": map[string]interface{}{"type": "string"}})
	if _, ok := got["required"]; ok {
		t.Fatalf("ObjectSchema() with no required args set a required key: %#v", got)
	}
}

func TestPropBuilders(t *testing.T) {
	for _, tc := range []struct {
		name string
		prop Prop
		want map[string]interface{}
	}{
		{"string", StringProp("n", "d", false), map[string]interface{}{"type": "string", "description": "d"}},
		{"string enum", StringEnumProp("n", "d", false, "a", "b"), map[string]interface{}{"type": "string", "description": "d", "enum": []string{"a", "b"}}},
		{"number", NumberProp("n", "d", false), map[string]interface{}{"type": "number", "description": "d"}},
		{"integer", IntegerProp("n", "d", false), map[string]interface{}{"type": "integer", "description": "d"}},
		{"boolean", BooleanProp("n", "d", false), map[string]interface{}{"type": "boolean", "description": "d"}},
		{"array no items", ArrayProp("n", "d", false, nil), map[string]interface{}{"type": "array", "description": "d"}},
		{"array with items", ArrayProp("n", "d", false, map[string]interface{}{"type": "boolean"}), map[string]interface{}{"type": "array", "description": "d", "items": map[string]interface{}{"type": "boolean"}}},
		{"string array", StringArrayProp("n", "d", false), map[string]interface{}{"type": "array", "description": "d", "items": map[string]interface{}{"type": "string"}}},
		{"object", ObjectProp("n", "d", false), map[string]interface{}{"type": "object", "description": "d"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.prop.Name != "n" {
				t.Errorf("Name = %q, want %q", tc.prop.Name, "n")
			}
			if !reflect.DeepEqual(tc.prop.Schema, tc.want) {
				t.Errorf("Schema = %#v, want %#v", tc.prop.Schema, tc.want)
			}
		})
	}
}

func TestPropRequiredFlag(t *testing.T) {
	if StringProp("n", "d", true).Required != true {
		t.Fatal("Required = false, want true")
	}
	if StringProp("n", "d", false).Required != false {
		t.Fatal("Required = true, want false")
	}
}

func TestInputSchemaEmpty(t *testing.T) {
	if got, want := InputSchema(), EmptyObjectSchema(); !reflect.DeepEqual(got, want) {
		t.Fatalf("InputSchema() = %#v, want %#v (same as EmptyObjectSchema())", got, want)
	}
}

func TestInputSchemaPromotesRequired(t *testing.T) {
	got := InputSchema(
		StringProp("name", "the name", true),
		NumberProp("count", "how many", false),
		BooleanProp("active", "is active", true),
	)
	want := ObjectSchema(map[string]interface{}{
		"name":   map[string]interface{}{"type": "string", "description": "the name"},
		"count":  map[string]interface{}{"type": "number", "description": "how many"},
		"active": map[string]interface{}{"type": "boolean", "description": "is active"},
	}, "name", "active")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("InputSchema() = %#v, want %#v", got, want)
	}
}

func TestInputSchemaAsToolInputSchema(t *testing.T) {
	s := NewServer("test", "v1")
	s.RegisterTool(Tool{
		Name:         "greet",
		Description:  "greet someone",
		InputSchema:  InputSchema(StringProp("name", "who to greet", true)),
		ReadOnlyHint: true,
		Handler:      func(context.Context, map[string]any) (any, error) { return "ok", nil },
	})
	if _, err := s.CallTool(context.Background(), "greet", map[string]any{"name": "ada"}); err != nil {
		t.Fatalf("CallTool: %v", err)
	}
}
