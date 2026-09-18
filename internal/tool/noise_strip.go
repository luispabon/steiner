package tool

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	defaultBashIngestionLimitBytes = 12288
	defaultGrepIngestionLimitLines = 200
)

var ansiEscapePattern = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]|\x1b\].*?(?:\x07|\x1b\\)`)

type bashIngestionResult struct {
	ExitCode  int    `json:"exit_code,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	Output    string `json:"output"`
}

type grepIngestionResult struct {
	Matches    int    `json:"matches,omitempty"`
	NextOffset int    `json:"next_offset,omitempty"`
	Output     string `json:"output"`
}

// ShapeIngestedToolResult normalizes a tool result payload for prompt ingestion.
// The transformation is destructive by design and only applies to tool results
// whose output is known to be log-like rather than source content.
func ShapeIngestedToolResult(toolName, content string) string {
	switch toolName {
	case "bash":
		return shapeBashIngestedResult(content)
	case "grep":
		return shapeGrepIngestedResult(content)
	default:
		return content
	}
}

type truncationStrategy string

const (
	tailPriorityStrategy truncationStrategy = "tail_priority"
	countCapStrategy     truncationStrategy = "count_cap"
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

func shapeGrepIngestedResult(content string) string {
	var structured grepIngestionResult
	if err := json.Unmarshal([]byte(content), &structured); err != nil || structured.Output == "" {
		return content
	}

	shaped, truncated, shown, total := shapeToolText(structured.Output, defaultGrepIngestionLimitLines, countCapStrategy)
	structured.Output = shaped
	if truncated {
		structured.Output = addTruncationMarker(structured.Output, shown, total, countCapStrategy)
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
	case countCapStrategy:
		shaped, truncated = countCapText(text, limit)
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

func countCapText(text string, limit int) (string, bool) {
	if text == "" {
		return "", false
	}
	lines := splitToolLines(text)
	if len(lines) <= limit {
		return text, false
	}
	if limit <= 0 {
		return "", true
	}
	return strings.Join(lines[:limit], "\n"), true
}

func splitToolLines(text string) []string {
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
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
	text = stripCarriageReturnOverwrites(text)
	text = stripProgressLines(text)
	return collapseToolLines(text)
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

func collapseToolLines(text string) string {
	lines := strings.Split(text, "\n")
	if len(lines) == 0 {
		return ""
	}

	var out []string
	for i := 0; i < len(lines); {
		line := lines[i]
		count := 1
		for i+count < len(lines) && lines[i+count] == line {
			count++
		}

		if isBlankToolLine(line) {
			if len(out) == 0 || out[len(out)-1] != "" {
				out = append(out, "")
			}
			i += count
			continue
		}

		if count > 1 && isWarningOrInfoLine(line) {
			out = append(out, fmt.Sprintf("%s (repeated %dx)", line, count))
		} else {
			out = append(out, repeatToolLine(line, count)...)
		}
		i += count
	}

	return strings.Join(out, "\n")
}

func repeatToolLine(line string, count int) []string {
	if count <= 1 {
		return []string{line}
	}
	out := make([]string, 0, count)
	for i := 0; i < count; i++ {
		out = append(out, line)
	}
	return out
}

func isBlankToolLine(line string) bool {
	return strings.TrimSpace(line) == ""
}

func isWarningOrInfoLine(line string) bool {
	trimmed := strings.TrimSpace(strings.ToLower(line))
	return strings.HasPrefix(trimmed, "warning:") ||
		strings.HasPrefix(trimmed, "warning ") ||
		strings.HasPrefix(trimmed, "warn:") ||
		strings.HasPrefix(trimmed, "warn ") ||
		strings.HasPrefix(trimmed, "info:") ||
		strings.HasPrefix(trimmed, "info ")
}

var (
	// progressBarLine matches lines that are purely progress bar visualisation:
	//   [########>       ]  or  [====>      ] 45%
	progressBarLine = regexp.MustCompile(`^\s*\[[= \.\#>\-]+\](?:\s*\d+\.?\d*%)?\s*$`)

	// purePercentageLine matches lines that are just a percentage value:
	//   45%  or  45.5%
	purePercentageLine = regexp.MustCompile(`^\s*\d+\.?\d*\s*%\s*$`)

	// progressKeywordLine matches lines with common progress/download/install
	// keywords followed by a percentage:
	//   Downloading 45%  |  Receiving objects: 67%  |  Extracting package (32%)
	progressKeywordLine = regexp.MustCompile(`(?i)^\s*(?:download|upload|install|extract|fetch|receiv|transfer|progress|checking|resolving).*\d+\.?\d*\s*%`)

	// spinnerLine matches lines that are just a spinner character optionally
	// followed by whitespace and/or a short label:
	//   ⠋  |  -  |  \  |  |
	spinnerLine = regexp.MustCompile(`^\s*[-\|/\\⠁-⣿](?:\s+\S+)?\s*$`)
)

// stripProgressLines removes lines that represent transient progress output:
// progress bars, percentage-only lines, download progress lines, and spinner
// characters. These are never useful to preserve in ingested tool output.
func stripProgressLines(text string) string {
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if progressBarLine.MatchString(line) ||
			purePercentageLine.MatchString(line) ||
			progressKeywordLine.MatchString(line) ||
			spinnerLine.MatchString(line) {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
