package tool

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

const defaultBashIngestionLimitBytes = 12288

var ansiEscapePattern = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]|\x1b\].*?(?:\x07|\x1b\\)`)

type bashIngestionResult struct {
	ExitCode  int    `json:"exit_code,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	Output    string `json:"output"`
}

// ShapeIngestedToolResult normalizes a tool result payload for prompt ingestion.
// The transformation is destructive by design and only applies to tool results
// whose output is known to be log-like rather than source content.
func ShapeIngestedToolResult(toolName, content string) string {
	switch toolName {
	case "bash":
		return shapeBashIngestedResult(content)
	default:
		return content
	}
}

type truncationStrategy string

const (
	tailPriorityStrategy truncationStrategy = "tail_priority"
	headStrategy         truncationStrategy = "head"
)

func shapeBashIngestedResult(content string) string {
	var structured bashIngestionResult
	if err := json.Unmarshal([]byte(content), &structured); err != nil || structured.Output == "" {
		return content
	}

	shaped, truncated, shown, total := shapeToolText(structured.Output, defaultBashIngestionLimitBytes, tailPriorityStrategy)
	structured.Output = shaped
	structured.Truncated = structured.Truncated || truncated
	if truncated {
		structured.Output = addTruncationMarker(structured.Output, shown, total, tailPriorityStrategy)
	}
	data, marshalErr := json.Marshal(structured)
	if marshalErr != nil {
		return content
	}
	return string(data)
}

func shapeToolText(text string, limit int, strategy truncationStrategy) (string, bool, int, int) {
	total := len(text)
	if limit < 0 {
		limit = 0
	}

	var (
		shaped    string
		truncated bool
	)

	switch strategy {
	case tailPriorityStrategy:
		shaped, truncated = tailPriorityText(text, limit)
	case headStrategy:
		shaped, truncated = headText(text, limit)
	default:
		shaped = text
	}

	shaped = stripToolNoise(shaped)
	return shaped, truncated, len(shaped), total
}

func tailPriorityText(text string, limit int) (string, bool) {
	if limit == 0 {
		if text == "" {
			return "", false
		}
		return "", true
	}
	if len(text) <= limit {
		return text, false
	}
	start := len(text) - limit
	for start < len(text) && !utf8.RuneStart(text[start]) {
		start++
	}
	if start >= len(text) {
		return "", true
	}
	return text[start:], true
}

func headText(text string, limit int) (string, bool) {
	if limit == 0 {
		if text == "" {
			return "", false
		}
		return "", true
	}
	if len(text) <= limit {
		return text, false
	}
	end := limit
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	if end <= 0 {
		return "", true
	}
	return text[:end], true
}

func addTruncationMarker(text string, shown, total int, strategy truncationStrategy) string {
	marker := TruncationMarker(shown, total)
	if text == "" {
		return marker
	}
	if strategy == tailPriorityStrategy {
		return marker + "\n" + text
	}
	return text + "\n" + marker
}

// TruncationMarker formats a truncation marker for tool output.
func TruncationMarker(shown, total int) string {
	return fmt.Sprintf("<truncated output shown=%d total=%d>", shown, total)
}

func stripToolNoise(text string) string {
	if text == "" {
		return ""
	}
	text = ansiEscapePattern.ReplaceAllString(text, "")
	return stripCarriageReturnOverwrites(text)
}

func stripCarriageReturnOverwrites(text string) string {
	if !strings.Contains(text, "\r") {
		return text
	}

	var out strings.Builder
	var line strings.Builder
	for _, r := range text {
		switch r {
		case '\r':
			line.Reset()
		case '\n':
			out.WriteString(line.String())
			out.WriteByte('\n')
			line.Reset()
		default:
			line.WriteRune(r)
		}
	}
	out.WriteString(line.String())
	return out.String()
}
