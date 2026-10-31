package builtin

import (
	"fmt"
	"sort"
	"strings"

	"github.com/gobwas/glob"
)

// maxGrepGlobPatterns caps the number of patterns produced by brace and
// zero-directory doublestar expansion so a pathological file_glob cannot
// multiply into an unbounded matcher set.
const maxGrepGlobPatterns = 256

// errGrepGlobTooComplex reports a file_glob whose brace and zero-directory
// expansion exceeds maxGrepGlobPatterns.
var errGrepGlobTooComplex = fmt.Errorf("pattern expands beyond %d forms", maxGrepGlobPatterns)

// compileGrepFileGlob compiles a grep file_glob pattern into a matcher over
// paths relative to the search root, using forward slashes.
//
// A slashless alternative names a file at any depth, so it matches the path
// basename. An alternative containing a slash matches the root-relative path
// only. A leading "./" on the pattern or on any brace alternative anchors that
// alternative to the search root. A "**/" that forms its own path segment also
// matches zero directories. Braces, escapes, and character classes keep
// gobwas/glob syntax; an empty pattern yields a nil matcher.
func compileGrepFileGlob(fileGlob string) (glob.Glob, error) {
	if fileGlob == "" {
		return nil, nil
	}

	patterns, err := expandGrepGlobPatterns(fileGlob)
	if err != nil {
		return nil, fmt.Errorf("invalid glob: %w", err)
	}

	var matcher grepGlobSet
	compiled := make(map[string]struct{})
	for _, pattern := range patterns {
		rootAnchored := false
		for strings.HasPrefix(pattern, "./") {
			pattern = pattern[len("./"):]
			rootAnchored = true
		}
		candidates := []string{pattern}
		if !rootAnchored && !strings.Contains(pattern, "/") {
			candidates = append(candidates, "**/"+pattern)
		}
		for _, candidate := range candidates {
			if _, ok := compiled[candidate]; ok {
				continue
			}
			compiled[candidate] = struct{}{}
			compiledPattern, err := glob.Compile(candidate, '/')
			if err != nil {
				return nil, fmt.Errorf("invalid glob: %w", err)
			}
			matcher = append(matcher, compiledPattern)
		}
	}
	return matcher, nil
}

// grepGlobSet matches a path when any compiled pattern matches it.
type grepGlobSet []glob.Glob

func (s grepGlobSet) Match(path string) bool {
	for _, pattern := range s {
		if pattern.Match(path) {
			return true
		}
	}
	return false
}

// expandGrepGlobPatterns flattens brace alternations and then drops each
// zero-directory form of "**/" from every alternative. It fails with
// errGrepGlobTooComplex instead of silently changing semantics when expansion
// exceeds maxGrepGlobPatterns.
func expandGrepGlobPatterns(pattern string) ([]string, error) {
	alternatives, err := expandGrepGlobBraces(pattern)
	if err != nil {
		return nil, err
	}
	var expanded []string
	for _, alternative := range alternatives {
		forms, err := expandGrepGlobZeroDir(alternative)
		if err != nil {
			return nil, err
		}
		if len(expanded)+len(forms) > maxGrepGlobPatterns {
			return nil, errGrepGlobTooComplex
		}
		expanded = append(expanded, forms...)
	}
	return expanded, nil
}

// expandGrepGlobBraces rewrites the first brace alternation, splitting on
// top-level commas while leaving escaped braces and braces inside character
// classes alone, then recurses into each alternative.
func expandGrepGlobBraces(pattern string) ([]string, error) {
	open, closing, alternatives, ok := splitGrepGlobBraces(pattern)
	if !ok {
		return []string{pattern}, nil
	}
	var expanded []string
	for _, alternative := range alternatives {
		sub, err := expandGrepGlobBraces(pattern[:open] + alternative + pattern[closing+1:])
		if err != nil {
			return nil, err
		}
		if len(expanded)+len(sub) > maxGrepGlobPatterns {
			return nil, errGrepGlobTooComplex
		}
		expanded = append(expanded, sub...)
	}
	return expanded, nil
}

// splitGrepGlobBraces locates the first brace alternation and splits its
// top-level alternatives. It reports false when no alternation with a closing
// brace exists, in which case the pattern is left to gobwas/glob to parse.
func splitGrepGlobBraces(pattern string) (int, int, []string, bool) {
	open := -1
	for i := 0; i < len(pattern) && open < 0; i++ {
		switch pattern[i] {
		case '\\':
			i++
		case '[':
			i = skipGrepGlobClass(pattern, i)
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
			i = skipGrepGlobClass(pattern, i)
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

// expandGrepGlobZeroDir returns every pattern produced by removing any subset
// of the standalone "**/" segments, so each of them matches zero directories
// as well as one or more. Removing only a prefix of the segments is not enough:
// gobwas/glob cannot elide an inner "**/" once an earlier segment is consumed.
func expandGrepGlobZeroDir(pattern string) ([]string, error) {
	forms := map[string]struct{}{pattern: {}}
	queue := []string{pattern}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, offset := range grepGlobZeroDirOffsets(current) {
			next := current[:offset] + current[offset+len("**/"):]
			if _, seen := forms[next]; seen {
				continue
			}
			forms[next] = struct{}{}
			if len(forms) > maxGrepGlobPatterns {
				return nil, errGrepGlobTooComplex
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

// grepGlobZeroDirOffsets reports every "**/" that forms its own path segment,
// meaning it starts the pattern or follows a separator, and is neither escaped
// nor inside a character class. A doublestar attached to a literal prefix is a
// spanning wildcard, not a directory wildcard, so "foo**/bar.go" keeps it.
func grepGlobZeroDirOffsets(pattern string) []int {
	var offsets []int
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '\\':
			i++
		case '[':
			i = skipGrepGlobClass(pattern, i)
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

// skipGrepGlobClass returns the index of the ']' closing the character class
// that starts at open, or the last index of the pattern when unterminated.
func skipGrepGlobClass(pattern string, open int) int {
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
