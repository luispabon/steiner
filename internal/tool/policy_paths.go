package tool

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func normalizePolicyPath(root, raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	path := expandTilde(raw)
	if !filepath.IsAbs(path) {
		if root == "" {
			path = filepath.Clean(path)
		} else {
			path = filepath.Join(root, path)
		}
	}
	return filepath.Clean(path)
}

func expandTilde(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[1:])
		}
	}
	return path
}

// PathWithinRoot reports whether path is lexically contained within root. Both
// are expected to be cleaned absolute paths; symlinks are not resolved.
func PathWithinRoot(root, path string) bool {
	return pathWithinRoot(root, path)
}

// canonicalWritePathWithin reports whether path resolves under the lexical
// allowlist prefix's intended location beneath the resolved policy root. The
// allowlist prefix itself is not resolved, so a symlink in that prefix cannot
// silently redefine the directory that plan mode permits.
func canonicalWritePathWithin(root, allowed, path string) (bool, error) {
	resolvedRoot, err := resolvePathWithMissing(root)
	if err != nil {
		return false, fmt.Errorf("resolve policy root: %w", err)
	}
	rel, err := filepath.Rel(root, allowed)
	if err != nil || !pathWithinRoot(root, allowed) {
		return false, fmt.Errorf("allowlist prefix is outside policy root")
	}
	intended := filepath.Join(resolvedRoot, rel)
	resolvedPath, err := resolvePathWithMissing(path)
	if err != nil {
		return false, fmt.Errorf("resolve policy path: %w", err)
	}
	return pathWithinRoot(intended, resolvedPath), nil
}

// resolvePathWithMissing resolves path, preserving its missing suffix after
// resolving the nearest existing ancestor. It fails on errors other than a
// missing path component.
func resolvePathWithMissing(path string) (string, error) {
	target := filepath.Clean(path)
	var missing []string
	for {
		resolved, err := filepath.EvalSymlinks(target)
		if err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return filepath.Clean(resolved), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		if _, lstatErr := os.Lstat(target); lstatErr == nil {
			return "", fmt.Errorf("resolve symlink %q: %w", target, err)
		} else if !os.IsNotExist(lstatErr) {
			return "", lstatErr
		}
		parent := filepath.Dir(target)
		if parent == target {
			return "", err
		}
		missing = append(missing, filepath.Base(target))
		target = parent
	}
}

// pathWithinPolicyPrefix reports whether path is contained within prefix both
// lexically and after resolving symlinks. The lexical check retains configured
// path boundaries while the resolved check prevents a symlink from escaping one.
func pathWithinPolicyPrefix(prefix, path string) (bool, error) {
	if !pathWithinRoot(prefix, path) {
		return false, nil
	}
	return resolvedPathWithin(prefix, path)
}

// resolvedPathWithin reports whether path is contained within prefix after
// resolving symlinks while preserving any missing suffix.
func resolvedPathWithin(prefix, path string) (bool, error) {
	resolvedPrefix, err := resolvePathWithMissing(prefix)
	if err != nil {
		return false, fmt.Errorf("resolve policy prefix: %w", err)
	}
	resolvedPath, err := resolvePathWithMissing(path)
	if err != nil {
		return false, fmt.Errorf("resolve policy path: %w", err)
	}
	return pathWithinRoot(resolvedPrefix, resolvedPath), nil
}

func pathWithinRoot(root, path string) bool {
	if root == "" || path == "" {
		return false
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// rejectSpecialFile denies device, pipe, and socket paths so tools can never
// open the controlling terminal (e.g. /dev/stdin) or other non-regular files.
func rejectSpecialFile(path string) error {
	fi, err := os.Stat(path) // follow symlinks so /dev/stdin -> tty is caught
	if err != nil {
		return nil // missing/unstatable: let the tool surface its own error
	}
	const special = os.ModeDevice | os.ModeCharDevice | os.ModeNamedPipe | os.ModeSocket
	if fi.Mode()&special != 0 {
		return &PathPolicyError{
			Path:   path,
			Reason: fmt.Sprintf("path %q is not a regular file", path),
		}
	}
	return nil
}
