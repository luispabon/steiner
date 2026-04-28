package builtin

import (
	"fmt"
	"path/filepath"
	"strings"
)

// absWorkspacePath resolves a path relative to the workspace directory.
// If p is empty, it returns the workspace directory itself.
// For tools that require a non-empty path (read, write, edit), callers
// should check for empty before calling.
func absWorkspacePath(workDir, p string) (string, error) {
	if strings.TrimSpace(p) == "" {
		if workDir == "" {
			return "", fmt.Errorf("path is required")
		}
		return filepath.Clean(workDir), nil
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p), nil
	}
	if workDir == "" {
		return "", fmt.Errorf("relative path %q requires a working directory", p)
	}
	return filepath.Join(workDir, p), nil
}

// relDisplayPath returns a human-readable representation of p relative to workDir.
// If p is not under workDir, it returns p as-is.
func relDisplayPath(workDir, p string) string {
	if workDir == "" || p == "" {
		return p
	}
	rel, err := filepath.Rel(workDir, p)
	if err != nil {
		return p
	}
	return rel
}
