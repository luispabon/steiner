package builtin

import (
	"context"
	"fmt"

	"github.com/luispabon/steiner/internal/tool"
)

// NewGrepTool creates a ToolDef for the grep tool.
func NewGrepTool(env Env) tool.ToolDef {
	return tool.ToolDef{
		Name:            "grep",
		Description:     `Search file contents with paginated logical results. Use output_mode="files_with_matches" to locate relevant files, output_mode="count" for per-file counts, and output_mode="content" with context to inspect matches.`,
		ParameterSchema: GrepSchema(),
		Handler: func(ctx context.Context, input map[string]any) (any, error) {
			in, err := decodeInput[GrepInput](input)
			if err != nil {
				return nil, fmt.Errorf("grep: %w", err)
			}

			NormalizeGrep(&in)

			if in.OutputMode == "" {
				in.OutputMode = "content"
			}
			if in.LineNumbers == nil {
				v := true
				in.LineNumbers = &v
			}
			if in.Context > 0 {
				if in.BeforeContext == 0 {
					in.BeforeContext = in.Context
				}
				if in.AfterContext == 0 {
					in.AfterContext = in.Context
				}
			}

			validModes := map[string]bool{
				"content":            true,
				"files_with_matches": true,
				"count":              true,
			}
			if !validModes[in.OutputMode] {
				return nil, fmt.Errorf("grep: invalid output_mode: %q", in.OutputMode)
			}

			searchPath := in.Path
			if searchPath == "" {
				searchPath = "."
			}
			_, err = env.PathPolicy.ResolvePath(searchPath, false)
			if err != nil {
				return nil, fmt.Errorf("grep: %w", err)
			}

			absPath, err := absWorkspacePath(env.WorkDir, searchPath)
			if err != nil {
				return nil, fmt.Errorf("grep: %w", err)
			}

			showLines := true
			if in.LineNumbers != nil {
				showLines = *in.LineNumbers
			}

			files, err := grepSearch(ctx, absPath, searchPath, in.Pattern, in.CaseInsensitive, in.Multiline, in.Glob, in.Type, env.Excluder)
			if err != nil {
				return nil, fmt.Errorf("grep: %w", err)
			}

			result := buildGrepResult(files, in.OutputMode, showLines, in.BeforeContext, in.AfterContext, in.Offset, in.HeadLimit)
			return result, nil
		},
	}
}
