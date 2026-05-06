package builtin

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/tool"
	"github.com/luispabon/steiner/internal/tool/builtin/patchdoc"
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
		Description:     "Use apply_patch for all file mutations. Provide a Codex-style patch document that begins with \"*** Begin Patch\" and ends with \"*** End Patch\".",
		ParameterSchema: ApplyPatchSchema(),
		Handler: func(ctx context.Context, input map[string]any) (any, error) {
			in, err := decodeInput[ApplyPatchInput](input)
			if err != nil {
				return nil, fmt.Errorf("apply_patch: %w", err)
			}

			if strings.TrimSpace(in.Patch) == "" {
				return nil, errors.New("apply_patch: patch is required")
			}

			parsed, err := patchdoc.Parse(in.Patch)
			if err != nil {
				return &ApplyPatchResult{
					DryRun:      in.DryRun,
					HunksFailed: 1,
					Output:      err.Error(),
				}, nil
			}

			result, err := patchdoc.ApplyPatch(env.WorkDir, *parsed, in.DryRun, patchdoc.OSFS{})
			if err != nil {
				return &ApplyPatchResult{
					DryRun:      in.DryRun,
					HunksFailed: 1,
					Output:      err.Error(),
				}, nil
			}

			return newApplyPatchResult(result), nil
		},
	}
}
