package output

import (
	"encoding/json"
	"strings"
)

func buildEditPreview(arguments map[string]any) ToolPreview {
	path := trimmedStringArg(arguments, "path")
	before := rawStringArg(arguments, "old_string")
	after := rawStringArg(arguments, "new_string")
	if path == "" || before == "" || after == "" {
		return plainToolPreview()
	}
	return ToolPreview{
		Kind:     ToolPreviewKindEditDiff,
		Path:     path,
		Language: previewLanguage(path),
		Before:   before,
		After:    after,
	}
}

func buildWritePreview(arguments map[string]any, writeTargetExistedBefore *bool) ToolPreview {
	path := trimmedStringArg(arguments, "path")
	contents := rawStringArg(arguments, "content")
	if path == "" || contents == "" {
		return plainToolPreview()
	}
	created := false
	if writeTargetExistedBefore != nil {
		created = !*writeTargetExistedBefore
	}
	return ToolPreview{
		Kind:     ToolPreviewKindFileWrite,
		Path:     path,
		Language: previewLanguage(path),
		Contents: contents,
		Created:  created,
	}
}

func buildReadPreview(arguments map[string]any, result string) ToolPreview {
	var payload struct {
		Path      string `json:"path"`
		StartLine int    `json:"start_line"`
		Output    string `json:"output"`
	}
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		return plainToolPreview()
	}
	path := trimmedStringArg(arguments, "path")
	if path == "" {
		path = strings.TrimSpace(payload.Path)
	}
	if path == "" || payload.Output == "" {
		return plainToolPreview()
	}
	return ToolPreview{
		Kind:     ToolPreviewKindReadFile,
		Path:     path,
		Language: previewLanguage(path),
		Contents: normalizeReadPreviewContents(payload.Output, payload.StartLine),
	}
}
