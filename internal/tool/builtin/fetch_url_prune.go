package builtin

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const (
	// fetchedMaxAge is the maximum age a retained tool artifact may reach
	// before it is eligible for removal regardless of disk budget.
	fetchedMaxAge = 7 * 24 * time.Hour
	// fetchedMinBudgetEvictionAge is the minimum age a file must reach
	// before budget eviction may remove it. Two steiner processes sharing
	// a project root is normal (sub-agents, a second terminal); without
	// this floor a starting session's budget sweep could delete a file
	// another session's in-flight turn was just told to read.
	fetchedMinBudgetEvictionAge = 1 * time.Hour
	// fetchedBudgetBytes is the shared soft size target for all retained
	// artifact directories across all roots after budget eviction.
	fetchedBudgetBytes = 250 * 1024 * 1024
)

// artifactDirs are the root-relative directories subject to retention.
var artifactDirs = []string{
	filepath.Join(".steiner", "tmp", "fetched"),
	filepath.Join(".steiner", "tmp", "bash-output"),
}

type artifactFile struct {
	root    *os.Root
	rel     string
	modTime time.Time
	size    int64
}

// PruneToolArtifacts removes stale and excess files under
// <root>/.steiner/tmp/fetched and <root>/.steiner/tmp/bash-output for each
// distinct root (for example the project root and a worktree workDir).
// Files older than fetchedMaxAge are removed first; remaining files, counted
// against one shared budget of fetchedBudgetBytes, are then removed
// oldest-first, except that no file younger than fetchedMinBudgetEvictionAge
// is removed by budget eviction. Missing directories are not an error.
// Symlinks are never followed or removed, and all access is confined to each
// root. Sibling directories such as .steiner/tmp/images and
// .steiner/worktrees are never scanned or modified.
func PruneToolArtifacts(roots []string) (removed int, err error) {
	return pruneToolArtifacts(roots, time.Now(), fetchedBudgetBytes)
}

// pruneToolArtifacts implements PruneToolArtifacts with an injectable clock
// and budget so tests can exercise budget eviction with small files.
func pruneToolArtifacts(roots []string, now time.Time, budgetBytes int64) (removed int, err error) {
	var errs []error
	var files []artifactFile
	for _, dir := range dedupeRoots(roots) {
		root, openErr := os.OpenRoot(dir)
		if openErr != nil {
			if !os.IsNotExist(openErr) {
				errs = append(errs, fmt.Errorf("open artifact root %s: %w", dir, openErr))
			}
			continue
		}
		defer root.Close()
		for _, rel := range artifactDirs {
			var walkErr error
			files, walkErr = collectArtifacts(root, rel, files)
			if walkErr != nil {
				errs = append(errs, walkErr)
			}
		}
	}

	var total int64
	kept := files[:0]
	for _, f := range files {
		if now.Sub(f.modTime) >= fetchedMaxAge {
			if rmErr := removeArtifact(f); rmErr != nil {
				errs = append(errs, rmErr)
				continue
			}
			removed++
			continue
		}
		kept = append(kept, f)
		total += f.size
	}

	if total > budgetBytes {
		sort.Slice(kept, func(i, j int) bool { return kept[i].modTime.Before(kept[j].modTime) })
		for _, f := range kept {
			if total <= budgetBytes {
				break
			}
			if now.Sub(f.modTime) < fetchedMinBudgetEvictionAge {
				continue
			}
			if rmErr := removeArtifact(f); rmErr != nil {
				errs = append(errs, rmErr)
				continue
			}
			removed++
			total -= f.size
		}
	}
	return removed, errors.Join(errs...)
}

// dedupeRoots cleans roots, resolves symlinks when possible, and drops
// empty and duplicate entries while preserving order.
func dedupeRoots(roots []string) []string {
	seen := make(map[string]bool, len(roots))
	var out []string
	for _, r := range roots {
		if r == "" {
			continue
		}
		r = filepath.Clean(r)
		if resolved, err := filepath.EvalSymlinks(r); err == nil {
			r = resolved
		}
		if seen[r] {
			continue
		}
		seen[r] = true
		out = append(out, r)
	}
	return out
}

// collectArtifacts appends regular files below rel to files. Symlinks and
// other non-regular entries are skipped; a symlinked or missing rel yields
// nothing.
func collectArtifacts(root *os.Root, rel string, files []artifactFile) ([]artifactFile, error) {
	info, err := root.Lstat(rel)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrInvalid) {
			return files, nil
		}
		return files, fmt.Errorf("stat artifact dir %s: %w", rel, err)
	}
	if !info.IsDir() {
		return files, nil
	}
	dir, err := root.Open(rel)
	if err != nil {
		return files, fmt.Errorf("open artifact dir %s: %w", rel, err)
	}
	entries, err := dir.ReadDir(-1)
	_ = dir.Close() // read-only handle; close error carries no data loss
	if err != nil {
		return files, fmt.Errorf("read artifact dir %s: %w", rel, err)
	}
	var errs []error
	for _, entry := range entries {
		child := filepath.Join(rel, entry.Name())
		switch {
		case entry.IsDir():
			var subErr error
			files, subErr = collectArtifacts(root, child, files)
			if subErr != nil {
				errs = append(errs, subErr)
			}
		case entry.Type().IsRegular():
			fi, statErr := root.Lstat(child)
			if statErr != nil || !fi.Mode().IsRegular() {
				// Vanished or replaced between ReadDir and Lstat.
				continue
			}
			files = append(files, artifactFile{root: root, rel: child, modTime: fi.ModTime(), size: fi.Size()})
		}
	}
	return files, errors.Join(errs...)
}

func removeArtifact(f artifactFile) error {
	if err := f.root.Remove(f.rel); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove artifact file %s: %w", f.rel, err)
	}
	return nil
}
