package builtin

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

// NewGlobTool creates a ToolDef for the glob tool backed by a custom
// filepath.WalkDir-based walker that respects path exclusions.
func NewGlobTool(env Env) tool.ToolDef {
	return tool.ToolDef{
		Name:            "glob",
		Description:     "Find files by glob pattern. Use limit and offset to page through large result sets.",
		ParameterSchema: GlobSchema(),
		Approval:        config.ApprovalModeAuto,
		Handler: func(ctx context.Context, input map[string]any) (any, error) {
			in, err := decodeInput[GlobInput](input)
			if err != nil {
				return nil, fmt.Errorf("glob: %w", err)
			}

			NormalizeGlob(&in)

			if in.Path == "" {
				in.Path = "."
			}

			_, err = env.PathPolicy.ResolvePath(in.Path, false)
			if err != nil {
				return nil, fmt.Errorf("glob: %w", err)
			}

			absPath, err := absWorkspacePath(env.WorkDir, in.Path)
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

			total := len(allFiles)
			start := in.Offset
			if start > total {
				start = total
			}
			end := start + in.Limit
			if end > total {
				end = total
			}
			page := allFiles[start:end]

			result := Result{
				Output:   strings.Join(page, "\n"),
				Returned: len(page),
			}

			if end < total {
				result.NextOffset = in.Offset + in.Limit
			}

			return result, nil
		},
	}
}

// globWalk walks the directory tree rooted at root and returns all files
// whose base name matches pattern. Excluded directories are not traversed.
// Each matched path is validated through PathPolicy before being included.
// The returned paths are relative to root and sorted alphabetically.
func globWalk(root, pattern string, excluder tool.PathExcluder, policy *tool.PathPolicy) ([]string, error) {
	var matches []string

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return filepath.SkipDir
		}

		if excluder.ShouldExclude(path) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if d.IsDir() {
			return nil
		}

		matched, err := filepath.Match(pattern, d.Name())
		if err != nil {
			return err
		}
		if !matched {
			return nil
		}

		_, err = policy.ResolvePath(path, false)
		if err != nil {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		matches = append(matches, rel)
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Strings(matches)
	return matches, nil
}
