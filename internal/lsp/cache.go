package lsp

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
)

// cacheDirFor returns the persistent cache directory for a given root.
// It creates the directory with 0o700 permissions if it doesn't exist.
// The directory is derived from configCacheDir (or os.UserCacheDir if not set)
// and contains a hash of the root to isolate roots.
func cacheDirFor(configCacheDir, root string) (string, error) {
	base := configCacheDir
	if base == "" {
		var err error
		base, err = os.UserCacheDir()
		if err != nil {
			return "", fmt.Errorf("get user cache dir: %w", err)
		}
	}

	hash := sha256.Sum256([]byte(root))
	hashStr := fmt.Sprintf("%x", hash[:])[:16]

	cacheDir := filepath.Join(base, "steiner", "lsp", hashStr)
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return "", fmt.Errorf("create cache dir: %w", err)
	}

	// Verify the permissions are exactly 0o700.
	info, err := os.Stat(cacheDir)
	if err != nil {
		return "", fmt.Errorf("stat cache dir: %w", err)
	}
	if info.Mode().Perm() != 0o700 {
		if err := os.Chmod(cacheDir, 0o700); err != nil {
			return "", fmt.Errorf("chmod cache dir: %w", err)
		}
	}

	return cacheDir, nil
}
