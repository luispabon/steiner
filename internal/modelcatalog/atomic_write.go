package modelcatalog

import (
	"fmt"
	"os"
)

func atomicWriteFile(dir, pattern, target, kind string, data []byte) error {
	tmp, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return fmt.Errorf("create %s temp file: %w", kind, err)
	}
	tmpName := tmp.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s temp file: %w", kind, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s temp file: %w", kind, err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return fmt.Errorf("chmod %s temp file: %w", kind, err)
	}
	if err := os.Rename(tmpName, target); err != nil {
		if kind == "cache" {
			return fmt.Errorf("rename cache envelope: %w", err)
		}
		return fmt.Errorf("rename popularity store: %w", err)
	}
	removeTemp = false
	return nil
}
