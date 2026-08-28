package builtin

import (
	"bytes"
	"fmt"
	"strings"
	"unicode"
)

func buildNoMatchDiagnostics(prefix string, content []byte, oldText, absPath string) string {
	var lines []string
	lines = append(lines, fmt.Sprintf("%s: no match for old_string in %s", prefix, absPath))

	hasWhitespaceMismatch := normalizedWhitespaceMatchExists(content, oldText)
	if hasWhitespaceMismatch {
		lines = append(lines, fmt.Sprintf("%s: exact match failed; normalized whitespace match exists", prefix))

		if matchedText, _, ok := extractNormalizedMatch(content, oldText); ok {
			kind, details := classifyWhitespaceMismatch(oldText, matchedText)
			if kind != "" {
				lines = append(lines, fmt.Sprintf("%s: %s: %s", prefix, kind, details))
			}
			lines = append(lines, fmt.Sprintf("%s: file text that matches after whitespace normalization:", prefix))
			for _, l := range strings.Split(matchedText, "\n") {
				lines = append(lines, "  | "+l)
			}
		}
	}

	if anchorStart, anchorEnd, lineNum, preview, ok := findDiagnosticAnchor(content, oldText); ok {
		lines = append(lines, fmt.Sprintf("%s: nearest anchor at line %d, bytes %d-%d (matched fragment %q)", prefix, lineNum, anchorStart+1, anchorEnd, preview))
		lines = append(lines, fmt.Sprintf("%s: context:", prefix))
		lines = append(lines, previewContext(content, lineNum, preview)...)
	} else {
		lines = append(lines, fmt.Sprintf("%s: no nearby exact anchor found", prefix))
	}

	if hasWhitespaceMismatch {
		lines = append(lines, fmt.Sprintf("%s: suggestion: retry with old_string set to the file text shown above", prefix))
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

func classifyWhitespaceMismatch(oldText string, matchedRegion string) (kind string, details string) {
	oldLines := strings.Split(strings.TrimRight(oldText, "\n"), "\n")
	regionLines := strings.Split(strings.TrimRight(matchedRegion, "\n"), "\n")

	oldNonBlank := []string{}
	for _, l := range oldLines {
		if strings.TrimSpace(l) != "" {
			oldNonBlank = append(oldNonBlank, l)
		}
	}
	regionNonBlank := []string{}
	for _, l := range regionLines {
		if strings.TrimSpace(l) != "" {
			regionNonBlank = append(regionNonBlank, l)
		}
	}

	oldBlanks := countBlankLines(oldLines)
	regionBlanks := countBlankLines(regionLines)
	if len(oldNonBlank) != len(regionNonBlank) || oldBlanks != regionBlanks {
		return "blank-line-count", fmt.Sprintf("old_string has %d blank lines where the file has %d", oldBlanks, regionBlanks)
	}

	for i, ol := range oldNonBlank {
		if strings.TrimSpace(ol) != strings.TrimSpace(regionNonBlank[i]) {
			return "internal-whitespace", "the lines match except for spacing within them"
		}
	}

	deltas := make([]int, len(oldNonBlank))
	allZero := true
	for i, ol := range oldNonBlank {
		oldLead := leadingWhitespaceLength(ol)
		regionLead := leadingWhitespaceLength(regionNonBlank[i])
		deltas[i] = regionLead - oldLead
		if deltas[i] != 0 {
			allZero = false
		}
	}

	// A zero shift on every line, having already ruled out blank-line-count
	// and internal-whitespace differences, means indentation is not the
	// difference either — leave this unclassified rather than reporting a
	// misleading "shifted uniformly: 0".
	if allZero {
		return "", ""
	}

	uniform := true
	first := deltas[0]
	for _, d := range deltas[1:] {
		if d != first {
			uniform = false
			break
		}
	}

	deltasStr := formatIndentationDelta(deltas, oldNonBlank, regionNonBlank)
	if uniform {
		return "leading-indentation-uniform", "all lines shifted uniformly: " + deltasStr
	}
	return "leading-indentation-nonuniform", "lines shifted non-uniformly (model picture of nesting is inconsistent): " + deltasStr
}

func countBlankLines(lines []string) int {
	count := 0
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			count++
		}
	}
	return count
}

func leadingWhitespaceLength(line string) int {
	leading := strings.TrimLeft(line, " \t")
	return len(line) - len(leading)
}

