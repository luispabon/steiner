package output

import (
	"path/filepath"
	"strings"
)

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

func pathStringArg(arguments map[string]any) string {
	return strings.TrimSpace(rawStringArg(arguments, "path"))
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

func splitToolPreviewLines(output string) []string {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" || trimmed == "No matches found" {
		return nil
	}
	lines := strings.Split(trimmed, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		out = append(out, line)
	}
	return out
}
