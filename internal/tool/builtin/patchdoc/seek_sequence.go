package patchdoc

import (
	"strings"
	"unicode"
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
	s = strings.TrimSpace(s)
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '\u2010', '\u2011', '\u2012', '\u2013', '\u2014', '\u2015', '\u2212':
			b.WriteRune('-')
		case '\u2018', '\u2019', '\u201A', '\u201B':
			b.WriteRune('\'')
		case '\u201C', '\u201D', '\u201E', '\u201F':
			b.WriteRune('"')
		case '\u00A0', '\u2002', '\u2003', '\u2004', '\u2005', '\u2006', '\u2007', '\u2008', '\u2009', '\u200A', '\u202F', '\u205F', '\u3000':
			b.WriteRune(' ')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
