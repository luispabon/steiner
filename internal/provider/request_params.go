package provider

// reasoningWirePayload builds the wire-shape "reasoning" object for a
// resolved reasoning request, e.g. {"effort": "low", "summary": "auto"}.
func reasoningWirePayload(reasoning *ReasoningRequest) map[string]any {
	return map[string]any{"effort": reasoning.Effort, "summary": "auto"}
}

// mergeRequestParams merges base request fields with normalized and extra
// parameters. Later maps override earlier ones, so callers should pass the
// standard request fields as base.
func mergeRequestParams(base, params, extraParams map[string]any) map[string]any {
	m := make(map[string]any, len(base)+len(params)+len(extraParams))
	for key, value := range params {
		m[key] = value
	}
	for key, value := range extraParams {
		m[key] = value
	}
	for key, value := range base {
		m[key] = value
	}
	return m
}
