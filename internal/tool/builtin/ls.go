package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/deepnoodle-ai/dive"
	"github.com/deepnoodle-ai/dive/toolkit"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

// NewLSTool creates a ToolDef for the ls tool backed by Dive's ListDirectoryTool.
func NewLSTool(env Env) tool.ToolDef {
	lsTool := toolkit.NewListDirectoryTool(toolkit.ListDirectoryToolOptions{
		MaxEntries: maxLSLimit,
	})
	return tool.ToolDef{
		Name:            "ls",
		Description:     "List directory contents. Use recursive sparingly. Use limit and offset for large directories.",
		ParameterSchema: LSSchema(),
		Approval:        config.ApprovalModeAuto,
		Handler: func(ctx context.Context, input map[string]any) (any, error) {
			in, err := decodeInput[LSInput](input)
			if err != nil {
				return nil, fmt.Errorf("ls: %w", err)
			}

			NormalizeLS(&in)

			if in.Path == "" {
				in.Path = "."
			}

			_, err = env.PathPolicy.ResolvePath(in.Path, false)
			if err != nil {
				return nil, fmt.Errorf("ls: %w", err)
			}

			absPath, err := absWorkspacePath(env.WorkDir, in.Path)
			if err != nil {
				return nil, fmt.Errorf("ls: %w", err)
			}

			if in.Recursive {
				return lsRecursive(ctx, absPath, in.Limit, in.Offset)
			}
			return lsNonRecursive(ctx, lsTool, absPath, in.Limit, in.Offset)
		},
	}
}

// lsNonRecursive lists a single directory using Dive's ListDirectoryTool.
func lsNonRecursive(
	ctx context.Context,
	lsTool *dive.TypedToolAdapter[*toolkit.ListDirectoryInput],
	absPath string,
	limit, offset int,
) (*Result, error) {
	diveResult, err := lsTool.Call(ctx, &toolkit.ListDirectoryInput{
		Path: absPath,
	})
	if err != nil {
		return nil, fmt.Errorf("ls: %w", err)
	}

	if diveResult.IsError {
		return &Result{
			Output: diveText(diveResult),
		}, nil
	}

	contentText := ""
	if len(diveResult.Content) > 0 {
		contentText = diveResult.Content[0].Text
	}

	entries, err := parseDirectoryEntries(contentText)
	if err != nil {
		return &Result{
			Output: contentText,
		}, nil
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return entries[i].Name < entries[j].Name
	})

	var allLines []string
	for _, e := range entries {
		name := e.Name
		if e.IsDir {
			name += "/"
		}
		allLines = append(allLines, name)
	}

	return pageResults(allLines, limit, offset), nil
}

// lsRecursive walks a directory tree and returns relative paths.
func lsRecursive(ctx context.Context, absPath string, limit, offset int) (*Result, error) {
	var allEntries []string

	err := filepath.WalkDir(absPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}

		rel, err := filepath.Rel(absPath, path)
		if err != nil {
			return err
		}

		if rel == "." {
			return nil
		}

		name := rel
		if d.IsDir() {
			name += "/"
		}
		allEntries = append(allEntries, name)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("ls: %w", err)
	}

	sort.Strings(allEntries)

	return pageResults(allEntries, limit, offset), nil
}

// pageResults builds a Result from a sorted list of entry names with pagination.
func pageResults(allLines []string, limit, offset int) *Result {
	total := len(allLines)
	start := offset
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}
	page := allLines[start:end]

	result := Result{
		Output:   strings.Join(page, "\n"),
		Returned: len(page),
	}

	if end < total {
		result.NextOffset = offset + limit
	}

	return &result
}

// parseDirectoryEntries extracts the JSON array from Dive's ListDirectoryTool text output.
func parseDirectoryEntries(text string) ([]toolkit.DirectoryEntry, error) {
	parts := strings.SplitN(text, "\n\n", 2)
	if len(parts) < 2 {
		return nil, fmt.Errorf("unexpected output format")
	}
	var entries []toolkit.DirectoryEntry
	if err := json.Unmarshal([]byte(parts[1]), &entries); err != nil {
		return nil, fmt.Errorf("parse directory entries: %w", err)
	}
	return entries, nil
}
