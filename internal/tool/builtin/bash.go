package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/deepnoodle-ai/dive/toolkit"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

// BashResult is the result from a bash tool call.
type BashResult struct {
	ExitCode  int    `json:"exit_code"`
	Truncated bool   `json:"truncated,omitempty"`
	Output    string `json:"output"`
	Message   string `json:"message,omitempty"`
}

// NewBashTool creates a ToolDef for the bash tool backed by Dive's BashTool.
func NewBashTool(env Env) tool.ToolDef {
	return tool.ToolDef{
		Name:            "bash",
		Description:     "Run a shell command in the workspace. Prefer targeted commands. Set cwd when needed. Output may be truncated.",
		ParameterSchema: BashSchema(),
		Approval:        config.ApprovalModePrompt,
		Handler: func(ctx context.Context, input map[string]any) (any, error) {
			in, err := decodeInput[BashInput](input)
			if err != nil {
				return nil, fmt.Errorf("bash: %w", err)
			}

			NormalizeBash(&in)

			var cwd string
			if in.CWD != "" {
				cwd, err = env.PathPolicy.ResolveCWD(in.CWD)
				if err != nil {
					return nil, fmt.Errorf("bash: %w", err)
				}
			}

			bashTool := toolkit.NewBashTool(toolkit.BashToolOptions{
				MaxOutputLength: in.MaxOutputChars,
			})

			diveResult, err := bashTool.Call(ctx, &toolkit.BashInput{
				Command:          in.Command,
				Timeout:          in.TimeoutSeconds * 1000,
				WorkingDirectory: cwd,
			})
			if err != nil {
				return nil, fmt.Errorf("bash: %w", err)
			}

			text := ""
			if len(diveResult.Content) > 0 {
				text = diveResult.Content[0].Text
			}

			var diveOutput struct {
				Stdout     string `json:"stdout"`
				Stderr     string `json:"stderr"`
				ReturnCode int    `json:"return_code"`
			}
			if jsonErr := json.Unmarshal([]byte(text), &diveOutput); jsonErr != nil {
				return &BashResult{
					ExitCode: -1,
					Output:   text,
					Message:  text,
				}, nil
			}

			var output string
			if diveOutput.Stderr != "" {
				output = diveOutput.Stderr + "\n" + diveOutput.Stdout
			} else {
				output = diveOutput.Stdout
			}

			truncated := false
			message := ""
			if strings.HasSuffix(diveOutput.Stdout, "\n... (output truncated)") ||
				strings.HasSuffix(diveOutput.Stderr, "\n... (output truncated)") {
				truncated = true
				message = fmt.Sprintf("output truncated at %d characters", in.MaxOutputChars)
			}

			return &BashResult{
				ExitCode:  diveOutput.ReturnCode,
				Truncated: truncated,
				Output:    output,
				Message:   message,
			}, nil
		},
	}
}
