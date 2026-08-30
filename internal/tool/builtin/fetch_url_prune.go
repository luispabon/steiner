package builtin

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const (
	// fetchedMaxAge is the maximum age a file in .steiner/tmp/fetched may
	// reach before it is eligible for removal regardless of disk budget.
	fetchedMaxAge = 7 * 24 * time.Hour
	// fetchedMinBudgetEvictionAge is the minimum age a file must reach
	// before budget eviction may remove it. Two steiner processes sharing
	// a project root is normal (sub-agents, a second terminal); without
	// this floor a starting session's budget sweep could delete a file
	// another session's in-flight turn was just told to read.
	fetchedMinBudgetEvictionAge = 1 * time.Hour
	// fetchedBudgetBytes is the target total size for .steiner/tmp/fetched
	// after budget eviction.
	fetchedBudgetBytes = 250 * 1024 * 1024
)

// PruneFetchedDir removes stale and excess files under
// <workDir>/.steiner/tmp/fetched. Files older than fetchedMaxAge are removed
// first; remaining files are then removed oldest-first until the directory
// is under fetchedBudgetBytes, except that no file younger than
// fetchedMinBudgetEvictionAge is ever removed by budget eviction. A missing
// fetched directory is not an error. Only .steiner/tmp/fetched is touched;
// sibling directories such as .steiner/tmp/images and .steiner/worktrees are
// never scanned or modified.
func PruneFetchedDir(workDir string) (removed int, err error) {
	return pruneFetchedDir(workDir, time.Now(), fetchedBudgetBytes)
}

// pruneFetchedDir implements PruneFetchedDir with an injectable clock and
// budget so tests can exercise budget eviction without allocating
// fetchedBudgetBytes worth of files on disk.
func pruneFetchedDir(workDir string, now time.Time, budgetBytes int64) (removed int, err error) {
	fetchedDir := filepath.Join(workDir, ".steiner", "tmp", "fetched")

	entries, err := os.ReadDir(fetchedDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("read fetched dir: %w", err)
	}

	type fileInfo struct {
		path    string
		modTime time.Time
		size    int64
	}

	var files []fileInfo
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			// Entry vanished between ReadDir and Info (e.g. another
			// steiner process already removed it); nothing to prune.
			continue
		}
		path := filepath.Join(fetchedDir, entry.Name())
		if now.Sub(info.ModTime()) >= fetchedMaxAge {
			if err := os.Remove(path); err != nil {
				// Already removed by a concurrent process; not our error.
				continue
			}
			removed++
			continue
		}
		files = append(files, fileInfo{path: path, modTime: info.ModTime(), size: info.Size()})
	}

	var total int64
	for _, f := range files {
		total += f.size
	}
	if total <= budgetBytes {
		return removed, nil
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].modTime.Before(files[j].modTime)
	})

	for _, f := range files {
		if total <= budgetBytes {
			break
		}
		if now.Sub(f.modTime) < fetchedMinBudgetEvictionAge {
			continue
		}
		if err := os.Remove(f.path); err != nil {
			// Already removed by a concurrent process; leave total as-is
			// since the file's bytes are no longer actually present.
			continue
		}
		removed++
		total -= f.size
	}

	return removed, nil
}
