package builtin

import (
	"context"
	"fmt"

	"github.com/luispabon/steiner/internal/tool"
)

// ApplyPatchInput is the typed input for the apply_patch tool.
type ApplyPatchInput struct {
	Patch  string `json:"patch"`
	DryRun bool   `json:"dry_run,omitempty"`
}

// NewApplyPatchTool creates a ToolDef for the apply_patch tool.
func NewApplyPatchTool(env Env) tool.ToolDef {
	return tool.ToolDef{
		Name:            "apply_patch",
		Description:     "Apply a Codex-style patch document to create, update, delete, or move files. Use this for file mutations. The patch must begin with \"*** Begin Patch\" and end with \"*** End Patch\".",
		ParameterSchema: ApplyPatchSchema(),
		Handler: func(ctx context.Context, input map[string]any) (any, error) {
			in, err := decodeInput[ApplyPatchInput](input)
			if err != nil {
				return nil, fmt.Errorf("apply_patch: %w", err)
			}
			_ = env
			_ = ctx
			_ = in
			return nil, fmt.Errorf("apply_patch: parser not implemented")
		},
	}
}
