package agent

import (
	"path/filepath"
	"strings"
)

func sanitizeTrackedPath(path string) string {
	return sanitizeTrackedPathWithRoot(path, "")
}

func sanitizeTrackedPathWithRoot(path, root string) string {
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
