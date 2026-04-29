package output

import (
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	ToolPreviewKindEditDiff  = "edit_diff"
	ToolPreviewKindFileWrite = "file_write"
	ToolPreviewKindReadFile  = "read_file"
	ToolPreviewKindGlobList  = "glob_list"
	ToolPreviewKindLSList    = "ls_list"
	ToolPreviewKindGrep      = "grep"
	ToolPreviewKindBash      = "bash"
	ToolPreviewKindPlain     = "plain"
)

type ToolPreviewListEntry struct {
	Path  string
	IsDir bool
}

type ToolPreviewGrepMatch struct {
	LineNumber int
	Text       string
}

type ToolPreviewGrepFile struct {
	Path    string
	Count   int
	Matches []ToolPreviewGrepMatch
}

type ToolPreview struct {
	Kind       string
	Path       string
	Language   string
	Before     string
	After      string
	Contents   string
	Created    bool
	Command    string
	Output     string
	Message    string
	ExitCode   int
	Truncated  bool
	Returned   int
	NextOffset int
	OutputMode string
	Entries    []ToolPreviewListEntry
	GrepFiles  []ToolPreviewGrepFile
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
	case "glob":
		return buildGlobPreview(arguments, result)
	case "ls":
		return buildLSPreview(arguments, result)
	case "grep":
		return buildGrepPreview(arguments, result)
	case "bash":
		return buildBashPreview(arguments, result)
	default:
		return plainToolPreview()
	}
}

func plainToolPreview() ToolPreview {
	return ToolPreview{Kind: ToolPreviewKindPlain}
}

func buildGlobPreview(arguments map[string]any, result string) ToolPreview {
	var payload struct {
		Output     string `json:"output"`
		Returned   int    `json:"returned"`
		Truncated  bool   `json:"truncated,omitempty"`
		NextOffset int    `json:"next_offset,omitempty"`
	}
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		return plainToolPreview()
	}

	path := trimmedStringArg(arguments, "path")
	if path == "" {
		path = "."
	}
	entries := previewListEntries(payload.Output)
	return ToolPreview{
		Kind:       ToolPreviewKindGlobList,
		Path:       path,
		Returned:   payload.Returned,
		NextOffset: payload.NextOffset,
		Truncated:  payload.Truncated,
		Entries:    entries,
	}
}

func buildLSPreview(arguments map[string]any, result string) ToolPreview {
	var payload struct {
		Output     string `json:"output"`
		Returned   int    `json:"returned"`
		Truncated  bool   `json:"truncated,omitempty"`
		NextOffset int    `json:"next_offset,omitempty"`
	}
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		return plainToolPreview()
	}

	path := trimmedStringArg(arguments, "path")
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
		Returned:   payload.Returned,
		NextOffset: payload.NextOffset,
		Truncated:  payload.Truncated,
		Entries:    entries,
	}
}

func buildGrepPreview(arguments map[string]any, result string) ToolPreview {
	var payload struct {
		Matches    int    `json:"matches"`
		Returned   int    `json:"returned"`
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
	path := trimmedStringArg(arguments, "path")
	if path == "" {
		path = "."
	}

	preview := ToolPreview{
		Kind:       ToolPreviewKindGrep,
		Path:       path,
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

func buildBashPreview(arguments map[string]any, result string) ToolPreview {
	var payload struct {
		ExitCode  int    `json:"exit_code"`
		Truncated bool   `json:"truncated,omitempty"`
		Output    string `json:"output"`
		Message   string `json:"message,omitempty"`
	}
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		return plainToolPreview()
	}

	return ToolPreview{
		Kind:      ToolPreviewKindBash,
		Command:   rawStringArg(arguments, "command"),
		ExitCode:  payload.ExitCode,
		Truncated: payload.Truncated,
		Output:    payload.Output,
		Message:   payload.Message,
	}
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

func previewListEntries(output string) []ToolPreviewListEntry {
	lines := splitToolPreviewLines(output)
	entries := make([]ToolPreviewListEntry, 0, len(lines))
	for _, line := range lines {
		entries = append(entries, ToolPreviewListEntry{Path: line})
	}
	return entries
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
