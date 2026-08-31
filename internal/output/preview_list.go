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
	entries := previewListEntries(payload.Output)
	return ToolPreview{
		Kind:       ToolPreviewKindGlobList,
		Path:       path,
		Returned:   len(entries),
		NextOffset: payload.NextOffset,
		Entries:    entries,
	}
}

func buildLSPreview(arguments map[string]any, result string) ToolPreview {
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
	entries := previewListEntries(payload.Output)
	for i := range entries {
		entries[i].IsDir = strings.HasSuffix(entries[i].Path, "/")
		entries[i].Path = strings.TrimSuffix(entries[i].Path, "/")
	}

	return ToolPreview{
		Kind:       ToolPreviewKindLSList,
		Path:       path,
		Returned:   len(entries),
		NextOffset: payload.NextOffset,
		Entries:    entries,
	}
}

func previewListEntries(output string) []ToolPreviewListEntry {
	lines := splitToolPreviewLines(output)
	entries := make([]ToolPreviewListEntry, 0, len(lines))
	for _, line := range lines {
		entries = append(entries, ToolPreviewListEntry{Path: line})
	}
	return entries
}
