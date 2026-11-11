package builtin

import (
	"fmt"
	"strings"

	"github.com/gobwas/glob"
)

// compileGrepFileGlob compiles a grep file_glob pattern into a matcher over
// paths relative to the search root, using forward slashes.
//
// A slashless alternative names a file at any depth, so it matches the path
// basename. An alternative containing a slash matches the root-relative path
// only. A leading "./" on the pattern or on any brace alternative anchors that
// alternative to the search root; the marker is read before zero-directory
// expansion, so a "./" left behind by dropping a "**/" segment stays literal.
// A "**/" that forms its own path segment also matches zero directories.
// Braces, escapes, and character classes keep gobwas/glob syntax: a matched
// group such as "{a,b}" is expanded, a brace inside a character class is
// literal, and an unescaped brace with no counterpart is an error, so a literal
// brace is written "\{" or "\}". An empty pattern yields a nil matcher.
func compileGrepFileGlob(fileGlob string) (glob.Glob, error) {
	if fileGlob == "" {
		return nil, nil
	}

	forms, err := expandGrepGlobPatterns(fileGlob)
	if err != nil {
		return nil, fmt.Errorf("invalid glob: %w", err)
	}

	var matcher grepGlobSet
	compiled := make(map[string]struct{})
	for _, form := range forms {
		candidates := []string{form.pattern}
		if !form.rootAnchored && !strings.Contains(form.pattern, "/") {
			candidates = append(candidates, "**/"+form.pattern)
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

// grepGlobForm is one expanded file_glob alternative plus the root anchoring
// its original brace alternative carried.
type grepGlobForm struct {
	pattern      string
	rootAnchored bool
}

// expandGrepGlobPatterns flattens brace alternations, strips any leading "./"
// root marker from each original alternative, and only then drops every
// zero-directory form of that alternative's "**/" segments. It fails with
// errGlobUnmatchedBrace for a malformed brace and with errGlobTooComplex instead
// of silently changing semantics when expansion exceeds maxGlobExpansions.
func expandGrepGlobPatterns(pattern string) ([]grepGlobForm, error) {
	alternatives, err := expandGlobBraces(pattern)
	if err != nil {
		return nil, err
	}
	var expanded []grepGlobForm
	for _, alternative := range alternatives {
		rootAnchored, trimmed := trimGrepGlobRootMarker(alternative)
		forms, err := expandGlobZeroDir(trimmed)
		if err != nil {
			return nil, err
		}
		if len(expanded)+len(forms) > maxGlobExpansions {
			return nil, errGlobTooComplex
		}
		for _, form := range forms {
			expanded = append(expanded, grepGlobForm{pattern: form, rootAnchored: rootAnchored})
		}
	}
	return expanded, nil
}

// trimGrepGlobRootMarker removes leading "./" segments and reports whether the
// pattern was marked root-relative. It must run on the original brace
// alternative: a "./" produced later by dropping a "**/" segment is literal
// pattern text and must not anchor the pattern.
func trimGrepGlobRootMarker(pattern string) (bool, string) {
	rootAnchored := false
	for strings.HasPrefix(pattern, "./") {
		pattern = pattern[len("./"):]
		rootAnchored = true
	}
	return rootAnchored, pattern
}
