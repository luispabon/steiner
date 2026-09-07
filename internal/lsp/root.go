package lsp

import (
	"os"
	"path/filepath"
)

// resolveRoot walks up from the directory containing file, returning the first
// directory containing any of the markers. If no marker is found, it returns
// the workspace directory. It never returns the filesystem root.
func resolveRoot(file, workspace string, markers []string) (string, error) {
	dir := filepath.Dir(file)

	for {
		for _, marker := range markers {
			path := filepath.Join(dir, marker)
			if _, err := os.Stat(path); err == nil {
				return dir, nil
			}
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached filesystem root; return workspace instead.
			return workspace, nil
		}
		dir = parent
	}
}
