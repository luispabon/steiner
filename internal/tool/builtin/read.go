package builtin

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/deepnoodle-ai/dive/toolkit"

	"github.com/luispabon/steiner/internal/tool"
)

// NewReadTool creates a ToolDef for the read tool backed by Dive's ReadFileTool.
func NewReadTool(env Env) tool.ToolDef {
	readTool := toolkit.NewReadFileTool()
	return tool.ToolDef{
		Name:            "read",
		Description:     "Read a file or part of a file. Prefer offset and limit for large files. Use grep or glob first when locating code. Returns line-numbered content and pagination metadata.",
		ParameterSchema: ReadSchema(),
		Handler: func(ctx context.Context, input map[string]any) (any, error) {
			in, err := decodeInput[ReadInput](input)
			if err != nil {
				return nil, fmt.Errorf("read: %w", err)
			}

			NormalizeRead(&in)

			_, err = env.PathPolicy.ResolveReadPath(in.Path)
			if err != nil {
				return nil, fmt.Errorf("read: %w", err)
			}

			absPath, err := absWorkspacePath(env.WorkDir, in.Path)
			if err != nil {
				return nil, fmt.Errorf("read: %w", err)
			}

			diveResult, err := readTool.Call(ctx, &toolkit.ReadFileInput{
				FilePath: absPath,
				Offset:   in.Offset,
				Limit:    in.Limit,
			})
			if err != nil {
				return nil, fmt.Errorf("read: %w", err)
			}

			contentText := ""
			if len(diveResult.Content) > 0 {
				contentText = diveResult.Content[0].Text
			}

			if diveResult.IsError {
				return &ReadResult{
					Path:   relDisplayPath(env.WorkDir, absPath),
					Output: contentText,
				}, nil
			}

			data, err := os.ReadFile(absPath)
			if err != nil {
				return nil, fmt.Errorf("read: %w", err)
			}

			totalLines := 0
			if len(data) > 0 {
				totalLines = bytes.Count(data, []byte{'\n'})
				if data[len(data)-1] != '\n' {
					totalLines++
				}
			}

			outputLines := strings.Split(contentText, "\n")
			if len(outputLines) > 0 && outputLines[len(outputLines)-1] == "" {
				outputLines = outputLines[:len(outputLines)-1]
			}
			numLines := len(outputLines)

			startLine := in.Offset
			endLine := startLine + numLines - 1
			if numLines == 0 {
				endLine = 0
			}

			result := ReadResult{
				Path:       relDisplayPath(env.WorkDir, absPath),
				FileHash:   fileContentHash(data),
				StartLine:  startLine,
				EndLine:    endLine,
				TotalLines: totalLines,
				Output:     contentText,
			}

			if endLine > 0 && endLine < totalLines {
				result.NextOffset = endLine + 1
			}

			return result, nil
		},
	}
}
