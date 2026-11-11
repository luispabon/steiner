package builtin

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// maxGlobExpansions caps the number of patterns produced by brace and
// zero-directory doublestar expansion, for both the glob tool and grep's
// file_glob, so a pathological pattern cannot multiply into an unbounded
// matcher set.
const maxGlobExpansions = 256

// errGlobTooComplex reports a pattern whose brace and zero-directory expansion
// exceeds maxGlobExpansions. Grep and glob both surface it.
var errGlobTooComplex = fmt.Errorf("pattern expands beyond %d forms", maxGlobExpansions)

// errGlobUnmatchedBrace reports a pattern whose braces do not pair up once
// escapes and character classes are excluded. Grep and glob both surface it
// instead of compiling the brace as literal text.
var errGlobUnmatchedBrace = errors.New(`unmatched brace in pattern; escape literal braces as \{ or \}`)

// validateGlobBraces reports errGlobUnmatchedBrace when the pattern holds an
// unescaped brace outside a character class with no counterpart. It runs before
// any expansion, so the forms later compiled by gobwas hold only escaped braces
// and braces inside character classes; matched groups are expanded away.
func validateGlobBraces(pattern string) error {
	depth := 0
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '\\':
			i++
		case '[':
			i = skipGlobClass(pattern, i)
		case '{':
			depth++
		case '}':
			if depth == 0 {
				return errGlobUnmatchedBrace
			}
			depth--
		}
	}
	if depth > 0 {
		return errGlobUnmatchedBrace
	}
	return nil
}

// expandGlobBraces rewrites the first brace alternation, splitting on top-level
// commas while leaving escaped braces and braces inside character classes
// alone, then recurses into each alternative. It validates the pattern first, so
// an unmatched brace is an error and no returned form holds one. Recursion stays
// valid because substituting a matched group keeps the rest balanced.
func expandGlobBraces(pattern string) ([]string, error) {
	if err := validateGlobBraces(pattern); err != nil {
		return nil, err
	}
	open, closing, alternatives, ok := splitGlobBraces(pattern)
	if !ok {
		return []string{pattern}, nil
	}
	var expanded []string
	for _, alternative := range alternatives {
		sub, err := expandGlobBraces(pattern[:open] + alternative + pattern[closing+1:])
		if err != nil {
			return nil, err
		}
		if len(expanded)+len(sub) > maxGlobExpansions {
			return nil, errGlobTooComplex
		}
		expanded = append(expanded, sub...)
	}
	return expanded, nil
}

// splitGlobBraces locates the first brace alternation and splits its top-level
// alternatives. It reports false when the pattern holds no unescaped brace
// group outside a character class, in which case the pattern is left for
// gobwas/glob to parse.
func splitGlobBraces(pattern string) (int, int, []string, bool) {
	open := -1
	for i := 0; i < len(pattern) && open < 0; i++ {
		switch pattern[i] {
		case '\\':
			i++
		case '[':
			i = skipGlobClass(pattern, i)
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
			i = skipGlobClass(pattern, i)
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

// expandGlobZeroDir returns every pattern produced by removing any subset of
// the standalone "**/" segments, so each of them matches zero directories as
// well as one or more. Removing only a prefix of the segments is not enough:
// gobwas/glob cannot elide an inner "**/" once an earlier segment is consumed.
func expandGlobZeroDir(pattern string) ([]string, error) {
	forms := map[string]struct{}{pattern: {}}
	queue := []string{pattern}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, offset := range globZeroDirOffsets(current) {
			next := current[:offset] + current[offset+len("**/"):]
			if _, seen := forms[next]; seen {
				continue
			}
			forms[next] = struct{}{}
			if len(forms) > maxGlobExpansions {
				return nil, errGlobTooComplex
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

// globZeroDirOffsets reports every "**/" that forms its own path segment,
// meaning it starts the pattern or follows a separator, and is neither escaped
// nor inside a character class. A doublestar attached to a literal prefix is a
// spanning wildcard, not a directory wildcard, so "foo**/bar.go" keeps it.
func globZeroDirOffsets(pattern string) []int {
	var offsets []int
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '\\':
			i++
		case '[':
			i = skipGlobClass(pattern, i)
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

// skipGlobClass returns the index of the ']' closing the character class that
// starts at open, or the last index of the pattern when unterminated.
func skipGlobClass(pattern string, open int) int {
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
