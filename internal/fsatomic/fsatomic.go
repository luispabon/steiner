// Package fsatomic replaces files atomically: data is written to a uniquely
// named temp file in the target directory, synced, and renamed over the target.
package fsatomic

import (
	"fmt"
	"os"
	"path/filepath"
)

// WriteFile atomically replaces path with data and sets its permission bits to
// mode exactly, independent of the process umask. The temp file lives in the
// same directory as path so the final rename stays on one filesystem. Readers
// see either the old or the new content, never a partial write.
//
// WriteFile does not create parent directories; callers create them with the
// mode they need. On any failure the temp file is removed and path is left
// unchanged.
func WriteFile(path string, data []byte, mode os.FileMode) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			// Best-effort cleanup; the error that failed the write is returned.
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename temp file: %w", err)
	}
	return nil
}
