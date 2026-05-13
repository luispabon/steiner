package builtin

import (
	"bufio"
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

			_, err = env.PathPolicy.ResolvePath(in.Path, false)
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

			totalLines, _ := countFileLines(absPath)

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

// countFileLines returns the number of lines in a file.
func countFileLines(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("open file: %w", err)
	}
	defer func() {
		_ = f.Close()
	}()

	scanner := bufio.NewScanner(f)
	count := 0
	for scanner.Scan() {
		count++
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("scan file: %w", err)
	}
	return count, nil
}
