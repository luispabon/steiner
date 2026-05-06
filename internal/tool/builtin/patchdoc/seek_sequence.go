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

	searchStart := start
	if eof {
		searchStart = len(lines) - len(pattern)
	}
	if searchStart < 0 {
		searchStart = 0
	}

	maxStart := len(lines) - len(pattern)
	if searchStart > maxStart {
		return 0, false
	}

	matchers := []func(string, string) bool{
		func(line, pat string) bool { return line == pat },
		func(line, pat string) bool { return trimRightSpace(line) == trimRightSpace(pat) },
		func(line, pat string) bool { return strings.TrimSpace(line) == strings.TrimSpace(pat) },
		func(line, pat string) bool { return normaliseForPatchMatch(line) == normaliseForPatchMatch(pat) },
	}

	for _, match := range matchers {
		for i := searchStart; i <= maxStart; i++ {
			ok := true
			for j, pat := range pattern {
				if !match(lines[i+j], pat) {
					ok = false
					break
				}
			}
			if ok {
				return i, true
			}
		}
	}

	return 0, false
}

func trimRightSpace(s string) string {
	return strings.TrimRightFunc(s, unicode.IsSpace)
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
