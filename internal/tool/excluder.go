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

// FilePickerAlwaysInclude lists path components that are shown in the TUI
// file picker and file list overlays even when the built-in heuristic
// exclusion list would otherwise hide them. Used to surface project-metadata
// folders (such as .steiner) that are otherwise excluded by default.
//
// Matching is component-based, so a nested directory named .steiner at any
// depth is also re-included. Exact prefix exclusions such as
// FilePickerExcludePaths take precedence over this list.
var FilePickerAlwaysInclude = []string{
	".steiner",
}

// FilePickerExcludePaths lists exact relative prefixes hidden from the TUI
// file picker and file list overlays even when a parent directory is
// force-included. Keeps heavy .steiner subdirectories (tmp, worktrees) out
// of the picker while the rest of .steiner stays visible.
var FilePickerExcludePaths = []string{
	".steiner/tmp",
	".steiner/worktrees",
}

// PathExcluder checks paths against exact prefix exclusions and glob patterns.
// Built-in heuristic exclusions are always active and appended automatically.
// Exact prefix exclusions win over an optional forceInclude set, which in turn
// overrides only the component glob patterns.
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
// set of component names that override glob-pattern exclusions. Exact prefix
// exclusions still win over the force-include set. Pass nil for
// includeComponents to disable the override (equivalent to NewPathExcluder).
// Used by the TUI file picker to surface project-metadata folders.
func NewPathExcluderWithIncludes(excludePaths, excludePatterns, includeComponents []string) PathExcluder {
	e := PathExcluder{
		excludePaths: cleanExcludePaths(excludePaths),
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

// cleanExcludePaths applies filepath.Clean to each configured exclude path so
// that user-configured forms such as a trailing separator ("secrets/") or a
// "./"-relative prefix match the same way a cleaned candidate path does in
// ShouldExclude. Empty entries are dropped: filepath.Clean("") returns ".",
// which would otherwise match the entire relative tree as an exclude prefix.
func cleanExcludePaths(excludePaths []string) []string {
	cleaned := make([]string, 0, len(excludePaths))
	for _, p := range excludePaths {
		if p == "" {
			continue
		}
		cleaned = append(cleaned, filepath.Clean(p))
	}
	return cleaned
}

// ShouldExclude returns true if the path matches any exclusion rule.
// Exact prefix exclusions are checked first and take precedence over
// force-include: a path under an excluded prefix is always excluded. If the
// path is not prefix-excluded but contains any component listed in the
// forceInclude set, it is not excluded by the glob patterns.
func (e PathExcluder) ShouldExclude(path string) bool {
	for _, prefix := range e.excludePaths {
		// filepath.Clean collapses a root separator exclude path to just the
		// separator itself, so prefix+separator ("//") never matches any real
		// path. Treat a bare separator as excluding everything beneath it.
		if prefix == string(filepath.Separator) {
			return true
		}
		if path == prefix || strings.HasPrefix(path, prefix+string(filepath.Separator)) {
			return true
		}
	}
	if e.shouldForceInclude(path) {
		return false
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
