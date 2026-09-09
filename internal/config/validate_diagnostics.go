package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// validateDiagnosticsConfig checks the diagnostics section. projectRoot may be
// empty, in which case the containment check is skipped; callers that know the
// root (config.Load) always pass it.
func validateDiagnosticsConfig(problems *[]string, cfg DiagnosticsConfig, projectRoot string) {
	if !cfg.Enabled {
		return
	}
	if cfg.RetentionDays < 1 {
		*problems = append(*problems, "diagnostics.retention_days must be greater than zero when enabled")
	}
	dir := strings.TrimSpace(cfg.Dir)
	if dir == "" {
		*problems = append(*problems, "diagnostics.dir is required when enabled")
		return
	}
	if insideProjectRoot(dir, projectRoot) {
		*problems = append(*problems, fmt.Sprintf("diagnostics.dir %q must not resolve inside the project root %q: diagnostics are user state, not repo content", dir, projectRoot))
	}
}

// insideProjectRoot reports whether dir resolves to projectRoot or a path
// beneath it. Both paths are made absolute first so a relative diagnostics.dir
// is judged against the same base.
func insideProjectRoot(dir, projectRoot string) bool {
	projectRoot = strings.TrimSpace(projectRoot)
	if projectRoot == "" {
		return false
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	absRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return false
	}
	absDir = resolveExistingPath(absDir)
	absRoot = resolveExistingPath(absRoot)
	rel, err := filepath.Rel(absRoot, absDir)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// resolveExistingPath resolves symlinks in the existing portion of path while
// preserving a nonexistent leaf. This prevents a symlinked diagnostics
// directory, including one with a new child path, from bypassing containment.
func resolveExistingPath(path string) string {
	path = filepath.Clean(path)
	current := path
	var suffix []string
	for {
		if _, err := os.Lstat(current); err == nil {
			resolved, err := filepath.EvalSymlinks(current)
			if err != nil {
				return path
			}
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return filepath.Clean(resolved)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return path
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}
