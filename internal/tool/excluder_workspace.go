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
// root. It accepts absolute native paths plus the root the walk started from.
// Built-in directory exclusions match only walk-relative components, so a walk
// that explicitly targets a path below an excluded ancestor (for example
// .steiner/worktrees/x) stays searchable. Configured slashless patterns stay
// workspace-relative inside the workspace, so an explicit root below a
// configured component pattern is still excluded.
//
// Paths are compared lexically: symlinks are neither followed nor resolved.
// Callers must pass absPath and walkRoot in the same form (lexically cleaned or
// symlink-resolved) as workspaceRoot and the configured exclude paths.
//
// Rule precedence, highest first:
//
//   - exact configured paths (relative entries are anchored to the workspace
//     root, "." excludes the workspace, a filesystem or volume root excludes
//     everything beneath it, and absolute entries are used as given) always
//     exclude;
//   - force-include components never override exact paths but override every
//     pattern and built-in below;
//   - slash patterns match the workspace-relative slash path and any of its
//     ancestor directory prefixes; they do not apply outside the workspace;
//   - slashless patterns match individual path components (workspace-relative
//     inside the workspace, walk-relative outside);
//   - built-in directory names match walk-relative directory components only.
//
// Patterns may be prefixed with "./" or "/" and may end with separators;
// these forms are normalised. A path segment that is exactly "**" matches zero
// or more path segments, so "**/fixtures" matches both "fixtures" and
// "a/fixtures". Character classes do not match separators, so "a[!x]b" matches
// a single path segment. Invalid patterns are ignored.
//
// The zero value excludes nothing.
type WorkspaceExcluder struct {
	workspaceRoot     string
	excludePaths      []string
	slashPatterns     []workspacePattern
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
// directory. Paths are matched lexically with the platform's separator and
// case rules: symlinks are not resolved, so callers must pass both arguments in
// the same form (lexically cleaned or resolved) as the workspace root and
// configured exclusions.
func (e WorkspaceExcluder) ExcludeEntry(walkRoot, absPath string, isDir bool) bool {
	if !e.active() {
		return false
	}
	path := normalizeWorkspacePath(absPath, e.caseInsensitive)
	for _, prefix := range e.excludePaths {
		if exactPathMatch(path, prefix) {
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
func matchSlashPatterns(patterns []workspacePattern, relSlash string) bool {
	for _, pattern := range patterns {
		if pattern.matchesPrefix(relSlash) {
			return true
		}
	}
	return false
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
	if windows && len(path) == 2 && path[1] == ':' {
		// Keep a drive designator in volume-root form on every host; Windows
		// itself cleans a bare "C:" as "C:." instead.
		path += "/"
	}
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
func compileWorkspaceExcludePatterns(patterns []string, windows bool) ([]workspacePattern, []glob.Glob) {
	var slashPatterns []workspacePattern
	var componentPatterns []glob.Glob
	for _, pattern := range patterns {
		if pattern == "" {
			continue
		}
		pattern = normalizeWorkspacePattern(pattern, windows)
		if pattern == "" {
			continue
		}
		if !strings.Contains(pattern, "/") {
			matcher, err := glob.Compile(pattern)
			if err != nil {
				continue
			}
			componentPatterns = append(componentPatterns, matcher)
			continue
		}
		if compiled, ok := compileWorkspaceSlashPattern(pattern); ok {
			slashPatterns = append(slashPatterns, compiled)
		}
	}
	return slashPatterns, componentPatterns
}

// exactPathMatch reports whether path equals prefix or sits beneath it. A
// prefix that already ends in a separator (a filesystem or volume root) is used
// as is, so "/" and "C:\" match their descendants instead of forming a doubled
// separator that matches nothing.
func exactPathMatch(path, prefix string) bool {
	if path == prefix {
		return true
	}
	separator := string(filepath.Separator)
	if strings.HasSuffix(prefix, separator) {
		return strings.HasPrefix(path, prefix)
	}
	return strings.HasPrefix(path, prefix+separator)
}

// workspacePattern is a compiled slash pattern. It compiles into path segments
// and matches with a bounded segment walk that accepts a match of the entry or
// any ancestor prefix, so zero-directory "**/" handling neither expands
// combinations nor rescans prefixes.
type workspacePattern struct {
	segments []workspacePatternSegment
}

// workspacePatternSegment is one path segment of a compiled slash pattern. A
// globstar segment matches zero or more path segments. Any other segment matches
// one path segment, or several when it contains a separator or a "**" super
// wildcard that can span separators (a brace alternative or character class
// containing a separator also qualifies).
type workspacePatternSegment struct {
	globstar bool
	multi    bool
	matcher  glob.Glob
}

// matchesPrefix reports whether the pattern matches relSlash or any ancestor
// directory prefix of it.
func (p workspacePattern) matchesPrefix(relSlash string) bool {
	if len(p.segments) == 0 || relSlash == "" {
		return false
	}
	parts := strings.Split(relSlash, "/")
	current := make([]bool, len(parts)+1)
	next := make([]bool, len(parts)+1)
	current[0] = true
	for _, segment := range p.segments {
		clear(next)
		if segment.globstar {
			reachable := false
			for i := range next {
				reachable = reachable || current[i]
				next[i] = reachable
			}
		} else {
			for start := 0; start < len(parts); start++ {
				if !current[start] {
					continue
				}
				if segment.multi {
					for end := start + 1; end <= len(parts); end++ {
						if segment.matcher.Match(strings.Join(parts[start:end], "/")) {
							next[end] = true
						}
					}
					continue
				}
				if segment.matcher.Match(parts[start]) {
					next[start+1] = true
				}
			}
		}
		current, next = next, current
	}
	// Any reachable component boundary is a directory prefix the pattern
	// matched, which excludes the entry beneath it.
	for _, reachable := range current {
		if reachable {
			return true
		}
	}
	return false
}

// compileWorkspaceSlashPattern compiles one slash pattern, reporting false when
// the pattern is invalid. Only a segment that is exactly "**" is a globstar; a
// segment such as "a**" keeps its gobwas meaning and matches within one path
// segment.
func compileWorkspaceSlashPattern(pattern string) (workspacePattern, bool) {
	segments, ok := splitWorkspacePatternSegments(pattern)
	if !ok {
		return workspacePattern{}, false
	}
	compiled := make([]workspacePatternSegment, 0, len(segments))
	for _, segment := range segments {
		if segment == "**" {
			compiled = append(compiled, workspacePatternSegment{globstar: true})
			continue
		}
		// gobwas matches a negated character class against the separator too;
		// excluding it keeps classes from crossing path segment boundaries.
		matcher, err := glob.Compile(excludeSeparatorFromNegatedClasses(segment), '/')
		if err != nil {
			return workspacePattern{}, false
		}
		compiled = append(compiled, workspacePatternSegment{
			multi:   strings.Contains(segment, "/") || hasUnescapedDoubleStar(segment),
			matcher: matcher,
		})
	}
	return workspacePattern{segments: compiled}, true
}

// hasUnescapedDoubleStar reports whether pattern contains a "**" super wildcard
// outside a bracket expression and not escaped by a backslash. Such a segment
// can match across path separators under gobwas semantics.
func hasUnescapedDoubleStar(pattern string) bool {
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
		case c == '*' && i+1 < len(pattern) && pattern[i+1] == '*':
			return true
		}
	}
	return false
}

// excludeSeparatorFromNegatedClasses adds the separator to every negated
// character class, so a class such as "[!x]" cannot match a path separator
// although gobwas compiles classes without separator awareness.
func excludeSeparatorFromNegatedClasses(pattern string) string {
	var out strings.Builder
	out.Grow(len(pattern) + 4)
	escaped, inClass := false, false
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		switch {
		case escaped:
			out.WriteByte(c)
			escaped = false
		case c == '\\':
			out.WriteByte(c)
			escaped = true
		case inClass:
			if c == ']' {
				inClass = false
			}
			out.WriteByte(c)
			if c == '!' && i > 0 && pattern[i-1] == '[' {
				out.WriteByte('/')
			}
		case c == '[':
			inClass = true
			out.WriteByte(c)
		default:
			out.WriteByte(c)
		}
	}
	return out.String()
}

// splitWorkspacePatternSegments splits a slash pattern on separators outside
// bracket expressions and brace groups and not escaped by a backslash. It
// reports false when the split leaves an empty segment, which marks the pattern
// as malformed.
func splitWorkspacePatternSegments(pattern string) ([]string, bool) {
	var segments []string
	start := 0
	escaped, inClass := false, false
	braceDepth := 0
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
		case c == '{':
			braceDepth++
		case c == '}':
			if braceDepth > 0 {
				braceDepth--
			}
		case c == '/' && braceDepth == 0:
			segments = append(segments, pattern[start:i])
			start = i + 1
		}
	}
	segments = append(segments, pattern[start:])
	for _, segment := range segments {
		if segment == "" {
			return segments, false
		}
	}
	return segments, true
}

