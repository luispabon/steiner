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
	"vendor",
	".cache",
	"dist",
	"build",
	"out",
	"target",
	".next",
	".nuxt",
}

// PathExcluder checks paths against exact prefix exclusions and glob patterns.
// Built-in heuristic exclusions are always active and appended automatically.
type PathExcluder struct {
	excludePaths    []string
	excludePatterns []string
}

// NewPathExcluder creates a PathExcluder with always-active built-in heuristic
// exclusions plus user-configured exclusions. User config appends to built-ins.
func NewPathExcluder(excludePaths, excludePatterns []string) PathExcluder {
	e := PathExcluder{
		excludePaths: append([]string(nil), excludePaths...),
	}
	e.excludePatterns = append(e.excludePatterns, builtinExcludeEntries...)
	e.excludePatterns = append(e.excludePatterns, excludePatterns...)
	return e
}

// ShouldExclude returns true if the path matches any exclusion rule.
// Exact prefix exclusions are checked first, then glob patterns are matched
// against each path component.
func (e PathExcluder) ShouldExclude(path string) bool {
	for _, prefix := range e.excludePaths {
		if strings.HasPrefix(path, prefix) {
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
