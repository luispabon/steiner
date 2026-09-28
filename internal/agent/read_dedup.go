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

func dedupReadResult(content string, _ int, prior []Message) (string, readDedupOutcome) {
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
	if result.TotalLines == 0 || strings.HasPrefix(result.Output, fileUnchangedAnnotationPrefix) {
		full.Reason = "no earlier full copy"
		return content, full
	}
	if strings.Contains(result.Output, builtin.LineTruncationMarker) {
		full.Reason = "line-capped read"
		return content, full
	}
	currentPath, ok := normalizeTrackedPath(result.Path)
	if !ok {
		return content, full
	}
	for _, message := range prior {
		if !matchesPriorRead(message, result, currentPath) {
			continue
		}
		return annotateReadResult(content, result, message, full)
	}
	if len(prior) > 0 {
		full.Reason = "no earlier full copy"
	}
	return content, full
}

func matchesPriorRead(message Message, result builtin.ReadResult, currentPath string) bool {
	if message.Role != MessageRoleTool || message.Name != "read" || message.Turn <= 0 {
		return false
	}
	var previous builtin.ReadResult
	if err := json.Unmarshal([]byte(message.Content), &previous); err != nil || !eligiblePriorRead(previous) {
		return false
	}
	previousPath, ok := normalizeTrackedPath(previous.Path)
	return ok && previousPath == currentPath && previous.StartLine == result.StartLine && previous.EndLine == result.EndLine && previous.TotalLines == result.TotalLines && previous.FileHash == result.FileHash
}

func eligiblePriorRead(result builtin.ReadResult) bool {
	return result.FileHash != "" && result.TotalLines != 0 && !strings.Contains(result.Output, builtin.LineTruncationMarker) && !strings.HasPrefix(result.Output, fileUnchangedAnnotationPrefix)
}

func annotateReadResult(content string, result builtin.ReadResult, message Message, fallback readDedupOutcome) (string, readDedupOutcome) {
	result.Output = fmt.Sprintf("%s %d: lines %d-%d of %d in %s]", fileUnchangedAnnotationPrefix, message.Turn, result.StartLine, result.EndLine, result.TotalLines, result.Path)
	annotated, err := json.Marshal(result)
	if err != nil {
		return content, fallback
	}
	return string(annotated), readDedupOutcome{Action: "annotated", Reason: fmt.Sprintf("unchanged since turn %d", message.Turn), PreviousTurn: message.Turn}
}
