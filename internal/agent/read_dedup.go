package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/tool/builtin"
)

const fileUnchangedAnnotationPrefix = "[file unchanged since turn"

type readDedupOutcome struct {
	Action       string
	Reason       string
	PreviousTurn int
}

func dedupReadResult(content string, turn int, prior []Message) (string, readDedupOutcome) {
	_ = turn
	full := readDedupOutcome{Action: "full", Reason: "first read"}
	var result builtin.ReadResult
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		full.Reason = "no earlier full copy"
		return content, full
	}
	if result.FileHash == "" {
		full.Reason = "no file_hash"
		return content, full
	}
	if result.TotalLines == 0 {
		full.Reason = "no earlier full copy"
		return content, full
	}
	if strings.Contains(result.Output, builtin.LineTruncationMarker) {
		full.Reason = "line-capped read"
		return content, full
	}
	if strings.HasPrefix(result.Output, fileUnchangedAnnotationPrefix) {
		full.Reason = "no earlier full copy"
		return content, full
	}

	currentPath, ok := normalizeTrackedPath(result.Path)
	if !ok {
		return content, full
	}
	for _, message := range prior {
		if message.Role != MessageRoleTool || message.Name != "read" || message.Turn <= 0 {
			continue
		}
		var previous builtin.ReadResult
		if err := json.Unmarshal([]byte(message.Content), &previous); err != nil || previous.FileHash == "" || previous.TotalLines == 0 || strings.Contains(previous.Output, builtin.LineTruncationMarker) || strings.HasPrefix(previous.Output, fileUnchangedAnnotationPrefix) {
			continue
		}
		previousPath, ok := normalizeTrackedPath(previous.Path)
		if !ok || previousPath != currentPath || previous.StartLine != result.StartLine || previous.EndLine != result.EndLine || previous.TotalLines != result.TotalLines || previous.FileHash != result.FileHash {
			continue
		}
		result.Output = fmt.Sprintf("%s %d: lines %d-%d of %d in %s]", fileUnchangedAnnotationPrefix, message.Turn, result.StartLine, result.EndLine, result.TotalLines, result.Path)
		annotated, err := json.Marshal(result)
		if err != nil {
			return content, full
		}
		return string(annotated), readDedupOutcome{Action: "annotated", Reason: fmt.Sprintf("unchanged since turn %d", message.Turn), PreviousTurn: message.Turn}
	}
	if len(prior) > 0 {
		full.Reason = "no earlier full copy"
	}
	return content, full
}
