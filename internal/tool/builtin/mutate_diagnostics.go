package builtin

import (
	"bytes"
	"fmt"
	"strings"
)

func buildNoMatchDiagnostics(prefix string, content []byte, oldText, absPath string) string {
	var lines []string
	lines = append(lines, fmt.Sprintf("%s: no match for old_string in %s", prefix, absPath))

	hasWhitespaceMismatch := normalizedWhitespaceMatchExists(content, oldText)
	var matchLineNum int
	if hasWhitespaceMismatch {
		lines = append(lines, fmt.Sprintf("%s: exact match failed; normalized whitespace match exists", prefix))
		if matchedText, mln, ok := extractNormalizedMatch(content, oldText); ok {
			matchLineNum = mln
			lines = append(lines, fmt.Sprintf("%s: file text that matches after whitespace normalization:", prefix))
			for _, l := range strings.Split(matchedText, "\n") {
				lines = append(lines, "  | "+l)
			}
		}
	}

	anchorLineNum := 0
	if anchorStart, anchorEnd, lineNum, preview, ok := findDiagnosticAnchor(content, oldText); ok {
		anchorLineNum = lineNum
		lines = append(lines, fmt.Sprintf("%s: nearest anchor at line %d, bytes %d-%d", prefix, lineNum, anchorStart+1, anchorEnd))
		lines = append(lines, fmt.Sprintf("%s: context:", prefix))
		lines = append(lines, previewContext(content, lineNum, preview)...)
	} else {
		lines = append(lines, fmt.Sprintf("%s: no nearby exact anchor found", prefix))
	}

	if hasWhitespaceMismatch {
		suggestLine := anchorLineNum
		if suggestLine == 0 {
			suggestLine = matchLineNum
		}
		if suggestLine > 0 {
			lines = append(lines, fmt.Sprintf("%s: suggestion: retry with old_string set to the file text shown above, or use line_replace with line %d", prefix, suggestLine))
		} else {
			lines = append(lines, fmt.Sprintf("%s: suggestion: retry with old_string set to the file text shown above", prefix))
		}
	} else {
		lines = append(lines, fmt.Sprintf("%s: suggestion: reread a slightly wider region around the target text", prefix))
	}
	return strings.Join(lines, "\n")
}

func buildAmbiguousDiagnostics(prefix string, content []byte, oldText string, matchCount int, absPath string) string {
	var lines []string
	lines = append(lines, fmt.Sprintf("%s: ambiguous match for old_string in %s (found %d occurrences)", prefix, absPath, matchCount))

	if matchStart, matchEnd, lineNum, preview, ok := findExactMatchPreview(content, oldText); ok {
		lines = append(lines, fmt.Sprintf("%s: closest occurrence at line %d, bytes %d-%d", prefix, lineNum, matchStart+1, matchEnd))
		lines = append(lines, fmt.Sprintf("%s: context:", prefix))
		lines = append(lines, previewContext(content, lineNum, preview)...)
	}

	lines = append(lines, fmt.Sprintf("%s: suggestion: reread a slightly wider region around the target text", prefix))
	return strings.Join(lines, "\n")
}

// buildLineReplaceMismatchDiagnostics produces a line-range-scoped diagnostic for
// line_replace operations whose old_string guard did not match exactly once in
// the target line(s). The prefix should already include the
// "mutate: operation N line_replace" form so callers that parse the error shape
// keep working. rangeText is the actual line text (with any preserved line
// endings) covering lines startLine..endLine; pass it in so the preview shows
// the real file bytes, including tabs vs spaces. singleLine selects the
// "line N contains old_string M times" wording versus the range wording. absPath
// is the absolute file path the planner resolved; it is included on the first
// header line so a "wrong file" diagnosis is immediate.
func buildLineReplaceMismatchDiagnostics(prefix string, oldText string, count, startLine, endLine int, rangeText string, singleLine bool, absPath string) string {
	var lines []string
	if singleLine {
		lines = append(lines, fmt.Sprintf("%s: line %d in %s contains old_string %d times", prefix, startLine, absPath, count))
	} else {
		lines = append(lines, fmt.Sprintf("%s: old_string found %d times in lines %d–%d of %s (want exactly 1)", prefix, count, startLine, endLine, absPath))
	}

	lines = append(lines, fmt.Sprintf("%s: target line(s) in file:", prefix))
	for _, l := range strings.Split(strings.TrimRight(rangeText, "\n"), "\n") {
		lines = append(lines, "  | "+l)
	}

	switch {
	case normalizedWhitespaceMatchExists([]byte(rangeText), oldText):
		lines = append(lines, fmt.Sprintf("%s: exact match failed; normalized whitespace match exists", prefix))
		if matchedText, _, ok := extractNormalizedMatch([]byte(rangeText), oldText); ok {
			lines = append(lines, fmt.Sprintf("%s: file text that matches after whitespace normalization:", prefix))
			for _, l := range strings.Split(matchedText, "\n") {
				lines = append(lines, "  | "+l)
			}
		}
		lines = append(lines, fmt.Sprintf("%s: suggestion: retry with old_string set to the exact target line text (tabs vs spaces must match the file)", prefix))
	case singleLine:
		lines = append(lines, fmt.Sprintf("%s: suggestion: reread line %d to confirm the current text before retrying", prefix, startLine))
	default:
		lines = append(lines, fmt.Sprintf("%s: suggestion: reread lines %d–%d to confirm the current text before retrying", prefix, startLine, endLine))
	}

	return strings.Join(lines, "\n")
}

