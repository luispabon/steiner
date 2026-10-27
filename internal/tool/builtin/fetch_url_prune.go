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

// artifactDirs are the cache directory names under <root>/.steiner/tmp that
// are subject to retention.
var artifactDirs = []string{"fetched", "bash-output"}

// PruneToolArtifacts removes stale and excess files under
// <root>/.steiner/tmp/fetched and <root>/.steiner/tmp/bash-output for each
// distinct root (for example the project root and a worktree workDir).
// Only the immediate regular files of each cache directory are considered:
// the fetch and bash artifact writers create flat files, so subdirectories
// and non-regular entries are ignored and never recursed into. Files older
// than fetchedMaxAge are removed first; remaining files, counted against one
// shared budget of fetchedBudgetBytes, are then removed oldest-first, except
// that no file younger than fetchedMinBudgetEvictionAge is removed by budget
// eviction. Missing directories are not an error.
//
// Path components are inspected with Lstat and non-directories (including
// symlinks) are skipped. Each directory is then opened as a handle and
// checked to be the same file Lstat reported, and files are unlinked relative
// to that handle by bare name. A component swapped after the check cannot
// redirect a deletion, and a leaf swapped for a symlink is unlinked rather
// than followed. Sibling directories such as .steiner/tmp/images and
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

// collectToolArtifacts gathers immediate regular files from every root's
// cache directories. Errors for one root or directory do not stop the others.
// Only the cache directory handles stay open (at most one per cache per
// root); the func returned closes them.
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
		tmp, tmpErrs := openTmpDir(root)
		_ = root.Close() // read-only directory handle; nothing to flush
		errs = append(errs, tmpErrs...)
		if tmp == nil {
			continue
		}
		for _, name := range artifactDirs {
			cache, ok, stepErr := openChildDir(tmp, name)
			if stepErr != nil {
				errs = append(errs, stepErr)
			}
			if !ok {
				continue
			}
			handles = append(handles, cache)
			var listErrs []error
			files, listErrs = listArtifacts(cache, files)
			errs = append(errs, listErrs...)
		}
		_ = tmp.Close() // read-only directory handle; nothing to flush
	}
	return files, closeAll, errs
}

// openTmpDir opens <root>/.steiner/tmp one verified component at a time and
// returns nil when any component is missing or not a real directory.
func openTmpDir(root *os.Root) (*os.Root, []error) {
	steiner, ok, err := openChildDir(root, ".steiner")
	if !ok {
		return nil, errsOf(err)
	}
	tmp, ok, err := openChildDir(steiner, "tmp")
	_ = steiner.Close() // read-only directory handle; nothing to flush
	if !ok {
		return nil, errsOf(err)
	}
	return tmp, nil
}

func errsOf(err error) []error {
	if err == nil {
		return nil
	}
	return []error{err}
}

// evictArtifacts removes files older than fetchedMaxAge, then evicts the
// oldest remaining files until the total is within budgetBytes, never
// evicting a file younger than fetchedMinBudgetEvictionAge. Zero-byte files
// free no budget and are not evicted by budget pressure. removed counts only
// files this call actually unlinked.
func evictArtifacts(files []artifactFile, now time.Time, budgetBytes int64) (removed int, errs []error) {
	var total int64
	kept := make([]artifactFile, 0, len(files))
	for _, f := range files {
		if now.Sub(f.modTime) >= fetchedMaxAge {
			gone, rmErr := removeArtifact(f)
			if rmErr != nil {
				errs = append(errs, rmErr)
				continue
			}
			if gone {
				removed++
			}
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
		if f.size == 0 || now.Sub(f.modTime) < fetchedMinBudgetEvictionAge {
			continue
		}
		gone, rmErr := removeArtifact(f)
		if rmErr != nil {
			errs = append(errs, rmErr)
			continue
		}
		if gone {
			removed++
		}
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

// listArtifacts appends the immediate regular files of dir. Subdirectories,
// symlinks and other non-regular entries are ignored; there is no recursion.
func listArtifacts(dir *os.Root, files []artifactFile) ([]artifactFile, []error) {
	f, err := dir.Open(".")
	if err != nil {
		return files, []error{fmt.Errorf("open artifact dir: %w", err)}
	}
	entries, err := f.ReadDir(-1)
	_ = f.Close() // read-only handle; nothing to flush
	if err != nil {
		return files, []error{fmt.Errorf("read artifact dir: %w", err)}
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		name := entry.Name()
		fi, statErr := dir.Lstat(name)
		if statErr != nil || !fi.Mode().IsRegular() {
			// Vanished or replaced between ReadDir and Lstat.
			continue
		}
		files = append(files, artifactFile{parent: dir, name: name, modTime: fi.ModTime(), size: fi.Size()})
	}
	return files, nil
}

// removeArtifact unlinks f relative to its anchored parent handle and reports
// whether this call removed it (false when it was already gone). The leaf is
// never followed, so a symlink swapped in at the leaf is unlinked, not
// traversed.
func removeArtifact(f artifactFile) (bool, error) {
	if err := f.parent.Remove(f.name); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("remove artifact file %s: %w", f.name, err)
	}
	return true, nil
}
