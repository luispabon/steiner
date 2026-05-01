package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

type trackedFileRead struct {
	Path       string
	StartLine  int
	EndLine    int
	TotalLines int
	LastTurn   int
	ModTime    time.Time
}

type readResult struct {
	Path       string
	StartLine  int
	EndLine    int
	TotalLines int
	Output     string
}

type FileTracker struct {
	reads map[string]trackedFileRead
}

func (t *FileTracker) ObserveRead(turn int, content string, annotationsEnabled bool) string {
	result, ok := parseReadResult(content)
	if !ok {
		return content
	}
	path := strings.TrimSpace(result.Path)
	if path == "" {
		return content
	}
	info, err := os.Stat(path)
	if err != nil {
		return content
	}
	if t.reads == nil {
		t.reads = make(map[string]trackedFileRead)
	}

	next := trackedFileRead{
		Path:       path,
		StartLine:  result.StartLine,
		EndLine:    result.EndLine,
		TotalLines: result.TotalLines,
		LastTurn:   turn,
		ModTime:    info.ModTime(),
	}
	previous, ok := t.reads[path]
	t.reads[path] = next
	if !annotationsEnabled || !ok {
		return content
	}
	if !previous.ModTime.Equal(next.ModTime) {
		return content
	}
	if previous.StartLine != next.StartLine || previous.EndLine != next.EndLine || previous.TotalLines != next.TotalLines {
		return content
	}

	result.Output = fileUnchangedAnnotation(previous)
	data, err := json.Marshal(result)
	if err != nil {
		return content
	}
	return string(data)
}

func parseReadResult(content string) (readResult, bool) {
	var result readResult
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return readResult{}, false
	}
	return result, true
}

func (r readResult) rangeSummary() string {
	switch {
	case r.EndLine > 0 && r.TotalLines > 0:
		return fmt.Sprintf("lines %d-%d/%d", r.StartLine, r.EndLine, r.TotalLines)
	case r.TotalLines > 0:
		return fmt.Sprintf("%d lines", r.TotalLines)
	default:
		return "unknown range"
	}
}

func (t *FileTracker) Clone() FileTracker {
	if len(t.reads) == 0 {
		return FileTracker{}
	}
	out := FileTracker{reads: make(map[string]trackedFileRead, len(t.reads))}
	for path, read := range t.reads {
		out.reads[path] = read
	}
	return out
}

func (t *FileTracker) Summaries(limit int) []string {
	if len(t.reads) == 0 || limit <= 0 {
		return nil
	}
	out := make([]string, 0, len(t.reads))
	for _, read := range t.reads {
		out = append(out, fmt.Sprintf("%s lines %d-%d/%d", read.Path, read.StartLine, read.EndLine, read.TotalLines))
		if len(out) == limit {
			break
		}
	}
	return out
}

func fileUnchangedAnnotation(read trackedFileRead) string {
	rangeSummary := fmt.Sprintf("lines %d-%d of %d", read.StartLine, read.EndLine, read.TotalLines)
	if read.EndLine == 0 {
		rangeSummary = fmt.Sprintf("empty file with %d lines", read.TotalLines)
	}
	return fmt.Sprintf("[file unchanged since turn %d: %s in %s]", read.LastTurn, rangeSummary, read.Path)
}
