package builtin

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/deepnoodle-ai/dive/toolkit"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

// NewGlobTool creates a ToolDef for the glob tool backed by Dive's GlobTool.
func NewGlobTool(env Env) tool.ToolDef {
	globTool := toolkit.NewGlobTool()
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

			diveInput := &toolkit.GlobInput{
				Pattern: in.Pattern,
				Path:    absPath,
			}

			diveResult, err := globTool.Call(ctx, diveInput)
			if err != nil {
				return nil, fmt.Errorf("glob: %w", err)
			}

			if diveResult.IsError {
				return &Result{
					Output: diveText(diveResult),
				}, nil
			}

			outputText := strings.TrimSpace(diveText(diveResult))

			var allFiles []string
			if outputText != "" {
				allFiles = strings.Split(outputText, "\n")
			}

			sort.Strings(allFiles)

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
