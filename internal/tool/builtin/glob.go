package builtin

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gobwas/glob"

	"github.com/luispabon/steiner/internal/tool"
)

// NewGlobTool creates a ToolDef for the glob tool backed by a custom
// filepath.WalkDir-based walker that respects path exclusions.
func NewGlobTool(env Env) tool.ToolDef {
	return tool.ToolDef{
		Name:            "glob",
		ParallelSafe:    true,
		Description:     "Find files by glob pattern. Use limit and offset to page through large result sets.",
		ParameterSchema: GlobSchema(),
		Handler: func(_ context.Context, input map[string]any) (any, error) {
			in, err := decodeInput[GlobInput](input)
			if err != nil {
				return nil, fmt.Errorf("glob: %w", err)
			}

			normalizeGlob(&in)

			if in.Path == "" {
				in.Path = "."
			}

			absPath, err := env.PathPolicy.ResolveReadPath(in.Path)
			if err != nil {
				return nil, fmt.Errorf("glob: %w", err)
			}

			excluder := tool.PathExcluder{}
			if env.Excluder != nil {
				excluder = *env.Excluder
			}

			allFiles, err := globWalk(absPath, in.Pattern, excluder, env.PathPolicy)
			if err != nil {
				return nil, fmt.Errorf("glob: %w", err)
			}

			return pageResults(allFiles, in.Limit, in.Offset), nil
		},
	}
}

// globWalk walks the directory tree rooted at root and returns all files
// whose relative path matches pattern (using gobwas/glob for full glob
// semantics including ** and {a,b} alternation). Excluded directories are
// not traversed. Each matched path is validated through PathPolicy before
// being included. The returned paths are relative to root, use forward
// slashes, and are sorted alphabetically. The walk stops early when
// maxGlobLimit matches are collected.
func globWalk(root, pattern string, excluder tool.PathExcluder, policy *tool.PathPolicy) ([]string, error) {
	matchers, err := compileGlobMatchers(pattern)
	if err != nil {
		return nil, err
	}

	var matches []string

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return filepath.SkipDir
		}

		excludePath := path
		if policy != nil {
			excludePath = policy.DisplayPath(path)
		}
		if excluder.ShouldExclude(excludePath) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if d.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if !matchesAnyGlob(matchers, filepath.ToSlash(rel)) {
			return nil
		}

		_, err = policy.ResolveReadPath(path)
		if err != nil {
			return nil
		}

		matches = append(matches, rel)
		if len(matches) >= maxGlobLimit {
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Strings(matches)
	return matches, nil
}

func compileGlobMatchers(pattern string) ([]glob.Glob, error) {
	patterns := expandZeroDirectoryDoublestar(pattern)
	matchers := make([]glob.Glob, 0, len(patterns))
	for _, expanded := range patterns {
		matcher, err := glob.Compile(expanded, '/')
		if err != nil {
			return nil, fmt.Errorf("compile glob %q: %w", expanded, err)
		}
		matchers = append(matchers, matcher)
	}
	return matchers, nil
}

func matchesAnyGlob(matchers []glob.Glob, path string) bool {
	for _, matcher := range matchers {
		if matcher.Match(path) {
			return true
		}
	}
	return false
}

func expandZeroDirectoryDoublestar(pattern string) []string {
	patterns := map[string]struct{}{
		pattern: {},
	}
	queue := []string{pattern}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		for searchFrom := 0; searchFrom < len(current); {
			idx := strings.Index(current[searchFrom:], "**/")
			if idx < 0 {
				break
			}
			idx += searchFrom
			next := current[:idx] + current[idx+3:]
			if _, exists := patterns[next]; exists {
				searchFrom = idx + 1
				continue
			}
			patterns[next] = struct{}{}
			queue = append(queue, next)
			searchFrom = idx + 1
		}
	}

	expanded := make([]string, 0, len(patterns))
	for candidate := range patterns {
		expanded = append(expanded, candidate)
	}
	sort.Strings(expanded)
	return expanded
}
