package builtin

import (
	"fmt"
	"strings"

	"github.com/gobwas/glob"
)

// maxGrepGlobPatterns caps the number of patterns produced by brace and
// zero-directory doublestar expansion so a pathological file_glob cannot
// multiply into an unbounded matcher set.
const maxGrepGlobPatterns = 256

// compileGrepFileGlob compiles a grep file_glob pattern into a matcher over
// paths relative to the search root, using forward slashes.
//
// A slashless alternative names a file at any depth, so it matches the path
// basename. An alternative containing a slash matches the root-relative path
// only. A leading "./" marks the whole pattern as root-relative. Every "**/"
// also matches zero directories. Braces, escapes, and character classes keep
// gobwas/glob syntax; an empty pattern yields a nil matcher.
func compileGrepFileGlob(fileGlob string) (glob.Glob, error) {
	if fileGlob == "" {
		return nil, nil
	}

	rootAnchored := false
	for strings.HasPrefix(fileGlob, "./") {
		fileGlob = fileGlob[len("./"):]
		rootAnchored = true
	}

	var matcher grepGlobSet
	compiled := make(map[string]struct{})
	for _, pattern := range expandGrepGlobPatterns(fileGlob) {
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
// zero-directory form of "**/" from every alternative. When expansion exceeds
// maxGrepGlobPatterns the pattern is returned unchanged and compiles with its
// original gobwas semantics.
func expandGrepGlobPatterns(pattern string) []string {
	var expanded []string
	for _, alternative := range expandGrepGlobBraces(pattern) {
		expanded = append(expanded, expandGrepGlobZeroDir(alternative)...)
		if len(expanded) > maxGrepGlobPatterns {
			return []string{pattern}
		}
	}
	return expanded
}

// expandGrepGlobBraces rewrites the first brace alternation, splitting on
// top-level commas while leaving escaped braces and braces inside character
// classes alone, then recurses into each alternative.
func expandGrepGlobBraces(pattern string) []string {
	open, closing, alternatives, ok := splitGrepGlobBraces(pattern)
	if !ok {
		return []string{pattern}
	}
	var expanded []string
	for _, alternative := range alternatives {
		sub := expandGrepGlobBraces(pattern[:open] + alternative + pattern[closing+1:])
		if len(expanded)+len(sub) > maxGrepGlobPatterns {
			return []string{pattern}
		}
		expanded = append(expanded, sub...)
	}
	return expanded
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

// expandGrepGlobZeroDir returns the pattern plus every variant with one "**/"
// removed, so a directory doublestar also matches zero directories.
func expandGrepGlobZeroDir(pattern string) []string {
	idx, ok := findGrepGlobZeroDir(pattern)
	if !ok {
		return []string{pattern}
	}
	rest := expandGrepGlobZeroDir(pattern[:idx] + pattern[idx+len("**/"):])
	if len(rest)+1 > maxGrepGlobPatterns {
		return []string{pattern}
	}
	return append([]string{pattern}, rest...)
}

// findGrepGlobZeroDir reports the first "**/" outside a character class and
// not escaped. Doublestar text inside classes or escape sequences is literal.
func findGrepGlobZeroDir(pattern string) (int, bool) {
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '\\':
			i++
		case '[':
			i = skipGrepGlobClass(pattern, i)
		case '*':
			if strings.HasPrefix(pattern[i:], "**/") {
				return i, true
			}
		}
	}
	return 0, false
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