// normalizeWorkspacePattern normalises a configured pattern for workspace-
// relative matching: Windows separators and case are folded, leading "./" and
// "/" prefixes (which mark the workspace root) are dropped, and trailing
// separators are trimmed.
func normalizeWorkspacePattern(pattern string, windows bool) string {
	if windows {
		pattern = strings.ToLower(strings.ReplaceAll(pattern, "\\", "/"))
	}
	// Prefixes repeat: "/./docs" and "././docs" both mean "docs". Interior
	// "./" components are left untouched.
	for {
		switch {
		case strings.HasPrefix(pattern, "./"):
			pattern = pattern[2:]
		case strings.HasPrefix(pattern, "/"):
			pattern = pattern[1:]
		default:
			return trimTrailingPatternSeparators(pattern)
		}
	}
}

// trimTrailingPatternSeparators removes trailing separators that are not
// escaped, so "build/" and "docs/private/" match the same entries as their
// unadorned forms.
func trimTrailingPatternSeparators(pattern string) string {
	for len(pattern) > 0 && pattern[len(pattern)-1] == '/' {
		escaped := false
		for i := len(pattern) - 2; i >= 0 && pattern[i] == '\\'; i-- {
			escaped = !escaped
		}
		if escaped {
			break
		}
		pattern = pattern[:len(pattern)-1]
	}
	return pattern
}
