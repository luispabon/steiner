package tool

import (
	"path/filepath"
	"strings"
)

// Built-in heuristic exclusion patterns. These are always active and match
// against any path component.
var builtinExcludeEntries = []string{
	".git",
	"node_modules",
	".steiner",
	".claude",
	".git-worktrees",
	"vendor",
	".cache",
	"dist",
	"build",
	"out",
	"target",
	".next",
	".nuxt",
}

// FilePickerAlwaysInclude lists path components that are always shown in the
// TUI file picker and file list overlays, even when the built-in heuristic
// exclusion list would otherwise hide them. Used to surface project-metadata
// folders (such as .steiner) that are otherwise excluded by default.
//
// Matching is component-based, so a nested directory named .steiner at any
// depth is also re-included.
var FilePickerAlwaysInclude = []string{
	".steiner",
}

// PathExcluder checks paths against exact prefix exclusions and glob patterns.
// Built-in heuristic exclusions are always active and appended automatically.
// An optional forceInclude set can override exclusions for specific component
// names.
type PathExcluder struct {
	excludePaths    []string
	excludePatterns []string
	forceInclude    map[string]struct{}
}

// NewPathExcluder creates a PathExcluder with always-active built-in heuristic
// exclusions plus user-configured exclusions. User config appends to built-ins.
// Use this constructor for tools (glob, grep, config discovery) that should
// fully respect exclusion rules.
func NewPathExcluder(excludePaths, excludePatterns []string) PathExcluder {
	return NewPathExcluderWithIncludes(excludePaths, excludePatterns, nil)
}

// NewPathExcluderWithIncludes creates a PathExcluder with always-active
// built-in heuristic exclusions, user-configured exclusions, and an optional
// set of component names that override exclusions. Pass nil for
// includeComponents to disable the override (equivalent to NewPathExcluder).
// Used by the TUI file picker to surface project-metadata folders.
func NewPathExcluderWithIncludes(excludePaths, excludePatterns, includeComponents []string) PathExcluder {
	e := PathExcluder{
		excludePaths: append([]string(nil), excludePaths...),
	}
	e.excludePatterns = append(e.excludePatterns, builtinExcludeEntries...)
	e.excludePatterns = append(e.excludePatterns, excludePatterns...)
	if len(includeComponents) > 0 {
		e.forceInclude = make(map[string]struct{}, len(includeComponents))
		for _, c := range includeComponents {
			e.forceInclude[c] = struct{}{}
		}
	}
	return e
}

// ShouldExclude returns true if the path matches any exclusion rule.
// Exact prefix exclusions are checked first, then glob patterns are matched
// against each path component. If the path contains any component listed in
// the forceInclude set, the path is never excluded regardless of other rules.
func (e PathExcluder) ShouldExclude(path string) bool {
	if e.shouldForceInclude(path) {
		return false
	}
	for _, prefix := range e.excludePaths {
		if path == prefix || strings.HasPrefix(path, prefix+string(filepath.Separator)) {
			return true
		}
	}
	parts := strings.Split(path, string(filepath.Separator))
	for _, pattern := range e.excludePatterns {
		for _, part := range parts {
			if matched, _ := filepath.Match(pattern, part); matched {
				return true
			}
		}
	}
	return false
}

// shouldForceInclude reports whether the path contains any component listed in
// the forceInclude set. Returns false when the set is empty (the default).
func (e PathExcluder) shouldForceInclude(path string) bool {
	if len(e.forceInclude) == 0 {
		return false
	}
	parts := strings.Split(path, string(filepath.Separator))
	for _, part := range parts {
		if _, ok := e.forceInclude[part]; ok {
			return true
		}
	}
	return false
}
