package builtin

import (
	"context"
	"fmt"

	"github.com/deepnoodle-ai/dive/toolkit"

	"github.com/luispabon/steiner/internal/tool"
)

// NewEditTool creates a ToolDef for the edit tool backed by Dive's EditTool.
func NewEditTool(env Env) tool.ToolDef {
	editTool := toolkit.NewEditTool()
	return tool.ToolDef{
		Name:            "edit",
		Description:     "Replace exact text in one file. Use read first and include enough surrounding context in old_string to make the match unique. Fails if old_string is absent or ambiguous unless replace_all is true.",
		ParameterSchema: EditSchema(),
		Handler: func(ctx context.Context, input map[string]any) (any, error) {
			in, err := decodeInput[EditInput](input)
			if err != nil {
				return nil, fmt.Errorf("edit: %w", err)
			}

			_, err = env.PathPolicy.ResolvePath(in.Path, true)
			if err != nil {
				return nil, fmt.Errorf("edit: %w", err)
			}

			absPath, err := absWorkspacePath(env.WorkDir, in.Path)
			if err != nil {
				return nil, fmt.Errorf("edit: %w", err)
			}

			diveResult, err := editTool.Call(ctx, &toolkit.EditInput{
				FilePath:   absPath,
				OldString:  in.OldString,
				NewString:  in.NewString,
				ReplaceAll: in.ReplaceAll,
			})
			if err != nil {
				return nil, fmt.Errorf("edit: %w", err)
			}

			if diveResult.IsError {
				return &MutationResult{
					Path:   relDisplayPath(env.WorkDir, absPath),
					Output: diveText(diveResult),
				}, nil
			}

			return &MutationResult{
				Path:    relDisplayPath(env.WorkDir, absPath),
				Output:  diveText(diveResult),
				Mutated: true,
			}, nil
		},
	}
}
