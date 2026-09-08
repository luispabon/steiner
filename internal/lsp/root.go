package lsp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// absWorkspacePath resolves a tool-supplied file path against the workspace.
// Absolute paths are cleaned and returned as-is; relative paths are joined onto
// the workspace. Every LSP operation resolves its file this way before building
// cache keys, URIs or roots, so a relative and an absolute spelling of the same
// file behave identically.
func absWorkspacePath(workspace, file string) (string, error) {
	if strings.TrimSpace(file) == "" {
		return "", fmt.Errorf("file is required")
	}
	if filepath.IsAbs(file) {
		return filepath.Clean(file), nil
	}
	if workspace == "" {
		return "", fmt.Errorf("relative path %q requires a workspace directory", file)
	}
	return filepath.Join(workspace, file), nil
}

// resolveRoot walks up from the directory containing file, returning the first
// directory containing any of the markers. If no marker is found, it returns
// the workspace directory. It never returns the filesystem root.
func resolveRoot(file, workspace string, markers []string) string {
	dir := filepath.Dir(file)

	for {
		for _, marker := range markers {
			path := filepath.Join(dir, marker)
			if _, err := os.Stat(path); err == nil {
				return dir
			}
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached filesystem root; return workspace instead.
			return workspace
		}
		dir = parent
	}
}

// resolveWorkspaceRoot walks up from workspace itself, returning the first
// directory containing any of the markers. Used by workspace-mode symbol
// search, which has no file to route on. If no marker is found, it returns
// workspace. It never returns the filesystem root.
func resolveWorkspaceRoot(workspace string, markers []string) string {
	dir := workspace

	for {
		for _, marker := range markers {
			path := filepath.Join(dir, marker)
			if _, err := os.Stat(path); err == nil {
				return dir
			}
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached filesystem root; return workspace instead.
			return workspace
		}
		dir = parent
	}
}
