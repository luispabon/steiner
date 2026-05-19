package output

import (
	"encoding/json"
)

func buildFetchURLPreview(result string) ToolPreview {
	var payload struct {
		URL     string `json:"url"`
		Content string `json:"content"`
		Error   string `json:"error,omitempty"`
	}
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		return plainToolPreview()
	}
	if payload.Error != "" || payload.Content == "" {
		return plainToolPreview()
	}
	return ToolPreview{
		Kind:     ToolPreviewKindFetchURL,
		Path:     payload.URL,
		Language: "markdown",
		Contents: payload.Content,
	}
}

func buildWebSearchPreview(result string) ToolPreview {
	var errPayload struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(result), &errPayload); err == nil && errPayload.Error != "" {
		return plainToolPreview()
	}

	var results []map[string]string
	if err := json.Unmarshal([]byte(result), &results); err != nil {
		return plainToolPreview()
	}

	indented, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return plainToolPreview()
	}

	return ToolPreview{
		Kind:     ToolPreviewKindWebSearch,
		Path:     "search results",
		Language: "json",
		Contents: string(indented),
		Returned: len(results),
	}
}
