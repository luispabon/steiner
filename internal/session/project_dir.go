package session

import (
	"fmt"
	"os"
	"path/filepath"
)

// EnsureSteinerProjectDir creates the .steiner directory and a .gitignore file.
// It is idempotent and safe to call multiple times.
func EnsureSteinerProjectDir(workDir string) error {
	steinerDir := filepath.Join(workDir, ".steiner")
	if err := os.MkdirAll(steinerDir, 0o755); err != nil {
		return fmt.Errorf("create .steiner directory: %w", err)
	}

	gitignorePath := filepath.Join(steinerDir, ".gitignore")
	switch _, err := os.Stat(gitignorePath); {
	case err == nil:
	case os.IsNotExist(err):
		if err := os.WriteFile(gitignorePath, []byte("*\n"), 0o644); err != nil {
			return fmt.Errorf("create .steiner/.gitignore: %w", err)
		}
	default:
		return fmt.Errorf("stat .steiner/.gitignore: %w", err)
	}

	return nil
}
