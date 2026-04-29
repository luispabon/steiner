package builtin

import (
	"context"
	"fmt"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tool"
)

// DisplayFileResult is the metadata-only result returned to the model for
// display_file calls. File content is intentionally absent.
type DisplayFileResult struct {
	// Path is the normalised display path of the file that was requested.
	Path string `json:"path"`
	// Status is a brief acknowledgement ("displayed" or "unavailable").
	Status string `json:"status"`
	// Message is an optional human-readable note.
	Message string `json:"message,omitempty"`
}

// NewDisplayFileTool creates a ToolDef for the display_file built-in.
//
// In interactive mode the tool emits a DisplayFile event so the TUI can render
// the file in an overlay. The tool result returned to the model contains only
// metadata — no file content enters the conversation history.
//
// In non-interactive mode (exec or any mode without a TUI event sink) the tool
// returns a bounded failure message instead of silently falling back to reading
// the file.
func NewDisplayFileTool(env Env) tool.ToolDef {
	return tool.ToolDef{
		Name:            "display_file",
		Description:     "Ask the TUI to display a file to the user in an overlay without including the file contents in the conversation. Use this instead of read when the goal is to show the file visually rather than analyse its content.",
		ParameterSchema: DisplayFileSchema(),
		Approval:        config.ApprovalModeAuto,
		Handler: func(ctx context.Context, input map[string]any) (any, error) {
			in, err := decodeInput[DisplayFileInput](input)
			if err != nil {
				return nil, fmt.Errorf("display_file: %w", err)
			}

			if in.Path == "" {
				return nil, fmt.Errorf("display_file: path is required")
			}

			if !env.Interactive || env.EventSink == nil {
				return &DisplayFileResult{
					Path:    in.Path,
					Status:  "unavailable",
					Message: "interactive file display is unavailable in non-TUI mode; use the read tool to retrieve file content instead",
				}, nil
			}

			_, err = env.PathPolicy.ResolvePath(in.Path, false)
			if err != nil {
				return nil, fmt.Errorf("display_file: %w", err)
			}

			absPath, err := absWorkspacePath(env.WorkDir, in.Path)
			if err != nil {
				return nil, fmt.Errorf("display_file: %w", err)
			}

			displayPath := relDisplayPath(env.WorkDir, absPath)

			env.EventSink.Emit(output.NewDisplayFileEvent(absPath, in.Language))

			return &DisplayFileResult{
				Path:    displayPath,
				Status:  "displayed",
				Message: "file is being shown to the user in the viewer overlay",
			}, nil
		},
	}
}
