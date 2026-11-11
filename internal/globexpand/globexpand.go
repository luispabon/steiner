// Package globexpand expands the brace alternations and zero-directory
// doublestar segments shared by the glob tool, grep's file_glob, and workspace
// exclusion patterns.
//
// Expansion runs before gobwas/glob compiles a pattern. That keeps a malformed
// brace an ordinary error: gobwas compiles an empty brace group such as "aa{}"
// but the resulting matcher slices past its input and panics.
package globexpand

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// MaxExpansions caps the number of patterns produced by brace and
// zero-directory doublestar expansion, so a pathological pattern cannot
// multiply into an unbounded matcher set.
const MaxExpansions = 256

// ErrTooComplex reports a pattern whose brace and zero-directory expansion
// exceeds MaxExpansions.
var ErrTooComplex = fmt.Errorf("pattern expands beyond %d forms", MaxExpansions)

// ErrUnmatchedBrace reports a pattern whose braces do not pair up once escapes
// and character classes are excluded. Callers surface it instead of compiling
// the brace as literal text.
var ErrUnmatchedBrace = errors.New(`unmatched brace in pattern; escape literal braces as \{ or \}`)

// ValidateBraces reports ErrUnmatchedBrace when the pattern holds an unescaped
// brace outside a character class with no counterpart. It runs before any
// expansion, so the forms later compiled by gobwas hold only escaped braces and
// braces inside character classes; matched groups are expanded away.
func ValidateBraces(pattern string) error {
	depth := 0
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '\\':
			i++
		case '[':
			i = SkipClass(pattern, i)
		case '{':
			depth++
		case '}':
			if depth == 0 {
				return ErrUnmatchedBrace
			}
			depth--
		}
	}
	if depth > 0 {
		return ErrUnmatchedBrace
	}
	return nil
}

// ExpandBraces rewrites every brace alternation, splitting on top-level commas
// while leaving escaped braces and braces inside character classes alone. It
// validates the pattern once up front, so an unmatched brace is an error and the
// recursion below never rescans a pattern that substitution kept balanced.
func ExpandBraces(pattern string) ([]string, error) {
	if err := ValidateBraces(pattern); err != nil {
		return nil, err
	}
	return expandBracesBalanced(pattern)
}

// expandBracesBalanced rewrites the first brace alternation, then recurses into
// each alternative.
func expandBracesBalanced(pattern string) ([]string, error) {
	open, closing, alternatives, ok := splitBraces(pattern)
	if !ok {
		return []string{pattern}, nil
	}
	var expanded []string
	for _, alternative := range alternatives {
		sub, err := expandBracesBalanced(pattern[:open] + alternative + pattern[closing+1:])
		if err != nil {
			return nil, err
		}
		if len(expanded)+len(sub) > MaxExpansions {
			return nil, ErrTooComplex
		}
		expanded = append(expanded, sub...)
	}
	return expanded, nil
}

// splitBraces locates the first brace alternation and splits its top-level
// alternatives. It reports false when the pattern holds no unescaped brace group
// outside a character class, in which case the pattern is left for gobwas/glob
// to parse.
func splitBraces(pattern string) (int, int, []string, bool) {
	open := -1
	for i := 0; i < len(pattern) && open < 0; i++ {
		switch pattern[i] {
		case '\\':
			i++
		case '[':
			i = SkipClass(pattern, i)
		case '{':
			open = i
		}
	}
	if open < 0 {
		return 0, 0, nil, false
	}

	var alternatives []string
	depth := 0
	start := open + 1
	for i := open + 1; i < len(pattern); i++ {
		switch pattern[i] {
		case '\\':
			i++
		case '[':
			i = SkipClass(pattern, i)
		case '{':
			depth++
		case '}':
			if depth == 0 {
				return open, i, append(alternatives, pattern[start:i]), true
			}
			depth--
		case ',':
			if depth == 0 {
				alternatives = append(alternatives, pattern[start:i])
				start = i + 1
			}
		}
	}
	return 0, 0, nil, false
}

// ExpandZeroDir returns every pattern produced by removing any subset of the
// standalone "**/" segments, so each of them matches zero directories as well
// as one or more. Removing only a prefix of the segments is not enough:
// gobwas/glob cannot elide an inner "**/" once an earlier segment is consumed.
func ExpandZeroDir(pattern string) ([]string, error) {
	forms := map[string]struct{}{pattern: {}}
	queue := []string{pattern}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, offset := range zeroDirOffsets(current) {
			next := current[:offset] + current[offset+len("**/"):]
			if _, seen := forms[next]; seen {
				continue
			}
			forms[next] = struct{}{}
			if len(forms) > MaxExpansions {
				return nil, ErrTooComplex
			}
			queue = append(queue, next)
		}
	}
	expanded := make([]string, 0, len(forms))
	for form := range forms {
		expanded = append(expanded, form)
	}
	sort.Strings(expanded)
	return expanded, nil
}

// zeroDirOffsets reports every "**/" that forms its own path segment, meaning
// it starts the pattern or follows a separator, and is neither escaped nor
// inside a character class. A doublestar attached to a literal prefix is a
// spanning wildcard, not a directory wildcard, so "foo**/bar.go" keeps it.
func zeroDirOffsets(pattern string) []int {
	var offsets []int
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '\\':
			i++
		case '[':
			i = SkipClass(pattern, i)
		case '*':
			if !strings.HasPrefix(pattern[i:], "**/") {
				continue
			}
			if i > 0 && pattern[i-1] != '/' {
				continue
			}
			offsets = append(offsets, i)
			i += len("**/") - 1
		}
	}
	return offsets
}

// SkipClass returns the index of the ']' closing the character class that starts
// at open, or the last index of the pattern when unterminated.
func SkipClass(pattern string, open int) int {
	for i := open + 1; i < len(pattern); i++ {
		switch pattern[i] {
		case '\\':
			i++
		case ']':
			return i
		}
	}
	return len(pattern) - 1
}
