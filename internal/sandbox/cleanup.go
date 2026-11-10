package sandbox

import (
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// CleanupOrphans removes subdirectories under baseDir whose modtime is older
// than maxAge and whose liveness lock is free, meaning no live steiner runtime
// still owns them. Returns the number of directories removed. Returns 0 if
// baseDir does not exist or cannot be read; this function is best-effort
// startup cleanup and never reports errors to the caller. On platforms without
// advisory directory locks it removes nothing, because an old directory cannot
// be distinguished from a live one.
func CleanupOrphans(baseDir string, maxAge time.Duration) int {
	info, err := os.Lstat(baseDir)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return 0
	}
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
		if !fi.ModTime().Before(cutoff) {
			continue
		}
		if removeDirIfUnlocked(filepath.Join(baseDir, e.Name()), cutoff) {
			removed++
		}
	}
	return removed
}

// forceRemoveAll removes path and its contents, chmod-ing directories to
// 0o700 first so read-only trees (e.g. Go module caches with 0o444 files)
// can still be unlinked. Best-effort: chmod and walk errors are ignored.
func forceRemoveAll(path string) error {
	// best-effort cleanup; a partial walk still removes what it reached
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
