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

// artifactDirs are the root-relative directory components subject to
// retention. Each is walked one component at a time with no symlink
// traversal.
var artifactDirs = [][]string{
	{".steiner", "tmp", "fetched"},
	{".steiner", "tmp", "bash-output"},
}

// PruneToolArtifacts removes stale and excess files under
// <root>/.steiner/tmp/fetched and <root>/.steiner/tmp/bash-output for each
// distinct root (for example the project root and a worktree workDir).
// Files older than fetchedMaxAge are removed first; remaining files, counted
// against one shared budget of fetchedBudgetBytes, are then removed
// oldest-first, except that no file younger than fetchedMinBudgetEvictionAge
// is removed by budget eviction. Missing directories are not an error.
// Symlinks are never followed (including in intermediate path components and
// subdirectories) or removed. Every directory is opened as an anchored handle
// verified to be the directory just inspected, and files are removed relative
// to that handle, so a later swap of a path component cannot redirect a
// deletion. Sibling directories such as .steiner/tmp/images and
// .steiner/worktrees are never scanned or modified.
func PruneToolArtifacts(roots []string) (removed int, err error) {
	return pruneToolArtifacts(roots, time.Now(), fetchedBudgetBytes)
}

// artifactFile is a regular file anchored to its verified parent directory.
type artifactFile struct {
	parent  *os.Root
	name    string
	modTime time.Time
	size    int64
}

// pruneToolArtifacts implements PruneToolArtifacts with an injectable clock
// and budget so tests can exercise budget eviction with small files.
func pruneToolArtifacts(roots []string, now time.Time, budgetBytes int64) (removed int, err error) {
	files, closeAll, errs := collectToolArtifacts(roots)
	defer closeAll()
	removed, evictErrs := evictArtifacts(files, now, budgetBytes)
	return removed, errors.Join(append(errs, evictErrs...)...)
}

// collectToolArtifacts gathers regular files from every root. Errors for one
// root or directory do not stop the others. The returned func closes every
// directory handle held by the files.
func collectToolArtifacts(roots []string) (files []artifactFile, closeAll func(), errs []error) {
	var handles []*os.Root
	closeAll = func() {
		for _, h := range handles {
			_ = h.Close() // read-only directory handle; nothing to flush
		}
	}
	for _, dir := range dedupeRoots(roots) {
		root, openErr := os.OpenRoot(dir)
		if openErr != nil {
			if !errors.Is(openErr, fs.ErrNotExist) {
				errs = append(errs, fmt.Errorf("open artifact root %s: %w", dir, openErr))
			}
			continue
		}
		handles = append(handles, root)
		for _, parts := range artifactDirs {
			cur := root
			for _, part := range parts {
				next, ok, stepErr := openChildDir(cur, part)
				if stepErr != nil {
					errs = append(errs, stepErr)
				}
				if !ok {
					cur = nil
					break
				}
				handles = append(handles, next)
				cur = next
			}
			if cur == nil {
				continue
			}
			var walkErrs []error
			files, walkErrs = walkArtifacts(cur, &handles, files)
			errs = append(errs, walkErrs...)
		}
	}
	return files, closeAll, errs
}

// evictArtifacts removes files older than fetchedMaxAge, then evicts the
// oldest remaining files until the total is within budgetBytes, never
// evicting a file younger than fetchedMinBudgetEvictionAge.
func evictArtifacts(files []artifactFile, now time.Time, budgetBytes int64) (removed int, errs []error) {
	var total int64
	kept := make([]artifactFile, 0, len(files))
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
	if total <= budgetBytes {
		return removed, errs
	}
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
	return removed, errs
}

// dedupeRoots cleans roots, resolves symlinks when possible, and drops
// empty and duplicate entries while preserving order. Roots are supplied by
// the composition root, not by tool input, so resolving them is safe.
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

// openChildDir opens name under parent as an anchored directory handle. It
// returns ok=false with no error when name is missing, a symlink, or not a
// directory. The opened handle must be the same file Lstat reported, so a
// component swapped for a symlink between inspection and open is rejected.
func openChildDir(parent *os.Root, name string) (child *os.Root, ok bool, err error) {
	before, err := parent.Lstat(name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrInvalid) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("stat artifact dir %s: %w", name, err)
	}
	if !before.IsDir() {
		return nil, false, nil
	}
	child, err = parent.OpenRoot(name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("open artifact dir %s: %w", name, err)
	}
	after, err := child.Stat(".")
	if err != nil || !os.SameFile(before, after) {
		_ = child.Close() // rejected handle; nothing to flush
		if err != nil {
			return nil, false, fmt.Errorf("stat opened artifact dir %s: %w", name, err)
		}
		return nil, false, nil
	}
	return child, true, nil
}

// walkArtifacts appends regular files directly under dir and, recursively,
// under its real subdirectories. Symlinks and other non-regular entries are
// skipped. Opened handles are appended to handles for the caller to close.
func walkArtifacts(dir *os.Root, handles *[]*os.Root, files []artifactFile) ([]artifactFile, []error) {
	f, err := dir.Open(".")
	if err != nil {
		return files, []error{fmt.Errorf("open artifact dir: %w", err)}
	}
	entries, err := f.ReadDir(-1)
	_ = f.Close() // read-only handle; nothing to flush
	if err != nil {
		return files, []error{fmt.Errorf("read artifact dir: %w", err)}
	}
	var errs []error
	for _, entry := range entries {
		name := entry.Name()
		switch {
		case entry.IsDir():
			sub, ok, subErr := openChildDir(dir, name)
			if subErr != nil {
				errs = append(errs, subErr)
			}
			if !ok {
				continue
			}
			*handles = append(*handles, sub)
			var walkErrs []error
			files, walkErrs = walkArtifacts(sub, handles, files)
			errs = append(errs, walkErrs...)
		case entry.Type().IsRegular():
			fi, statErr := dir.Lstat(name)
			if statErr != nil || !fi.Mode().IsRegular() {
				// Vanished or replaced between ReadDir and Lstat.
				continue
			}
			files = append(files, artifactFile{parent: dir, name: name, modTime: fi.ModTime(), size: fi.Size()})
		}
	}
	return files, errs
}

// removeArtifact unlinks f relative to its anchored parent handle. The leaf
// is never followed, so a symlink swapped in at the leaf is unlinked, not
// traversed.
func removeArtifact(f artifactFile) error {
	if err := f.parent.Remove(f.name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove artifact file %s: %w", f.name, err)
	}
	return nil
}