func formatIndentationDelta(deltas []int, oldNonBlank, regionNonBlank []string) string {
	if len(deltas) == 0 {
		return ""
	}
	if len(deltas) == 1 {
		oldIndent := describeIndent(oldNonBlank[0])
		regionIndent := describeIndent(regionNonBlank[0])
		return fmt.Sprintf("line has %s, old_string has %s", regionIndent, oldIndent)
	}
	examples := []string{}
	for i := range deltas {
		if i >= 2 {
			break
		}
		oldIndent := describeIndent(oldNonBlank[i])
		regionIndent := describeIndent(regionNonBlank[i])
		examples = append(examples, fmt.Sprintf("line %d: file has %s, old_string has %s", i+1, regionIndent, oldIndent))
	}
	return strings.Join(examples, "; ")
}

func describeIndent(line string) string {
	s := strings.TrimLeft(line, " \t")
	indent := line[:len(line)-len(s)]
	tabs := strings.Count(indent, "\t")
	spaces := strings.Count(indent, " ")
	if tabs > 0 && spaces > 0 {
		return fmt.Sprintf("%d tabs + %d spaces", tabs, spaces)
	}
	if tabs > 0 {
		return fmt.Sprintf("%d %s", tabs, pluralize("tab", tabs))
	}
	return fmt.Sprintf("%d %s", spaces, pluralize("space", spaces))
}

func pluralize(s string, n int) string {
	if n == 1 {
		return s
	}
	return s + "s"
}

func isStructuralOnlyCandidate(candidate string) bool {
	fields := strings.Fields(candidate)
	if len(fields) == 1 && strings.HasSuffix(candidate, ":") {
		return true
	}
	for _, r := range candidate {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// anchorSurvivor is a diagnostic-anchor candidate that occurs in the file
// within the max-occurrence threshold applied by collectAnchorSurvivors.
type anchorSurvivor struct {
	candidate string
	idx       int
	count     int
}

// maxAnchorOccurrences rejects candidates occurring more than this many times
// in the file — beyond this, a candidate is common enough to be noise rather
// than a trustworthy anchor (e.g. a repeated structural token).
const maxAnchorOccurrences = 3

// anchorProximityWindow is the byte distance within which another candidate's
// match counts as corroborating evidence for the cluster-score tiebreaker.
const anchorProximityWindow = 300

func collectAnchorSurvivors(content []byte, candidates []string) []anchorSurvivor {
	var survivors []anchorSurvivor
	for _, candidate := range candidates {
		idx := bytes.Index(content, []byte(candidate))
		if idx < 0 {
			continue
		}
		count := bytes.Count(content, []byte(candidate))
		if count > maxAnchorOccurrences {
			continue
		}
		survivors = append(survivors, anchorSurvivor{candidate: candidate, idx: idx, count: count})
	}
	return survivors
}

// anchorClusterScore counts how many other survivors' matches fall within
// anchorProximityWindow bytes of target's match — corroborating evidence that
// this location, not just this candidate, is the right one.
func anchorClusterScore(target anchorSurvivor, all []anchorSurvivor) int {
	score := 0
	for _, other := range all {
		if other.candidate == target.candidate {
			continue
		}
		if other.idx-target.idx >= -anchorProximityWindow && other.idx-target.idx <= anchorProximityWindow {
			score++
		}
	}
	return score
}

// anchorSurvivorBetter reports whether s should replace best as the winning
// anchor, ranking by occurrence count (fewer wins), then cluster score
// (higher wins), then candidate length (longer wins), then earliest match.
func anchorSurvivorBetter(s, best anchorSurvivor, clusterS, clusterBest int) bool {
	if s.count != best.count {
		return s.count < best.count
	}
	if clusterS != clusterBest {
		return clusterS > clusterBest
	}
	if len(s.candidate) != len(best.candidate) {
		return len(s.candidate) > len(best.candidate)
	}
	return s.idx < best.idx
}

func findDiagnosticAnchor(content []byte, oldText string) (int, int, int, string, bool) {
	candidates := diagnosticAnchorCandidates(oldText)
	survivors := collectAnchorSurvivors(content, candidates)
	if len(survivors) == 0 {
		return 0, 0, 0, "", false
	}

	best := survivors[0]
	for _, s := range survivors[1:] {
		clusterBest := anchorClusterScore(best, survivors)
		clusterS := anchorClusterScore(s, survivors)
		if anchorSurvivorBetter(s, best, clusterS, clusterBest) {
			best = s
		}
	}

	return best.idx, best.idx + len(best.candidate), lineNumberAt(content, best.idx), best.candidate, true
}

func diagnosticAnchorCandidates(oldText string) []string {
	seen := make(map[string]struct{})
	var candidates []string

	add := func(candidate string) {
		candidate = strings.TrimSpace(candidate)
		if len(candidate) < 4 {
			return
		}
		if isStructuralOnlyCandidate(candidate) {
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
