package builtin

import (
	"bytes"
	"context"
	"fmt"
	"os"

	"github.com/luispabon/steiner/internal/tool"
)

// NewEditTool creates a ToolDef for the edit tool.
func NewEditTool(env Env) tool.ToolDef {
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

			content, err := os.ReadFile(absPath)
			if err != nil {
				return nil, fmt.Errorf("edit: %w", err)
			}

			if isBinary(content) {
				return &MutationResult{
					Path:   relDisplayPath(env.WorkDir, absPath),
					Output: "edit: file appears to be binary",
				}, nil
			}

			if in.OldString == "" {
				return &MutationResult{
					Path:   relDisplayPath(env.WorkDir, absPath),
					Output: "edit: old_string is empty",
				}, nil
			}

			oldBytes := []byte(in.OldString)
			newBytes := []byte(in.NewString)
			matchCount := bytes.Count(content, oldBytes)

			switch {
			case matchCount == 0:
				return &MutationResult{
					Path:   relDisplayPath(env.WorkDir, absPath),
					Output: "edit: no match for old_string",
				}, nil
			case matchCount > 1 && !in.ReplaceAll:
				return &MutationResult{
					Path:   relDisplayPath(env.WorkDir, absPath),
					Output: fmt.Sprintf("edit: ambiguous match for old_string (found %d occurrences)", matchCount),
				}, nil
			}

			replaced := content
			if in.ReplaceAll {
				replaced = bytes.ReplaceAll(content, oldBytes, newBytes)
			} else {
				replaced = bytes.Replace(content, oldBytes, newBytes, 1)
			}

			if err := os.WriteFile(absPath, replaced, 0o644); err != nil {
				return nil, fmt.Errorf("edit: write %q: %w", in.Path, err)
			}

			output := "edit: replaced 1 occurrence"
			if in.ReplaceAll {
				output = fmt.Sprintf("edit: replaced %d occurrence", matchCount)
				if matchCount != 1 {
					output = fmt.Sprintf("edit: replaced %d occurrences", matchCount)
				}
			}

			return &MutationResult{
				Path:    relDisplayPath(env.WorkDir, absPath),
				Output:  output,
				Mutated: true,
			}, nil
		},
	}
}
