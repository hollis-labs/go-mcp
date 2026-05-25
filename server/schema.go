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
