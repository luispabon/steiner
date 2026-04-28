package output

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

const (
	ToolPreviewKindEditDiff  = "edit_diff"
	ToolPreviewKindFileWrite = "file_write"
	ToolPreviewKindReadFile  = "read_file"
	ToolPreviewKindPlain     = "plain"
)

type ToolPreview struct {
	Kind     string
	Path     string
	Language string
	Before   string
	After    string
	Contents string
	Created  bool
}

func BuildToolPreview(tool string, arguments map[string]any, result string, writeTargetExistedBefore *bool) ToolPreview {
	switch strings.ToLower(strings.TrimSpace(tool)) {
	case "edit":
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
	case "write", "write_file":
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
	case "read", "read_file":
		path := trimmedStringArg(arguments, "path")
		if path == "" {
			return plainToolPreview()
		}
		contents, ok := readContentsFromResult(result)
		if !ok {
			return plainToolPreview()
		}
		return ToolPreview{
			Kind:     ToolPreviewKindReadFile,
			Path:     path,
			Language: previewLanguage(path),
			Contents: contents,
		}
	default:
		return plainToolPreview()
	}
}

func plainToolPreview() ToolPreview {
	return ToolPreview{Kind: ToolPreviewKindPlain}
}

func rawStringArg(arguments map[string]any, key string) string {
	if arguments == nil {
		return ""
	}
	value, ok := arguments[key]
	if !ok {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return text
}

func trimmedStringArg(arguments map[string]any, key string) string {
	return strings.TrimSpace(rawStringArg(arguments, key))
}

func readContentsFromResult(result string) (string, bool) {
	var payload struct {
		Output string `json:"output"`
	}
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		return "", false
	}
	if payload.Output == "" {
		return "", false
	}
	return payload.Output, true
}

func previewLanguage(path string) string {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	switch ext {
	case "md":
		return "markdown"
	case "yml":
		return "yaml"
	default:
		return ext
	}
}

func CountPreviewChanges(doc PreviewDocument) (adds, removes int) {
	for _, line := range doc.Lines {
		switch line.Kind {
		case PreviewLineKindAdded:
			adds++
		case PreviewLineKindRemoved:
			removes++
		}
	}
	return adds, removes
}
