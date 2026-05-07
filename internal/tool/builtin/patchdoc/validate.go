package patchdoc

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ValidatePatchPath validates a patch path against the workdir root and returns
// the absolute target path plus a slash-normalized relative path.
func ValidatePatchPath(root string, p string) (abs string, rel string, err error) {
	if p == "" {
		return "", "", fmt.Errorf("path must not be empty")
	}
	if strings.ContainsRune(p, '\x00') {
		return "", "", fmt.Errorf("path must not contain NUL bytes")
	}
	if filepath.IsAbs(p) {
		return "", "", fmt.Errorf("path must be relative")
	}

	cleaned := filepath.Clean(filepath.FromSlash(p))
	if cleaned == "." {
		return "", "", fmt.Errorf("path must not resolve to current directory")
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("path must not escape workdir")
	}

	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", "", fmt.Errorf("resolve workdir: %w", err)
	}

	resolvedRoot, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return "", "", fmt.Errorf("resolve workdir symlinks: %w", err)
	}

	abs = filepath.Join(rootAbs, cleaned)
	rel, err = filepath.Rel(rootAbs, abs)
	if err != nil {
		return "", "", fmt.Errorf("compute relative path: %w", err)
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("path escapes workdir")
	}

	if err := validateSymlinkPolicy(resolvedRoot, abs); err != nil {
		return "", "", err
	}

	return abs, filepath.ToSlash(rel), nil
}

func validateSymlinkPolicy(resolvedRoot string, target string) error {
	if _, err := os.Lstat(target); err == nil {
		resolvedTarget, err := filepath.EvalSymlinks(target)
		if err != nil {
			return fmt.Errorf("resolve path symlinks: %w", err)
		}
		if !pathWithinRoot(resolvedRoot, resolvedTarget) {
			return fmt.Errorf("path escapes workdir")
		}
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("stat path: %w", err)
	}

	parent := filepath.Dir(target)
	for {
		if _, err := os.Lstat(parent); err == nil {
			resolvedParent, err := filepath.EvalSymlinks(parent)
			if err != nil {
				return fmt.Errorf("resolve parent symlinks: %w", err)
			}
			if !pathWithinRoot(resolvedRoot, resolvedParent) {
				return fmt.Errorf("path escapes workdir")
			}
			return nil
		} else if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("stat parent: %w", err)
		}

		next := filepath.Dir(parent)
		if next == parent {
			break
		}
		parent = next
	}

	return fmt.Errorf("resolve parent symlinks: no existing ancestor for %q", target)
}

func pathWithinRoot(root string, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func validateDuplicateTargets(hunks []Hunk) error {
	sources := make(map[string]int, len(hunks))
	affected := make(map[string]int, len(hunks))

	for i, hunk := range hunks {
		source := hunk.Path()

		if prev, ok := sources[source]; ok {
			return fmt.Errorf("duplicate source path %q in hunks %d and %d", source, prev+1, i+1)
		}
		sources[source] = i

		target := hunk.AffectedPath()
		if prev, ok := affected[target]; ok {
			return fmt.Errorf("duplicate affected path %q in hunks %d and %d", target, prev+1, i+1)
		}
		affected[target] = i
	}

	for i, hunk := range hunks {
		update, ok := hunk.(UpdateFile)
		if !ok || update.MovePath == "" {
			continue
		}

		source := hunk.Path()
		target := hunk.AffectedPath()
		if source == target {
			return fmt.Errorf("move destination equals source %q", source)
		}
		if prev, ok := sources[target]; ok {
			return fmt.Errorf("move destination %q collides with source path in hunks %d and %d", target, prev+1, i+1)
		}
	}

	return nil
}
