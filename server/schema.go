package server

// EmptyObjectSchema returns a strict empty JSON object schema.
func EmptyObjectSchema() map[string]interface{} {
	return ObjectSchema(nil)
}

// ObjectSchema returns a strict JSON object schema with optional required fields.
func ObjectSchema(properties map[string]interface{}, required ...string) map[string]interface{} {
	if properties == nil {
		properties = map[string]interface{}{}
	}
	schema := map[string]interface{}{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// Prop is one property definition for InputSchema: its JSON Schema shape,
// plus whether it belongs in the parent object's `required` list. It is the
// property-level counterpart to ObjectSchema.
//
// This is generalized from near-identical property-builder helpers eight
// apps across the portfolio (Tether, Hadron, Tesseract, Torque,
// fragments-engine, Stack Explorer, NIL, ...) independently wrote after
// migrating off mark3labs/mcp-go, whose typed
// mcp.NewTool(mcp.WithString(...), mcp.Required(), mcp.Description(...))
// builder chain go-mcp's raw any InputSchema has no equivalent for. Nothing
// here is more general than the JSON Schema vocabulary those apps' tools
// actually used (string, string-enum, number, integer, boolean, array,
// string-array, free-form object) -- add a new builder only when a tool
// needs a shape none of these cover.
type Prop struct {
	Name     string
	Schema   map[string]interface{}
	Required bool
}

// StringProp declares a string property.
func StringProp(name, desc string, required bool) Prop {
	return Prop{Name: name, Schema: map[string]interface{}{"type": "string", "description": desc}, Required: required}
}

// StringEnumProp declares a string property restricted to an enumerated set
// of values.
func StringEnumProp(name, desc string, required bool, values ...string) Prop {
	return Prop{Name: name, Schema: map[string]interface{}{"type": "string", "description": desc, "enum": values}, Required: required}
}

// NumberProp declares a number property (JSON Schema "number" -- integer or
// floating-point). Use IntegerProp when the value must be a whole number.
func NumberProp(name, desc string, required bool) Prop {
	return Prop{Name: name, Schema: map[string]interface{}{"type": "number", "description": desc}, Required: required}
}

// IntegerProp declares an integer property (JSON Schema "integer").
func IntegerProp(name, desc string, required bool) Prop {
	return Prop{Name: name, Schema: map[string]interface{}{"type": "integer", "description": desc}, Required: required}
}

// BooleanProp declares a boolean property.
func BooleanProp(name, desc string, required bool) Prop {
	return Prop{Name: name, Schema: map[string]interface{}{"type": "boolean", "description": desc}, Required: required}
}

// ArrayProp declares an array property. items is the item schema (e.g.
// map[string]interface{}{"type": "string"}); nil leaves the item type
// unconstrained.
func ArrayProp(name, desc string, required bool, items map[string]interface{}) Prop {
	schema := map[string]interface{}{"type": "array", "description": desc}
	if items != nil {
		schema["items"] = items
	}
	return Prop{Name: name, Schema: schema, Required: required}
}

// StringArrayProp declares an array-of-strings property.
func StringArrayProp(name, desc string, required bool) Prop {
	return ArrayProp(name, desc, required, map[string]interface{}{"type": "string"})
}

// ObjectProp declares a free-form (untyped-properties) object property.
func ObjectProp(name, desc string, required bool) Prop {
	return Prop{Name: name, Schema: map[string]interface{}{"type": "object", "description": desc}, Required: required}
}

// InputSchema builds a strict JSON object schema (via ObjectSchema) from a
// set of property definitions -- the property-level counterpart to
// EmptyObjectSchema/ObjectSchema for a tool whose properties are declared
// individually rather than assembled by hand. Called with no props, it
// produces the same strict empty-object schema EmptyObjectSchema does.
func InputSchema(props ...Prop) map[string]interface{} {
	properties := make(map[string]interface{}, len(props))
	var required []string
	for _, p := range props {
		properties[p.Name] = p.Schema
		if p.Required {
			required = append(required, p.Name)
		}
	}
	return ObjectSchema(properties, required...)
}
