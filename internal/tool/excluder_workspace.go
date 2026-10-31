package tool

import (
	"path/filepath"
	"runtime"
	"strings"

	"github.com/gobwas/glob"
)

// builtinExcludeComponents is the lookup form of builtinExcludeEntries used by
// WorkspaceExcluder. Deriving it keeps the two views in sync.
var builtinExcludeComponents = func() map[string]struct{} {
	set := make(map[string]struct{}, len(builtinExcludeEntries))
	for _, name := range builtinExcludeEntries {
		set[name] = struct{}{}
	}
	return set
}()

// WorkspaceExcluder excludes walk entries using rules anchored to a workspace
// root. It accepts absolute native paths plus the root the walk started from,
// so built-in exclusions and component patterns never blind a walk that
// explicitly targets a path below an excluded ancestor.
//
// Rule precedence, highest first:
//
//   - exact configured paths (relative entries are anchored to the workspace
//     root, absolute entries are used as given) always exclude;
//   - force-include components never override exact paths but override every
//     pattern and built-in below;
//   - slash patterns match the workspace-relative slash path and any of its
//     ancestor directory prefixes; they do not apply outside the workspace;
//   - slashless patterns match individual path components (workspace-relative
//     inside the workspace, walk-relative outside);
//   - built-in directory names match walk-relative directory components only.
//
// The zero value excludes nothing.
type WorkspaceExcluder struct {
	workspaceRoot     string
	excludePaths      []string
	slashPatterns     []glob.Glob
	componentPatterns []glob.Glob
	forceInclude      map[string]struct{}
	builtins          bool
	caseInsensitive   bool
}

// NewWorkspaceExcluder creates a WorkspaceExcluder with always-active built-in
// directory exclusions plus user-configured exclusions anchored to
// workspaceRoot.
func NewWorkspaceExcluder(workspaceRoot string, excludePaths, excludePatterns []string) WorkspaceExcluder {
	return NewWorkspaceExcluderWithIncludes(workspaceRoot, excludePaths, excludePatterns, nil)
}

// NewWorkspaceExcluderWithIncludes creates a WorkspaceExcluder with
// always-active built-in directory exclusions, user-configured exclusions, and
// an optional set of component names that override patterns and built-ins.
// Exact configured paths still win over the force-include set. Pass nil for
// includeComponents to disable the override (equivalent to
// NewWorkspaceExcluder).
func NewWorkspaceExcluderWithIncludes(workspaceRoot string, excludePaths, excludePatterns, includeComponents []string) WorkspaceExcluder {
	return newWorkspaceExcluder(workspaceRoot, excludePaths, excludePatterns, includeComponents, runtime.GOOS == "windows")
}

// newWorkspaceExcluder builds a WorkspaceExcluder. windows selects platform
// path handling: separator normalisation and case-insensitive matching. It is
// separated from the exported constructor so tests can exercise both
// behaviours on any host.
func newWorkspaceExcluder(workspaceRoot string, excludePaths, excludePatterns, includeComponents []string, windows bool) WorkspaceExcluder {
	e := WorkspaceExcluder{
		builtins:        true,
		caseInsensitive: windows,
	}
	if workspaceRoot != "" {
		e.workspaceRoot = normalizeWorkspacePath(workspaceRoot, windows)
	}
	e.excludePaths = workspaceExcludePaths(e.workspaceRoot, excludePaths, windows)
	e.slashPatterns, e.componentPatterns = compileWorkspaceExcludePatterns(excludePatterns, windows)
	if len(includeComponents) > 0 {
		e.forceInclude = make(map[string]struct{}, len(includeComponents))
		for _, component := range includeComponents {
			if component == "" {
				continue
			}
			e.forceInclude[foldWorkspacePath(component, windows)] = struct{}{}
		}
	}
	return e
}

// ExcludeEntry reports whether the walk entry at absPath should be skipped.
// walkRoot is the absolute directory the walk started from, absPath is the
// entry's absolute native path, and isDir reports whether the entry is a
// directory. Paths are matched with the platform's separator and case rules.
func (e WorkspaceExcluder) ExcludeEntry(walkRoot, absPath string, isDir bool) bool {
	if !e.active() {
		return false
	}
	path := normalizeWorkspacePath(absPath, e.caseInsensitive)
	for _, prefix := range e.excludePaths {
		if path == prefix || strings.HasPrefix(path, prefix+string(filepath.Separator)) {
			return true
		}
	}

	workspaceRel, inWorkspace := e.workspaceRel(path)
	walkRel := e.walkRel(walkRoot, path, workspaceRel)

	patternRel := walkRel
	if inWorkspace {
		patternRel = workspaceRel
	}
	components := relComponents(patternRel)
	walkComponents := relComponents(walkRel)
	// Force-includes may be expressed against either basis: a name in an
	// ancestor above the walk root still overrides a workspace-relative
	// pattern, while a name inside the walk root overrides built-ins.
	if e.forceIncluded(components) || e.forceIncluded(walkComponents) {
		return false
	}
	if inWorkspace && matchSlashPatterns(e.slashPatterns, patternRel) {
		return true
	}
	if matchComponentPatterns(e.componentPatterns, components) {
		return true
	}
	return e.builtins && matchBuiltinDirectories(walkComponents, isDir)
}