func normalizedWhitespaceMatchExists(content []byte, oldText string) bool {
	oldNorm := strings.Join(strings.Fields(oldText), " ")
	if oldNorm == "" {
		return false
	}
	contentNorm := strings.Join(strings.Fields(string(content)), " ")
	return strings.Contains(contentNorm, oldNorm)
}

func extractNormalizedMatch(content []byte, oldText string) (matchedText string, lineNum int, ok bool) {
	oldNorm := strings.Join(strings.Fields(oldText), " ")
	if oldNorm == "" {
		return "", 0, false
	}
	oldLineCount := len(strings.Split(strings.TrimRight(oldText, "\n"), "\n"))

	fileLines := strings.Split(strings.ReplaceAll(string(content), "\r\n", "\n"), "\n")
	for start := 0; start+oldLineCount <= len(fileLines); start++ {
		window := fileLines[start : start+oldLineCount]
		windowNorm := strings.Join(strings.Fields(strings.Join(window, "\n")), " ")
		if windowNorm == oldNorm {
			return strings.Join(window, "\n"), start + 1, true
		}
	}
	return "", 0, false
}

func findDiagnosticAnchor(content []byte, oldText string) (int, int, int, string, bool) {
	candidates := diagnosticAnchorCandidates(oldText)
	bestStart := -1
	bestEnd := -1
	bestLine := 0
	bestPreview := ""
	bestLen := 0

	for _, candidate := range candidates {
		idx := bytes.Index(content, []byte(candidate))
		if idx < 0 {
			continue
		}
		if len(candidate) > bestLen || (len(candidate) == bestLen && (bestStart < 0 || idx < bestStart)) {
			bestStart = idx
			bestEnd = idx + len(candidate)
			bestLine = lineNumberAt(content, idx)
			bestPreview = candidate
			bestLen = len(candidate)
		}
	}

	if bestStart < 0 {
		return 0, 0, 0, "", false
	}

	return bestStart, bestEnd, bestLine, bestPreview, true
}

func diagnosticAnchorCandidates(oldText string) []string {
	seen := make(map[string]struct{})
	var candidates []string

	add := func(candidate string) {
		candidate = strings.TrimSpace(candidate)
		if len(candidate) < 4 {
			return
		}
		if _, ok := seen[candidate]; ok {
			return
		}
		seen[candidate] = struct{}{}
		candidates = append(candidates, candidate)
	}

	for _, line := range strings.Split(oldText, "\n") {
		add(line)

		fields := strings.Fields(line)
		for span := 2; span <= 4 && span <= len(fields); span++ {
			for start := 0; start+span <= len(fields); start++ {
				add(strings.Join(fields[start:start+span], " "))
			}
		}
	}

	return candidates
}

func findExactMatchPreview(content []byte, oldText string) (int, int, int, string, bool) {
	idx := bytes.Index(content, []byte(oldText))
	if idx < 0 {
		return 0, 0, 0, "", false
	}
	return idx, idx + len(oldText), lineNumberAt(content, idx), oldText, true
}

func lineNumberAt(content []byte, offset int) int {
	if offset < 0 {
		offset = 0
	}
	if offset > len(content) {
		offset = len(content)
	}
	line := 1
	for i := 0; i < offset; i++ {
		if content[i] == '\n' {
			line++
		}
	}
	return line
}

func previewContext(content []byte, lineNum int, anchor string) []string {
	lines := strings.Split(strings.ReplaceAll(string(content), "\r\n", "\n"), "\n")
	if len(lines) == 0 {
		return []string{fmt.Sprintf("  1 | %s", truncatePreviewLine(anchor, 120))}
	}

	start := lineNum - 1
	if start < 1 {
		start = 1
	}
	end := lineNum + 1
	if end > len(lines) {
		end = len(lines)
	}

	preview := make([]string, 0, end-start+1)
	for i := start; i <= end; i++ {
		lineText := strings.TrimRight(lines[i-1], "\r")
		if i == lineNum && lineText == "" && anchor != "" {
			lineText = anchor
		}
		prefix := "  "
		if i == lineNum {
			prefix = "> "
		}
		preview = append(preview, fmt.Sprintf("%s%4d | %s", prefix, i, truncatePreviewLine(lineText, 120)))
	}
	return preview
}

func truncatePreviewLine(s string, limit int) string {
	if limit <= 0 || len(s) <= limit {
		return s
	}
	if limit <= 3 {
		return s[:limit]
	}
	return s[:limit-3] + "..."
}
