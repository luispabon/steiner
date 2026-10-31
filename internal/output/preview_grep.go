package output

import (
	"encoding/json"
	"strconv"
	"strings"
)

func buildGrepPreview(arguments map[string]any, result string) ToolPreview {
	var payload struct {
		Shown      *int   `json:"shown"`
		Matches    *int   `json:"matches"`
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

	var grepFiles []ToolPreviewGrepFile
	totalMatches := 0
	if payload.Matches != nil {
		totalMatches = *payload.Matches
	}
	matches := 0
	switch mode {
	case "files_with_matches":
		grepFiles = previewGrepFiles(payload.Output)
	case "count":
		grepFiles = previewGrepCounts(payload.Output)
		for _, file := range grepFiles {
			matches += file.Count
		}
	default:
		grepFiles = previewGrepContent(payload.Output)
		// Content lines include context, so the shown logical result count is
		// the exact match count. Results saved before shown existed have none,
		// so they fall back to counting match rows.
		if payload.Shown != nil {
			matches = *payload.Shown
		} else {
			matches = countLegacyGrepMatchRows(payload.Output)
		}
	}

	preview := ToolPreview{
		Kind:         ToolPreviewKindGrep,
		Path:         path,
		Returned:     len(grepFiles),
		Matches:      matches,
		TotalMatches: totalMatches,
		NextOffset:   payload.NextOffset,
		OutputMode:   mode,
		Output:       payload.Output,
		GrepFiles:    grepFiles,
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

// countLegacyGrepMatchRows counts match rows in content output saved before
// the shown field existed. Match rows are "N:C: text" and context rows are
// "N: text", so a row counts when its prefix has a line and a column.
func countLegacyGrepMatchRows(output string) int {
	count := 0
	for _, line := range splitToolPreviewLines(output) {
		idx := strings.Index(line, ": ")
		if idx <= 0 {
			continue
		}
		parts := strings.Split(line[:idx], ":")
		if len(parts) != 2 {
			continue
		}
		if _, err := strconv.Atoi(parts[0]); err != nil {
			continue
		}
		if _, err := strconv.Atoi(parts[1]); err != nil {
			continue
		}
		count++
	}
	return count
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
