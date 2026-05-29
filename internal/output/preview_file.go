package output

import (
	"encoding/json"
	"strings"
)

func buildReadPreview(arguments map[string]any, result string) ToolPreview {
	var payload struct {
		Path      string `json:"path"`
		StartLine int    `json:"start_line"`
		Output    string `json:"output"`
	}
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		return plainToolPreview()
	}
	path := pathStringArg(arguments)
	if path == "" {
		path = strings.TrimSpace(payload.Path)
	}
	if path == "" || payload.Output == "" {
		return plainToolPreview()
	}
	return ToolPreview{
		Kind:      ToolPreviewKindReadFile,
		Path:      path,
		Language:  previewLanguage(path),
		Contents:  normalizeReadPreviewContents(payload.Output, payload.StartLine),
		StartLine: payload.StartLine,
	}
}
