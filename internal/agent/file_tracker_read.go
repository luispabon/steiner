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
	switch {
	case r.EndLine > 0 && r.TotalLines > 0:
		return fmt.Sprintf("lines %d-%d/%d", r.StartLine, r.EndLine, r.TotalLines)
	case r.TotalLines > 0:
		return fmt.Sprintf("%d lines", r.TotalLines)
	default:
		return "unknown range"
	}
}

func hashFileContent(path string) (uint64, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	h := fnv.New64a()
	h.Write(data)
	return h.Sum64(), true
}
