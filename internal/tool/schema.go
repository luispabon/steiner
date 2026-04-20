package tool

func ToOpenAISchema(def ToolDef) map[string]any {
	parameters := cloneSchemaMap(def.ParameterSchema)
	if parameters == nil {
		parameters = map[string]any{}
	}
	if _, ok := parameters["type"]; !ok {
		parameters["type"] = "object"
	}
	if _, ok := parameters["properties"]; !ok {
		parameters["properties"] = map[string]any{}
	}
	if _, ok := parameters["additionalProperties"]; !ok {
		parameters["additionalProperties"] = false
	}

	function := map[string]any{
		"name":       def.Name,
		"parameters": parameters,
	}
	if def.Description != "" {
		function["description"] = def.Description
	}

	return map[string]any{
		"type":     "function",
		"function": function,
	}
}

func cloneSchemaMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	cloned := make(map[string]any, len(value))
	for key, child := range value {
		cloned[key] = cloneSchemaValue(child)
	}
	return cloned
}

func cloneSchemaValue(value any) any {
	switch v := value.(type) {
	case nil:
		return nil
	case map[string]any:
		return cloneSchemaMap(v)
	case []any:
		cloned := make([]any, len(v))
		for i, child := range v {
			cloned[i] = cloneSchemaValue(child)
		}
		return cloned
	default:
		return value
	}
}
