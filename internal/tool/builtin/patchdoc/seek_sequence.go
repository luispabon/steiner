package patchdoc

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// SeekSequence attempts to find pattern within lines beginning at or after start.
func SeekSequence(lines []string, pattern []string, start int, eof bool) (int, bool) {
	if len(pattern) == 0 {
		return start, true
	}
	if len(pattern) > len(lines) {
		return 0, false
	}

	searchStart, maxStart, ok := sequenceSearchBounds(lines, pattern, start, eof)
	if !ok {
		return 0, false
	}

	matchers := []func(string, string) bool{
		func(line, pat string) bool { return line == pat },
		func(line, pat string) bool { return trimRightSpace(line) == trimRightSpace(pat) },
		func(line, pat string) bool { return trimLeadingIndent(line) == trimLeadingIndent(pat) },
		func(line, pat string) bool { return strings.TrimSpace(line) == strings.TrimSpace(pat) },
		func(line, pat string) bool { return normaliseForPatchMatch(line) == normaliseForPatchMatch(pat) },
	}

	if idx, ok := seekSequenceWithMatchers(lines, pattern, searchStart, maxStart, matchers); ok {
		return idx, true
	}

	if idx, ok := seekSequenceFuzzy(lines, pattern, searchStart, maxStart); ok {
		return idx, true
	}

	return 0, false
}

func sequenceSearchBounds(lines []string, pattern []string, start int, eof bool) (int, int, bool) {
	searchStart := start
	if eof {
		searchStart = len(lines) - len(pattern)
	}
	if searchStart < 0 {
		searchStart = 0
	}

	maxStart := len(lines) - len(pattern)
	if searchStart > maxStart {
		return 0, 0, false
	}
	return searchStart, maxStart, true
}

func seekSequenceWithMatchers(lines []string, pattern []string, searchStart int, maxStart int, matchers []func(string, string) bool) (int, bool) {
	for _, match := range matchers {
		for i := searchStart; i <= maxStart; i++ {
			if sequenceMatches(lines, pattern, i, match) {
				return i, true
			}
		}
	}
	return 0, false
}

func sequenceMatches(lines []string, pattern []string, start int, match func(string, string) bool) bool {
	for j, pat := range pattern {
		if !match(lines[start+j], pat) {
			return false
		}
	}
	return true
}

func seekSequenceFuzzy(lines []string, pattern []string, searchStart int, maxStart int) (int, bool) {
	bestIndex := 0
	bestDistance := 0
	foundFuzzy := false
	for i := searchStart; i <= maxStart; i++ {
		totalDistance, ok := fuzzySequenceDistance(lines, pattern, i)
		if ok && (!foundFuzzy || totalDistance < bestDistance) {
			bestIndex = i
			bestDistance = totalDistance
			foundFuzzy = true
		}
	}
	if !foundFuzzy {
		return 0, false
	}
	return bestIndex, true
}

func fuzzySequenceDistance(lines []string, pattern []string, start int) (int, bool) {
	totalDistance := 0
	for j, pat := range pattern {
		dist := levenshtein(normaliseForPatchMatch(lines[start+j]), normaliseForPatchMatch(pat))
		if dist > fuzzyMatchThreshold(pat) {
			return 0, false
		}
		totalDistance += dist
	}
	return totalDistance, true
}

func trimRightSpace(s string) string {
	return strings.TrimRightFunc(s, unicode.IsSpace)
}

func trimLeadingIndent(s string) string {
	return strings.TrimLeft(s, " \t")
}

func normaliseForPatchMatch(s string) string {
	s = norm.NFC.String(s)
	s = strings.TrimSpace(s)
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '‐', '‑', '‒', '–', '—', '―', '−':
			b.WriteRune('-')
		case '‘', '’', '‚', '‛':
			b.WriteRune('\'')
		case '“', '”', '„', '‟':
			b.WriteRune('"')
		case ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', '　':
			b.WriteRune(' ')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func fuzzyMatchThreshold(pattern string) int {
	return max(1, len([]rune(pattern))/5)
}

func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	la, lb := len(ra), len(rb)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			if ra[i-1] == rb[j-1] {
				curr[j] = prev[j-1]
			} else {
				curr[j] = 1 + min(prev[j], min(curr[j-1], prev[j-1]))
			}
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}

// ClosestMatch holds the result of a closest-line search.
type ClosestMatch struct {
	LineIndex int
	Content   string
	Distance  int
}

// FindClosestLine searches lines[max(0,searchStart-window):min(len,searchStart+window)]
// for the line with the lowest Levenshtein distance to normaliseForPatchMatch(target).
// Returns (match, true) only if distance < len([]rune(target))/2.
func FindClosestLine(lines []string, target string, searchStart int, window int) (ClosestMatch, bool) {
	normTarget := normaliseForPatchMatch(target)
	runeLen := len([]rune(normTarget))
	if runeLen == 0 {
		return ClosestMatch{}, false
	}
	threshold := runeLen / 2
	if threshold == 0 {
		threshold = 1
	}

	lo := searchStart - window
	if lo < 0 {
		lo = 0
	}
	hi := searchStart + window
	if hi > len(lines) {
		hi = len(lines)
	}

	best := ClosestMatch{Distance: runeLen + 1}
	found := false
	for i := lo; i < hi; i++ {
		d := levenshtein(normaliseForPatchMatch(lines[i]), normTarget)
		if d < best.Distance {
			best = ClosestMatch{LineIndex: i, Content: lines[i], Distance: d}
			found = true
		}
	}
	if !found || best.Distance >= threshold {
		return ClosestMatch{}, false
	}
	return best, true
}
