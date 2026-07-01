package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// CleanupOrphans removes subdirectories under baseDir whose modtime is older than maxAge.
// Returns the number of directories removed. Returns (0, nil) if baseDir does not exist
// or cannot be read — this function is intended for best-effort startup cleanup.
func CleanupOrphans(baseDir string, maxAge time.Duration) (int, error) {
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, nil
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
			if err := os.RemoveAll(filepath.Join(baseDir, e.Name())); err != nil {
				return removed, fmt.Errorf("remove orphan: %w", err)
			}
			removed++
		}
	}
	return removed, nil
}
