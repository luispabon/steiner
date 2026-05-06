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
		Description:     "Apply one or more file mutations atomically. Use apply_patch for all file edits — never use bash, sed, or cat for ad-hoc file changes.\n\nPatch format:\n*** Begin Patch\n*** Add File: path/to/new/file\n+line content\n*** Update File: path/to/existing/file\n@@ optional_literal_anchor\n context line\n-removed line\n+new line\n*** Delete File: path/to/file\n*** End Patch\n\nRules: paths relative to workspace root; every new-file line prefixed with \"+\"; update hunk body lines must start with \" \" for context, \"+\" for additions, or \"-\" for removals; text after @@ is matched literally against a source line; bare @@ is valid when no literal anchor is needed; use enough context to identify the location; multiple @@ chunks allowed per file.",
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
