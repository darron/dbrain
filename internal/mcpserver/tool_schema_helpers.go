package mcpserver

import "strings"

func objectSchema(properties map[string]interface{}, required ...string) map[string]interface{} {
	schema := map[string]interface{}{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func genericObjectSchema(description string) map[string]interface{} {
	schema := map[string]interface{}{"type": "object"}
	if strings.TrimSpace(description) != "" {
		schema["description"] = description
	}
	return schema
}

func arraySchema(items interface{}) map[string]interface{} {
	return map[string]interface{}{
		"type":  "array",
		"items": items,
	}
}

func scalarSchema(valueType string, description string) map[string]interface{} {
	schema := map[string]interface{}{
		"type": valueType,
	}
	if strings.TrimSpace(description) != "" {
		schema["description"] = description
	}
	return schema
}

func enumSchema(description string, values ...string) map[string]interface{} {
	schema := scalarSchema("string", description)
	enumValues := make([]interface{}, 0, len(values))
	for _, value := range values {
		enumValues = append(enumValues, value)
	}
	schema["enum"] = enumValues
	return schema
}
