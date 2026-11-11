package builtin

import (
	"errors"
	"fmt"
	"strings"

	"github.com/gobwas/glob"
)

// globMatcher holds the compiled forms of one glob pattern and the deepest
// directory level a walk needs to visit.
type globMatcher struct {
	// matchers match a path as written. Files and directories both test their
	// relative slash path against these.
	matchers globSet
	// dirMatchers are the compiled forms that end in "/". Only directories test
	// their relative path plus the slash against these, so */ matches directories
	// only and a* matches nothing.
	dirMatchers globSet
	// pruneDepth is the segment count of a fixed-depth pattern. Zero means the
	// walk is not pruned.
	pruneDepth int
}

// globSet matches a path when any compiled pattern in the set matches it.
type globSet []glob.Glob

// Match reports whether any pattern in the set matches path.
func (s globSet) Match(path string) bool {
	for _, pattern := range s {
		if pattern.Match(path) {
			return true
		}
	}
	return false
}

// errGlobPatternAbsolute reports a pattern that starts at the filesystem root
// instead of inside the directory the caller named.
var errGlobPatternAbsolute = errors.New(`pattern must be relative to path; put the directory in path`)

// errGlobPatternParent reports a pattern that climbs above the search root with
// a ".." segment.
var errGlobPatternParent = errors.New(`pattern must not contain ".." segments; put the directory in path`)

func compileGlobMatcher(pattern string) (globMatcher, error) {
	pattern = normalizeGlobPattern(pattern)
	forms, err := expandGlobForms(pattern)
	if err != nil {
		return globMatcher{}, err
	}
	// "." segments are normalized per expanded form, so a brace alternative can
	// carry its own dot segments instead of only a raw leading "./" being
	// stripped.
	for i, form := range forms {
		forms[i] = normalizeGlobForm(form)
	}
	matcher := globMatcher{
		matchers:    make(globSet, 0, len(forms)),
		dirMatchers: make(globSet, 0, len(forms)),
	}
	seen := make(map[string]struct{}, len(forms))
	for _, form := range forms {
		if err := globFormRootError(form); err != nil {
			return globMatcher{}, err
		}
		if _, ok := seen[form]; ok {
			continue
		}
		seen[form] = struct{}{}
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
	if depth, ok := globPruneDepth(forms); ok {
		matcher.pruneDepth = depth
	}
	return matcher, nil
}

// normalizeGlobPattern strips the leading "./" segments a caller may write in
// front of a relative pattern and substitutes the default pattern when nothing
// is left, so "", ".", "./", "./.", and "././" all mean the default pattern.
// It sees only the raw pattern; compileGlobMatcher normalizes the dot segments
// of every expanded form, including those inside brace alternatives.
func normalizeGlobPattern(pattern string) string {
	for strings.HasPrefix(pattern, "./") {
		pattern = pattern[len("./"):]
	}
	if pattern == "" || pattern == "." {
		return defaultGlobPattern
	}
	return pattern
}

// normalizeGlobForm drops every path segment that is exactly ".", whether it
// leads, sits between other segments, or trails, so a form such as "./a" or
// "dot/./x" keeps matching what it names. Segments are split only at a "/"
// outside a character class and outside an escape, so the dot in a class such
// as "a[/.]" stays a class member. A form left with nothing becomes the empty
// root pattern, which matches nothing, the same as an empty brace alternative;
// it is never turned back into the default pattern. A trailing "/" survives
// only when the form keeps other content.
func normalizeGlobForm(form string) string {
	segments := splitGlobForm(form)
	kept := make([]string, 0, len(segments))
	for _, segment := range segments {
		if segment != "." {
			kept = append(kept, segment)
		}
	}
	return strings.Join(kept, "/")
}

// splitGlobForm splits a glob form at every "/" that is outside a character
// class and not escaped, so a class such as "[/]" never divides a segment.
func splitGlobForm(form string) []string {
	segments := make([]string, 0, 2)
	start := 0
	for i := 0; i < len(form); i++ {
		switch form[i] {
		case '\\':
			i++
		case '[':
			i = skipGlobClass(form, i)
		case '/':
			segments = append(segments, form[start:i])
			start = i + 1
		}
	}
	return append(segments, form[start:])
}

// globFormRootError reports why an expanded form escapes the search root: a
// leading "/" makes it absolute, and a ".." segment climbs above the root. The
// absolute check comes first so a form that is both names its leading slash. It
// returns nil when the form stays inside the root.
func globFormRootError(form string) error {
	if strings.HasPrefix(form, "/") {
		return errGlobPatternAbsolute
	}
	for _, segment := range strings.Split(form, "/") {
		if segment == ".." {
			return errGlobPatternParent
		}
	}
	return nil
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

// globPruneDepth reports the deepest segment count the expanded forms reach and
// whether depth pruning is safe. A form keeps pruning only when it holds no
// "**", no "[" and no "\\": gobwas list and range matchers ignore the
// separator, so a class such as [/] can cross a directory boundary, and an
// escape can turn a metacharacter into a literal the segment count cannot
// predict. The count runs over the expanded forms, so a brace alternative with
// its own slashes widens the depth instead of being ignored.
func globPruneDepth(forms []string) (int, bool) {
	depth := 0
	for _, form := range forms {
		if strings.Contains(form, "**") || strings.ContainsAny(form, `[\`) {
			return 0, false
		}
		depth = max(depth, strings.Count(strings.TrimSuffix(form, "/"), "/")+1)
	}
	return depth, true
}

// matchEntry reports whether the walk entry matches and returns its result
// line: the relative slash path, with a trailing slash for directories. A
// directory matches when a pattern matches its relative path or a pattern
// ending in "/" matches that path plus the slash, so a pattern that matches the
// directory itself returns it while */ and **/ return directories only.
func (m globMatcher) matchEntry(rel string, isDir bool) (string, bool) {
	if !isDir {
		return rel, m.matchers.Match(rel)
	}
	if m.matchers.Match(rel) || m.dirMatchers.Match(rel+"/") {
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
