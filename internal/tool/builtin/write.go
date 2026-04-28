package builtin

import (
	"context"
	"fmt"

	"github.com/deepnoodle-ai/dive/toolkit"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

// NewWriteTool creates a ToolDef for the write tool backed by Dive's
// WriteFileTool.
func NewWriteTool(env Env) tool.ToolDef {
	writeTool := toolkit.NewWriteFileTool(toolkit.WriteFileToolOptions{
		WorkspaceDir: env.WorkDir,
	})
	return tool.ToolDef{
		Name:            "write",
		Description:     "Create or overwrite a whole file. For modifying existing files, prefer edit unless a full rewrite is intentional.",
		ParameterSchema: WriteSchema(),
		Approval:        config.ApprovalModePrompt,
		Handler: func(ctx context.Context, input map[string]any) (any, error) {
			in, err := decodeInput[WriteInput](input)
			if err != nil {
				return nil, fmt.Errorf("write: %w", err)
			}

			_, err = env.PathPolicy.ResolvePath(in.Path, true)
			if err != nil {
				return nil, fmt.Errorf("write: %w", err)
			}

			absPath, err := absWorkspacePath(env.WorkDir, in.Path)
			if err != nil {
				return nil, fmt.Errorf("write: %w", err)
			}

			diveResult, err := writeTool.Call(ctx, &toolkit.WriteFileInput{
				FilePath: absPath,
				Content:  in.Content,
			})
			if err != nil {
				return nil, fmt.Errorf("write: %w", err)
			}

			if diveResult.IsError {
				return &MutationResult{
					Path:   relDisplayPath(env.WorkDir, absPath),
					Output: diveText(diveResult),
				}, nil
			}

			return &MutationResult{
				Path:   relDisplayPath(env.WorkDir, absPath),
				Output: diveText(diveResult),
			}, nil
		},
	}
}