// active reports whether the excluder carries any rule at all. The zero value
// is inactive.
func (e WorkspaceExcluder) active() bool {
	return e.builtins || e.workspaceRoot != "" || len(e.excludePaths) > 0 || len(e.slashPatterns) > 0 || len(e.componentPatterns) > 0
}

// workspaceRel returns path relative to the workspace root as a slash path.
// The second result is false when path is not inside the workspace.
func (e WorkspaceExcluder) workspaceRel(path string) (string, bool) {
	if e.workspaceRoot == "" {
		return "", false
	}
	rel, err := filepath.Rel(e.workspaceRoot, path)
	if err != nil {
		return "", false
	}
	return relativeSlash(rel)
}

// walkRel returns path relative to the walk root as a slash path, falling back
// to fallback when the walk root is unusable. Entries at or above the walk root
// yield an empty relative path so no component rule matches them.
func (e WorkspaceExcluder) walkRel(walkRoot, path, fallback string) string {
	if walkRoot == "" {
		return fallback
	}
	rel, err := filepath.Rel(normalizeWorkspacePath(walkRoot, e.caseInsensitive), path)
	if err != nil {
		return fallback
	}
	slash, ok := relativeSlash(rel)
	if !ok {
		return ""
	}
	return slash
}

// relativeSlash converts a filepath.Rel result into a slash path and reports
// whether it stays inside the base directory.
func relativeSlash(rel string) (string, bool) {
	switch {
	case rel == ".":
		return "", true
	case filepath.IsAbs(rel), rel == "..", strings.HasPrefix(rel, ".."+string(filepath.Separator)):
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// forceIncluded reports whether any component is listed in the force-include
// set. Components arrive already normalised for the platform.
func (e WorkspaceExcluder) forceIncluded(components []string) bool {
	if len(e.forceInclude) == 0 {
		return false
	}
	for _, component := range components {
		if _, ok := e.forceInclude[component]; ok {
			return true
		}
	}
	return false
}

// matchSlashPatterns reports whether any pattern matches relSlash or one of its
// ancestor directory prefixes, so a pattern for a directory also excludes the
// entries beneath it.
func matchSlashPatterns(patterns []glob.Glob, relSlash string) bool {
	if len(patterns) == 0 || relSlash == "" {
		return false
	}
	for _, prefix := range relPrefixes(relSlash) {
		for _, pattern := range patterns {
			if pattern.Match(prefix) {
				return true
			}
		}
	}
	return false
}

// relPrefixes returns relSlash followed by each ancestor directory prefix.
func relPrefixes(relSlash string) []string {
	parts := strings.Split(relSlash, "/")
	prefixes := make([]string, len(parts))
	for i := range parts {
		prefixes[i] = strings.Join(parts[:i+1], "/")
	}
	return prefixes
}

// matchComponentPatterns reports whether any slashless pattern matches any
// component.
func matchComponentPatterns(patterns []glob.Glob, components []string) bool {
	for _, pattern := range patterns {
		for _, component := range components {
			if pattern.Match(component) {
				return true
			}
		}
	}
	return false
}

// matchBuiltinDirectories reports whether any directory component of the
// walk-relative path is a built-in exclusion name. The final component of a
// file entry is not a directory, so a file named after a built-in stays
// visible while entries inside such a directory are excluded.
func matchBuiltinDirectories(components []string, isDir bool) bool {
	if !isDir && len(components) > 0 {
		components = components[:len(components)-1]
	}
	for _, component := range components {
		if _, ok := builtinExcludeComponents[component]; ok {
			return true
		}
	}
	return false
}

// relComponents splits a slash path into components.
func relComponents(relSlash string) []string {
	if relSlash == "" || relSlash == "." {
		return nil
	}
	return strings.Split(relSlash, "/")
}

// normalizeWorkspacePath cleans an absolute native path and, on Windows,
// normalises separators and case for comparison.
func normalizeWorkspacePath(path string, windows bool) string {
	if windows {
		path = strings.ReplaceAll(path, "\\", "/")
	}
	path = filepath.Clean(path)
	return foldWorkspacePath(path, windows)
}

// foldWorkspacePath lowercases paths on case-insensitive filesystems. It is
// used for whole paths and for single components.
func foldWorkspacePath(path string, windows bool) string {
	if windows {
		return strings.ToLower(path)
	}
	return path
}

// workspaceExcludePaths anchors relative configured paths to the workspace root
// and keeps absolute paths as given. Entries that cannot be anchored are
// dropped.
func workspaceExcludePaths(workspaceRoot string, excludePaths []string, windows bool) []string {
	cleaned := make([]string, 0, len(excludePaths))
	for _, path := range excludePaths {
		if path == "" {
			continue
		}
		path = normalizeWorkspacePath(path, windows)
		if !workspacePathIsAbs(path, windows) {
			if workspaceRoot == "" {
				continue
			}
			path = filepath.Join(workspaceRoot, path)
		}
		cleaned = append(cleaned, path)
	}
	return cleaned
}

// workspacePathIsAbs reports whether path is absolute, including Windows drive
// paths when the windows flag is set. The explicit drive check keeps Windows
// path handling observable on hosts whose filepath.IsAbs does not know drive
// letters.
func workspacePathIsAbs(path string, windows bool) bool {
	if filepath.IsAbs(path) {
		return true
	}
	if !windows || len(path) < 3 || path[1] != ':' {
		return false
	}
	return path[2] == '/' || path[2] == '\\'
}

// compileWorkspaceExcludePatterns compiles configured patterns into
// workspace-relative slash patterns and component patterns. Patterns that fail
// to compile are skipped, matching PathExcluder's silent handling.
func compileWorkspaceExcludePatterns(patterns []string, windows bool) ([]glob.Glob, []glob.Glob) {
	var slashPatterns, componentPatterns []glob.Glob
	for _, pattern := range patterns {
		if pattern == "" {
			continue
		}
		if windows {
			pattern = strings.ToLower(strings.ReplaceAll(pattern, "\\", "/"))
		}
		if hasUnescapedSeparator(pattern) {
			for _, expanded := range expandZeroDirectorySegments(pattern) {
				matcher, err := glob.Compile(expanded, '/')
				if err != nil {
					continue
				}
				slashPatterns = append(slashPatterns, matcher)
			}
			continue
		}
		matcher, err := glob.Compile(pattern)
		if err != nil {
			continue
		}
		componentPatterns = append(componentPatterns, matcher)
	}
	return slashPatterns, componentPatterns
}

// hasUnescapedSeparator reports whether the glob pattern contains a slash
// outside a bracket expression and not escaped by a backslash.
func hasUnescapedSeparator(pattern string) bool {
	escaped, inClass := false, false
	for i := 0; i < len(pattern); i++ {
		switch c := pattern[i]; {
		case escaped:
			escaped = false
		case c == '\\':
			escaped = true
		case inClass:
			if c == ']' {
				inClass = false
			}
		case c == '[':
			inClass = true
		case c == '/':
			return true
		}
	}
	return false
}

// expandZeroDirectorySegments returns pattern plus every variant with one or
// more removable "**/" segments dropped, so `**/name` also matches a top-level
// `name`. Escaped asterisks and bracket expressions are left intact.
func expandZeroDirectorySegments(pattern string) []string {
	expanded := []string{pattern}
	seen := map[string]struct{}{pattern: {}}
	for i := 0; i < len(expanded); i++ {
		for _, next := range removeZeroDirectorySegments(expanded[i]) {
			if _, ok := seen[next]; ok {
				continue
			}
			seen[next] = struct{}{}
			expanded = append(expanded, next)
		}
	}
	return expanded
}

// removeZeroDirectorySegments returns pattern with each removable "**/"
// occurrence removed one at a time.
func removeZeroDirectorySegments(pattern string) []string {
	var variants []string
	escaped, inClass := false, false
	for i := 0; i < len(pattern); i++ {
		switch c := pattern[i]; {
		case escaped:
			escaped = false
		case c == '\\':
			escaped = true
		case inClass:
			if c == ']' {
				inClass = false
			}
		case c == '[':
			inClass = true
		case c == '*' && strings.HasPrefix(pattern[i:], "**/"):
			variants = append(variants, pattern[:i]+pattern[i+3:])
			i += 2
		}
	}
	return variants
}
