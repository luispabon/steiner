package builtin

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/luispabon/steiner/internal/tool"
)

// NewLSTool creates a ToolDef for the ls tool.
func NewLSTool(env Env) tool.ToolDef {
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

			policy := env.PathPolicy.WithVirtualTmpRequest(in.Path)
			absPath, err := policy.ResolveReadPath(in.Path)
			if err != nil {
				return nil, fmt.Errorf("ls: %w", err)
			}

			walkRoot, err := resolveSearchRoot(absPath, &policy)
			if err != nil {
				return nil, fmt.Errorf("ls: %w", err)
			}

			excluder := tool.WorkspaceExcluder{}
			if env.Excluder != nil {
				excluder = *env.Excluder
			}

			if in.Recursive {
				return lsRecursive(ctx, walkRoot, absPath, in.Limit, in.Offset, excluder, &policy)
			}
			return lsNonRecursive(ctx, walkRoot, absPath, in.Limit, in.Offset, excluder, &policy)
		},
	}
}

// lsEntry is a directory entry reduced to the fields ls prints.
type lsEntry struct {
	name  string
	isDir bool
}

// lsNonRecursive lists a single directory level. Entries denied by path policy
// or hidden by workspace exclusions are dropped before pagination, so limit and
// offset count only visible entries. The directory is read directly rather than
// through Dive's ListDirectoryTool because Dive truncates to maxLSLimit before
// any filtering, which hides visible entries that sort past the cap in a large
// directory.
func lsNonRecursive(
	ctx context.Context,
	absPath string,
	lexicalRoot string,
	limit, offset int,
	excluder tool.WorkspaceExcluder,
	policy *tool.PathPolicy,
) (*Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("ls: %w", err)
	}

	entries, failure := readDirectoryEntries(absPath)
	if failure != "" {
		// An unreadable or missing directory is tool output, not an error, as it
		// was when this listing came from Dive's ListDirectoryTool.
		return &Result{Output: failure}, nil
	}

	entries = visibleEntries(entries, absPath, lexicalRoot, policy, excluder)

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].isDir != entries[j].isDir {
			return entries[i].isDir
		}
		return entries[i].name < entries[j].name
	})

	allLines := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.name
		if e.isDir {
			name += "/"
		}
		allLines = append(allLines, name)
	}

	result := pageResults(allLines, limit, offset)
	return &result, nil
}

// readDirectoryEntries reads one directory level in name order. A non-empty
// failure is the user-facing message for a path ls cannot list, worded as Dive's
// ListDirectoryTool worded it.
func readDirectoryEntries(dir string) ([]lsEntry, string) {
	info, err := os.Stat(dir)
	if err != nil {
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return nil, fmt.Sprintf("Directory not found at path: %s", dir)
		case errors.Is(err, fs.ErrPermission):
			return nil, fmt.Sprintf("Permission denied when trying to access directory: %s", dir)
		default:
			return nil, fmt.Sprintf("Failed to access directory %s. %s", dir, err)
		}
	}
	if !info.IsDir() {
		return nil, fmt.Sprintf("Path %s is not a directory", dir)
	}

	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Sprintf("Failed to read directory %s. %s", dir, err)
	}

	entries := make([]lsEntry, 0, len(dirEntries))
	for _, entry := range dirEntries {
		entries = append(entries, lsEntry{name: entry.Name(), isDir: entry.IsDir()})
	}
	return entries, ""
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
// offset count only visible entries. lexicalRoot is the path the caller named
// before a symlink root was resolved, so configured exclusions written against
// that path keep applying.
func visibleEntries(entries []lsEntry, dir, lexicalRoot string, policy *tool.PathPolicy, excluder tool.WorkspaceExcluder) []lsEntry {
	kept := entries[:0]
	for _, entry := range entries {
		path := filepath.Join(dir, entry.name)
		if listedPathDenied(policy, path) {
			continue
		}
		if excludeSearchEntry(&excluder, policy, dir, lexicalRoot, path, entry.isDir) {
			continue
		}
		kept = append(kept, entry)
	}
	return kept
}

// lsRecursive walks a directory tree and returns relative paths, skipping
// excluded directories and directories denied by policy. absPath is the
// resolved walk root and lexicalRoot the path the caller named, so configured
// exclusions are validated against both forms when the root is a symlink.
func lsRecursive(ctx context.Context, absPath, lexicalRoot string, limit, offset int, excluder tool.WorkspaceExcluder, policy *tool.PathPolicy) (*Result, error) {
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

		if excludeSearchEntry(&excluder, policy, absPath, lexicalRoot, path, d.IsDir()) {
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
