package builtin

import (
	"fmt"
	"strings"

	"github.com/gobwas/glob"
)

// globMatcher holds the compiled forms of one glob pattern and the deepest
// directory level a walk needs to visit.
type globMatcher struct {
	// matchers match a path as written. Files and directories both test their
	// relative slash path against these.
	matchers []glob.Glob
	// dirMatchers are the compiled forms that end in "/". Only directories test
	// their relative path plus the slash against these, so */ matches directories
	// only and a* matches nothing.
	dirMatchers []glob.Glob
	// pruneDepth is the segment count of a fixed-depth pattern. Zero means the
	// walk is not pruned.
	pruneDepth int
}

func compileGlobMatcher(pattern string) (globMatcher, error) {
	if pattern == "" {
		pattern = "*"
	}
	forms, err := expandGlobForms(pattern)
	if err != nil {
		return globMatcher{}, err
	}
	matcher := globMatcher{
		matchers:    make([]glob.Glob, 0, len(forms)),
		dirMatchers: make([]glob.Glob, 0, len(forms)),
	}
	for _, form := range forms {
		compiled, err := glob.Compile(form, '/')
		if err != nil {
			return globMatcher{}, fmt.Errorf("compile glob %q: %w", form, err)
		}
		if strings.HasSuffix(form, "/") {
			matcher.dirMatchers = append(matcher.dirMatchers, compiled)
			continue
		}
		matcher.matchers = append(matcher.matchers, compiled)
	}
	if globFixedDepth(pattern) {
		matcher.pruneDepth = strings.Count(strings.TrimSuffix(pattern, "/"), "/") + 1
	}
	return matcher, nil
}

// expandGlobForms flattens brace alternations, then expands each alternative's
// zero-directory doublestar forms, enforcing maxGlobExpansions across the whole
// pattern. The shared brace expansion validates the raw pattern first, so a
// malformed brace is an error and never reaches gobwas as a brace expression.
func expandGlobForms(pattern string) ([]string, error) {
	alternatives, err := expandGlobBraces(pattern)
	if err != nil {
		return nil, err
	}
	var forms []string
	for _, alternative := range alternatives {
		expanded, err := expandGlobZeroDir(alternative)
		if err != nil {
			return nil, err
		}
		if len(forms)+len(expanded) > maxGlobExpansions {
			return nil, errGlobTooComplex
		}
		forms = append(forms, expanded...)
	}
	return forms, nil
}

// globFixedDepth reports whether every path the pattern can match has at most
// its own segment count. Only ** and a character class such as [!x] can match a
// separator without a literal slash, because gobwas list and range matchers
// ignore it, so both disable depth pruning. The brace and escape gates are
// conservative: a brace alternative cannot exceed the segments its literal
// slashes already declare, and an escape cannot introduce a separator.
func globFixedDepth(pattern string) bool {
	return !strings.Contains(pattern, "**") && !strings.ContainsAny(pattern, `{\[`)
}

// matchEntry reports whether the walk entry matches and returns its result
// line: the relative slash path, with a trailing slash for directories. A
// directory matches when a pattern matches its relative path or a pattern
// ending in "/" matches that path plus the slash, so a pattern that matches the
// directory itself returns it while */ and **/ return directories only.
func (m globMatcher) matchEntry(rel string, isDir bool) (string, bool) {
	if !isDir {
		return rel, matchesAnyGlob(m.matchers, rel)
	}
	if matchesAnyGlob(m.matchers, rel) || matchesAnyGlob(m.dirMatchers, rel+"/") {
		return rel + "/", true
	}
	return "", false
}

// prunes reports whether a directory at rel is too deep for any compiled
// pattern to match: every pattern that allows pruning has a fixed segment
// count, so its children cannot match. The directory itself is still matched
// before the caller prunes it.
func (m globMatcher) prunes(rel string) bool {
	return m.pruneDepth > 0 && strings.Count(rel, "/")+1 >= m.pruneDepth
}

func matchesAnyGlob(matchers []glob.Glob, path string) bool {
	for _, matcher := range matchers {
		if matcher.Match(path) {
			return true
		}
	}
	return false
}
