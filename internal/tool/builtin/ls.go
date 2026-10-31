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

	"github.com/luispabon/steiner/internal/tool"
)

// NewLSTool creates a ToolDef for the ls tool backed by Dive's ListDirectoryTool.
func NewLSTool(env Env) tool.ToolDef {
	lsTool := toolkit.NewListDirectoryTool(toolkit.ListDirectoryToolOptions{
		MaxEntries: maxLSLimit,
	})
	return tool.ToolDef{
		Name:            "ls",
		ParallelSafe:    true,
		Description:     "List directory contents. Use recursive sparingly. Use limit and offset for large directories.",
		ParameterSchema: LSSchema(),
		Handler: func(ctx context.Context, input map[string]any) (any, error) {
			in, err := decodeInput[LSInput](input)
			if err != nil {
				return nil, fmt.Errorf("ls: %w", err)
			}

			normalizeLS(&in)

			if in.Path == "" {
				in.Path = "."
			}

			absPath, err := env.PathPolicy.ResolveReadPath(in.Path)
			if err != nil {
				return nil, fmt.Errorf("ls: %w", err)
			}

			walkRoot, err := resolveSearchRoot(absPath, env.PathPolicy)
			if err != nil {
				return nil, fmt.Errorf("ls: %w", err)
			}

			excluder := tool.WorkspaceExcluder{}
			if env.Excluder != nil {
				excluder = *env.Excluder
			}

			if in.Recursive {
				return lsRecursive(ctx, walkRoot, in.Limit, in.Offset, excluder, env.PathPolicy)
			}
			return lsNonRecursive(ctx, lsTool, walkRoot, in.Limit, in.Offset, excluder, env.PathPolicy)
		},
	}
}

// lsNonRecursive lists a single directory using Dive's ListDirectoryTool.
func lsNonRecursive(
	ctx context.Context,
	lsTool *dive.TypedToolAdapter[*toolkit.ListDirectoryInput],
	absPath string,
	limit, offset int,
	excluder tool.WorkspaceExcluder,
	policy *tool.PathPolicy,
) (*Result, error) {
	diveResult, err := lsTool.Call(ctx, &toolkit.ListDirectoryInput{
		Path: absPath,
	})
	if err != nil {
		return nil, fmt.Errorf("ls: %w", err)
	}

	if diveResult.IsError {
		var b strings.Builder
		if diveResult.Display != "" {
			b.WriteString(diveResult.Display)
		}
		for _, c := range diveResult.Content {
			if c.Text == "" {
				continue
			}
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(c.Text)
		}
		return &Result{
			Output: b.String(),
		}, nil
	}

	contentText := ""
	if len(diveResult.Content) > 0 {
		contentText = diveResult.Content[0].Text
	}

	entries, err := parseDirectoryEntries(contentText)
	if err != nil {
		// Fail closed: the raw listing is not filtered by policy.
		return nil, fmt.Errorf("ls: %w", err)
	}

	entries = visibleEntries(entries, absPath, policy, excluder)

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

	result := pageResults(allLines, limit, offset)
	return &result, nil
}

// listedPathDenied reports whether policy blocks listing path. A listing only
// reveals a name, so special files are not rejected the way a read is; blocked
// paths and policy resolution errors still hide the entry.
func listedPathDenied(policy *tool.PathPolicy, path string) bool {
	if policy == nil {
		return false
	}
	_, err := policy.ResolveReadEntryPath(path)
	return err != nil
}

// visibleEntries drops entries whose resolved path policy denies and entries
// hidden by workspace exclusions, so a non-recursive listing never leaks a
// blocked or excluded entry. Filtering happens before pagination so limit and
// offset count only visible entries.
func visibleEntries(entries []toolkit.DirectoryEntry, dir string, policy *tool.PathPolicy, excluder tool.WorkspaceExcluder) []toolkit.DirectoryEntry {
	kept := entries[:0]
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name)
		if listedPathDenied(policy, path) {
			continue
		}
		if excluder.ExcludeEntry(dir, path, entry.IsDir) {
			continue
		}
		kept = append(kept, entry)
	}
	return kept
}

// lsRecursive walks a directory tree and returns relative paths, skipping
// excluded directories and directories denied by policy.
func lsRecursive(ctx context.Context, absPath string, limit, offset int, excluder tool.WorkspaceExcluder, policy *tool.PathPolicy) (*Result, error) {
	var allEntries []string

	err := filepath.WalkDir(absPath, func(path string, d fs.DirEntry, err error) error {
		if handled, walkErr := walkDirEntryError(path, absPath, d, err); handled {
			return walkErr
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

		if listedPathDenied(policy, path) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if excluder.ExcludeEntry(absPath, path, d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
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

	result := pageResults(allEntries, limit, offset)
	return &result, nil
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
