package tool

func ToOpenAISchema(def ToolDef) map[string]any {
	parameters := CloneJSONMap(def.ParameterSchema)
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
