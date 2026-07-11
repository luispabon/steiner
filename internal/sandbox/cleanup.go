package sandbox

import (
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// CleanupOrphans removes subdirectories under baseDir whose modtime is older than maxAge.
// Returns the number of directories removed. Returns 0 if baseDir does not exist
// or cannot be read — this function is best-effort startup cleanup and never
// reports errors to the caller.
func CleanupOrphans(baseDir string, maxAge time.Duration) int {
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return 0
	}
	cutoff := time.Now().Add(-maxAge)
	var removed int
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		if fi.ModTime().Before(cutoff) {
			if err := forceRemoveAll(filepath.Join(baseDir, e.Name())); err != nil {
				continue
			}
			removed++
		}
	}
	return removed
}

// forceRemoveAll removes path and its contents, chmod-ing directories to
// 0o700 first so read-only trees (e.g. Go module caches with 0o444 files)
// can still be unlinked. Best-effort: chmod and walk errors are ignored.
func forceRemoveAll(path string) error {
	_ = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			_ = os.Chmod(p, 0o700)
		}
		return nil
	})
	return os.RemoveAll(path)
}
