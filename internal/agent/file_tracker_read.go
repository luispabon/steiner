package agent

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
)

type readResult struct {
	Path       string `json:"path"`
	StartLine  int    `json:"start_line"`
	EndLine    int    `json:"end_line"`
	TotalLines int    `json:"total_lines"`
	Output     string `json:"output"`
}

func parseReadResult(content string) (readResult, bool) {
	var result readResult
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return readResult{}, false
	}
	return result, true
}

func (r readResult) rangeSummary() string {
	if r.EndLine > 0 && r.TotalLines > 0 {
		return fmt.Sprintf("lines %d-%d/%d", r.StartLine, r.EndLine, r.TotalLines)
	}
	if r.TotalLines > 0 {
		return fmt.Sprintf("%d lines", r.TotalLines)
	}
	return "unknown range"
}

func hashFileContent(path string) (uint64, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	h := fnv.New64a()
	_, _ = h.Write(data)
	return h.Sum64(), true
}

// RecordRead records a read result without changing its content.
func (t *FileTracker) RecordRead(turn int, content string) {
	result, ok := parseReadResult(content)
	if !ok {
		return
	}
	canonical, ok := normalizeTrackedPath(result.Path)
	if !ok {
		return
	}
	hash, ok := hashFileContent(canonical)
	if !ok {
		return
	}
	if t.reads == nil {
		t.reads = make(map[string]trackedFileRead)
	}
	t.reads[canonical] = trackedFileRead{
		Path: result.Path, Canonical: canonical, StartLine: result.StartLine,
		EndLine: result.EndLine, TotalLines: result.TotalLines, LastTurn: turn,
		ContentHash: hash, Generation: t.generations[canonical],
	}
}
