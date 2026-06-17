package output

import (
	"encoding/json"
)

func buildFetchURLPreview(result string) ToolPreview {
	// Try error payload first.
	var errPayload struct {
		URL   string `json:"url"`
		Error string `json:"error,omitempty"`
	}
	if err := json.Unmarshal([]byte(result), &errPayload); err != nil {
		return plainToolPreview()
	}
	if errPayload.Error != "" {
		return ToolPreview{
			Kind:     ToolPreviewKindFetchURL,
			Path:     errPayload.URL,
			Language: "error",
			Contents: errPayload.Error,
		}
	}

	// Parse full success result.
	var payload struct {
		URL           string            `json:"url"`
		Title         string            `json:"title,omitempty"`
		Description   string            `json:"description,omitempty"`
		Content       string            `json:"content"`
		ContentLength int               `json:"content_length"`
		StatusCode    int               `json:"status_code,omitempty"`
		Image         *jsonImagePayload `json:"image,omitempty"`
	}
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		return plainToolPreview()
	}

	// Image response: Content is empty, Image is set.
	if payload.Image != nil && payload.Content == "" {
		placeholder := ImagePlaceholder(payload.Image.Width, payload.Image.Height, payload.Image.MediaType, payload.Image.SizeBytes)
		return ToolPreview{
			Kind:       ToolPreviewKindFetchURL,
			Path:       payload.URL,
			Language:   "image",
			Contents:   placeholder,
			StatusCode: payload.StatusCode,
			FetchTitle: payload.Title,
		}
	}

	// Normal markdown response.
	return ToolPreview{
		Kind:             ToolPreviewKindFetchURL,
		Path:             payload.URL,
		Language:         "markdown",
		Contents:         payload.Content,
		ContentLength:    payload.ContentLength,
		StatusCode:       payload.StatusCode,
		FetchTitle:       payload.Title,
		FetchDescription: payload.Description,
	}
}

type jsonImagePayload struct {
	MediaType string `json:"media_type"`
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
	SizeBytes int    `json:"size_bytes"`
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
