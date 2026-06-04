package builtin

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/luispabon/steiner/internal/tool"
)

// BashResult is the result from a bash tool call.
type BashResult struct {
	ExitCode  int    `json:"exit_code"`
	Truncated bool   `json:"truncated,omitempty"`
	Output    string `json:"output"`
	Message   string `json:"message,omitempty"`
}

// NewBashTool creates a ToolDef for the bash tool backed by a local BashSession.
func NewBashTool(env Env) tool.ToolDef {
	return tool.ToolDef{
		Name:            "bash",
		Description:     "Run a shell command in the workspace. Prefer targeted commands. Set cwd instead of running cd commands when needed. Output may be truncated.",
		ParameterSchema: BashSchema(),
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

			// Build the command string, optionally prefixed with a cd.
			command := in.Command
			if cwd != "" {
				command = fmt.Sprintf("cd %q && %s", cwd, in.Command)
			}

			// Apply timeout via context.
			timeout := time.Duration(in.TimeoutSeconds) * time.Second
			execCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			session := NewBashSession()
			session.CommandWrapper = env.CommandWrapper
			if err := session.Start(); err != nil {
				return nil, fmt.Errorf("bash: start session: %w", err)
			}
			defer func() { _ = session.Close() }()

			stdout, stderr, exitCode, execErr := session.Execute(execCtx, command)
			if execErr != nil {
				// Timeout or session error — return as a result rather than a Go error
				// so the model receives the failure information.
				return &BashResult{
					ExitCode: -1,
					Output:   execErr.Error(),
					Message:  execErr.Error(),
				}, nil
			}

			// Combine stdout and stderr into a single output string.
			var output strings.Builder
			stdout = strings.TrimSuffix(stdout, "\n")
			stderr = strings.TrimSpace(stderr)
			if stdout != "" {
				output.WriteString(stdout)
			}
			if stderr != "" {
				if output.Len() > 0 {
					output.WriteString("\n")
				}
				output.WriteString("[stderr]\n")
				output.WriteString(stderr)
			}

			// Detect truncation: maybeTruncate appends "[output truncated]" when hit.
			truncated := strings.Contains(stdout, "[output truncated]") ||
				strings.Contains(stderr, "[output truncated]")

			message := ""
			if truncated {
				message = fmt.Sprintf("output truncated at %d characters", in.MaxOutputChars)
			}

			// Apply the caller-specified max_output_chars cap on the combined output.
			combined := output.String()
			if in.MaxOutputChars > 0 && len(combined) > in.MaxOutputChars {
				combined = combined[:in.MaxOutputChars]
				truncated = true
				message = fmt.Sprintf("output truncated at %d characters", in.MaxOutputChars)
			}

			return &BashResult{
				ExitCode:  exitCode,
				Truncated: truncated,
				Output:    combined,
				Message:   message,
			}, nil
		},
	}
}
