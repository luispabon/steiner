package output

import (
	"encoding/json"
	"strconv"
	"strings"
)

func buildGrepPreview(arguments map[string]any, result string) ToolPreview {
	var payload struct {
		Matches    int    `json:"matches"`
		Returned   int    `json:"returned"`
		Truncated  bool   `json:"truncated,omitempty"`
		HasMore    bool   `json:"has_more,omitempty"`
		NextOffset int    `json:"next_offset,omitempty"`
		Output     string `json:"output"`
	}
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		return plainToolPreview()
	}

	mode := strings.ToLower(strings.TrimSpace(rawStringArg(arguments, "output_mode")))
	if mode == "" {
		mode = "content"
	}
	path := pathStringArg(arguments)
	if path == "" {
		path = "."
	}

	preview := ToolPreview{
		Kind:       ToolPreviewKindGrep,
		Path:       path,
		Truncated:  payload.Truncated,
		HasMore:    payload.HasMore,
		Returned:   payload.Returned,
		NextOffset: payload.NextOffset,
		OutputMode: mode,
		Output:     payload.Output,
	}

	switch mode {
	case "files_with_matches":
		preview.GrepFiles = previewGrepFiles(payload.Output)
	case "count":
		preview.GrepFiles = previewGrepCounts(payload.Output)
	default:
		preview.GrepFiles = previewGrepContent(payload.Output)
	}

	return preview
}

func previewGrepFiles(output string) []ToolPreviewGrepFile {
	lines := splitToolPreviewLines(output)
	files := make([]ToolPreviewGrepFile, 0, len(lines))
	for _, line := range lines {
		files = append(files, ToolPreviewGrepFile{Path: line})
	}
	return files
}

func previewGrepCounts(output string) []ToolPreviewGrepFile {
	lines := splitToolPreviewLines(output)
	files := make([]ToolPreviewGrepFile, 0, len(lines))
	for _, line := range lines {
		path := line
		count := 0
		if idx := strings.LastIndex(line, ":"); idx >= 0 {
			path = line[:idx]
			if n, err := strconv.Atoi(strings.TrimSpace(line[idx+1:])); err == nil {
				count = n
			}
		}
		files = append(files, ToolPreviewGrepFile{Path: path, Count: count})
	}
	return files
}

func previewGrepContent(output string) []ToolPreviewGrepFile {
	lines := splitToolPreviewLines(output)
	var files []ToolPreviewGrepFile
	var current *ToolPreviewGrepFile
	for _, line := range lines {
		if strings.HasPrefix(line, "## ") {
			path := strings.TrimSpace(strings.TrimPrefix(line, "## "))
			files = append(files, ToolPreviewGrepFile{Path: path})
			current = &files[len(files)-1]
			continue
		}
		if current == nil {
			continue
		}
		match := ToolPreviewGrepMatch{}
		if idx := strings.Index(line, ": "); idx > 0 {
			if n, err := strconv.Atoi(strings.TrimSpace(line[:idx])); err == nil {
				match.LineNumber = n
				match.Text = line[idx+2:]
			} else {
				match.Text = line
			}
		} else {
			match.Text = line
		}
		current.Matches = append(current.Matches, match)
	}
	return files
}
