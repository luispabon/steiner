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
// directory. Filtering stops once the page and its next-offset signal are
// settled, so a large directory does not pay a policy lookup per entry.
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

	dirEntries, failure := readDirectoryEntries(absPath)
	if failure != "" {
		// An unreadable or missing directory is tool output, not an error, as it
		// was when this listing came from Dive's ListDirectoryTool.
		return &Result{Output: failure}, nil
	}

	entries, err := visiblePageEntries(ctx, dirEntries, absPath, lexicalRoot, policy, excluder, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("ls: %w", err)
	}

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
func readDirectoryEntries(dir string) ([]fs.DirEntry, string) {
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
	return dirEntries, ""
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

// visiblePageEntries returns the visible entries that can affect the requested
// page, in listing order: directories before files, each group in the name
// order os.ReadDir produced. Entries denied by path policy or hidden by
// workspace exclusions are dropped. Scanning stops as soon as offset+limit+1
// visible entries are known, because pageResults needs no more to build the
// page and report NextOffset; later files are skipped without a policy lookup
// once the file group already covers any page. lexicalRoot is the path the
// caller named before a symlink root was resolved, so configured exclusions
// written against that path keep applying.
func visiblePageEntries(ctx context.Context, dirEntries []fs.DirEntry, dir, lexicalRoot string, policy *tool.PathPolicy, excluder tool.WorkspaceExcluder, limit, offset int) ([]lsEntry, error) {
	target := pageVisibleTarget(len(dirEntries), limit, offset)
	var dirs, files []lsEntry
	for _, entry := range dirEntries {
		if len(dirs) >= target {
			break
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		isDir := entry.IsDir()
		if !isDir && len(files) >= target {
			// Enough files to fill any page: the rest sort later, so they cannot
			// displace these, and they do not join the directory group.
			continue
		}
		name := entry.Name()
		path := filepath.Join(dir, name)
		if listedPathDenied(policy, path) {
			continue
		}
		if excludeSearchEntry(&excluder, policy, dir, lexicalRoot, path, isDir) {
			continue
		}
		if isDir {
			dirs = append(dirs, lsEntry{name: name, isDir: true})
			continue
		}
		files = append(files, lsEntry{name: name})
	}
	return append(dirs, files...), nil
}

// pageVisibleTarget is how many visible entries pageResults needs to serve the
// requested page and decide whether a following page exists: offset+limit+1,
// clamped to what the directory can supply. Zero means the page stays empty
// whatever the visible count is, so no entry needs filtering at all.
func pageVisibleTarget(available, limit, offset int) int {
	if offset < 0 {
		offset = 0
	}
	if offset >= available {
		return 0
	}
	if limit >= available-offset {
		return available + 1
	}
	return offset + limit + 1
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
