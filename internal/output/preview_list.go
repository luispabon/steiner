package output

import (
	"encoding/json"
	"strings"
)

func buildGlobPreview(arguments map[string]any, result string) ToolPreview {
	var payload struct {
		Output     string `json:"output"`
		NextOffset int    `json:"next_offset,omitempty"`
	}
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		return plainToolPreview()
	}

	path := pathStringArg(arguments)
	if path == "" {
		path = "."
	}
	lines := splitToolPreviewLines(payload.Output)
	entries := make([]ToolPreviewListEntry, 0, len(lines))
	for _, line := range lines {
		entries = append(entries, ToolPreviewListEntry{
			Path:  strings.TrimSuffix(line, "/"),
			IsDir: strings.HasSuffix(line, "/"),
		})
	}

	return ToolPreview{
		Kind:       ToolPreviewKindGlobList,
		Path:       path,
		Returned:   len(entries),
		NextOffset: payload.NextOffset,
		Entries:    entries,
	}
}
