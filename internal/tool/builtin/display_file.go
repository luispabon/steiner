package builtin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
		Handler: func(_ context.Context, input map[string]any) (any, error) {
			in, err := decodeInput[DisplayFileInput](input)
			if err != nil {
				return nil, fmt.Errorf("display_file: %w", err)
			}

			normalizeDisplayFile(&in)

			if in.Path == "" {
				return nil, fmt.Errorf("display_file: path is required")
			}

			absPath, err := env.PathPolicy.ResolveReadPath(in.Path)
			if err != nil {
				return nil, fmt.Errorf("display_file: %w", err)
			}
			displayPath := env.PathPolicy.DisplayPath(absPath)
			if !strings.HasPrefix(displayPath, "/") || strings.HasPrefix(displayPath, env.WorkDir) {
				displayPath = relDisplayPath(env.WorkDir, displayPath)
			}

			if !env.Interactive || env.EventSink == nil {
				return &DisplayFileResult{
					Path:    displayPath,
					Status:  "unavailable",
					Message: "interactive file display is unavailable in non-TUI mode; use the read tool to retrieve file content instead",
				}, nil
			}

			contents, truncated, previewKind, err := readDisplayFilePreview(absPath, in.Offset, in.Limit)
			if err != nil {
				return nil, fmt.Errorf("display_file: %w", err)
			}

			preview := output.PreviewDocument{
				Kind:      output.PreviewFormatKindFile,
				Path:      displayPath,
				LineLimit: in.Limit,
				StartLine: in.Offset,
			}
			switch previewKind {
			case "image":
				preview.Language = "plain"
				preview.Lines = plainTextPreviewLines("image preview unavailable in text overlay; use read for visual inspection")
			case "binary":
				preview.Language = "plain"
				preview.Lines = plainTextPreviewLines("binary file omitted from text preview; use read for content inspection")
			default:
				preview = output.FormatFilePreviewWithLimit(displayPath, contents, in.Limit)
				preview.Truncated = truncated
				preview.LineLimit = in.Limit
				preview.StartLine = in.Offset
			}

			env.EventSink.Emit(output.NewDisplayFileEvent(output.DisplayFilePayload{
				Path:    displayPath,
				Offset:  in.Offset,
				Limit:   in.Limit,
				Preview: preview,
			}))

			return &DisplayFileResult{
				Path:    displayPath,
				Status:  "displayed",
				Message: "file is being shown to the user in the viewer overlay",
			}, nil
		},
	}
}

// plainTextPreviewLines builds a single-line plain-text preview body carrying msg.
func plainTextPreviewLines(msg string) []output.PreviewLine {
	return []output.PreviewLine{{
		Kind:  output.PreviewLineKindText,
		Spans: []output.PreviewSpan{{Text: msg}},
	}}
}

func readDisplayFilePreview(path string, offset, limit int) (string, bool, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false, "", fmt.Errorf("read file: %w", err)
	}

	if IsImageExtension(filepath.Ext(path)) {
		return "", false, "image", nil
	}
	if isBinary(data) {
		return "", false, "binary", nil
	}

	lines := splitDisplayFileLines(string(data))
	if len(lines) == 0 {
		return "", false, "", nil
	}

	start := offset - 1
	if start < 0 {
		start = 0
	}
	if start > len(lines) {
		start = len(lines)
	}

	end := start + limit
	if end > len(lines) {
		end = len(lines)
	}

	selected := lines[start:end]
	truncated := end < len(lines)
	return strings.Join(selected, "\n"), truncated, "", nil
}

func splitDisplayFileLines(text string) []string {
	if text == "" {
		return nil
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	if strings.HasSuffix(text, "\n") {
		lines = lines[:len(lines)-1]
	}
	return lines
}
