package agent

import (
	"path/filepath"
	"strings"
)

func sanitizeScratchpadPath(path string) string {
	return sanitizeScratchpadPathWithRoot(path, "")
}

func sanitizeScratchpadPathWithRoot(path, root string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if !filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	root = strings.TrimSpace(root)
	if root != "" {
		root = filepath.Clean(root)
		if path == root {
			return "."
		}
		if trimmed := strings.TrimPrefix(path, root+string(filepath.Separator)); trimmed != path {
			return trimmed
		}
	}
	return filepath.Base(path)
}
