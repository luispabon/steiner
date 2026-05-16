package metadata

import "encoding/json"

// ModelInfo holds metadata for a single model from the models.dev cache.
type ModelInfo struct {
	ContextWindow   int
	MaxOutputTokens int
}

// Lookup finds model metadata for the given backend model ID in the cached JSON.
// Returns zero ModelInfo if not found or if data is malformed.
func Lookup(data []byte, modelID string) ModelInfo {
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return ModelInfo{}
	}
	modelsRaw, ok := root["models"]
	if !ok {
		return ModelInfo{}
	}
	models, ok := modelsRaw.(map[string]any)
	if !ok {
		return ModelInfo{}
	}
	entry, ok := models[modelID]
	if !ok {
		return ModelInfo{}
	}
	m, ok := entry.(map[string]any)
	if !ok {
		return ModelInfo{}
	}
	info := ModelInfo{}
	if v, ok := m["context"].(float64); ok {
		info.ContextWindow = int(v)
	}
	if v, ok := m["maxOutputTokens"].(float64); ok {
		info.MaxOutputTokens = int(v)
	}
	return info
}

// CountModels returns the number of model entries in cached metadata.
func CountModels(data []byte) int {
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return 0
	}
	modelsRaw, ok := root["models"]
	if !ok {
		return 0
	}
	models, ok := modelsRaw.(map[string]any)
	if !ok {
		return 0
	}
	return len(models)
}
