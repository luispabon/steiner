package builtin

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
)

const (
	// bashArtifactMaxBytes is the largest body saveBashArtifact accepts.
	bashArtifactMaxBytes = 1048576
	// bashArtifactAttempts bounds retries on a (practically impossible) name collision.
	bashArtifactAttempts = 4
)

var bashArtifactDirs = []string{".steiner", "tmp", "bash-output"}

// saveBashArtifact writes body to a new private file under
// .steiner/tmp/bash-output/ inside workDir and returns its workDir-relative
// slash path. All access goes through os.Root; symlinks at any component of
// the artifact directory path are refused, even when they stay inside workDir.
func saveBashArtifact(workDir string, body []byte) (string, error) {
	if len(body) > bashArtifactMaxBytes {
		return "", fmt.Errorf("bash artifact too large: %d bytes exceeds %d", len(body), bashArtifactMaxBytes)
	}
	root, err := os.OpenRoot(workDir)
	if err != nil {
		return "", fmt.Errorf("open bash artifact root: %w", err)
	}
	defer root.Close() //nolint:errcheck // read-only handle close

	dir := ""
	for _, part := range bashArtifactDirs {
		dir = path.Join(dir, part)
		if err := ensureArtifactDir(root, dir); err != nil {
			return "", err
		}
	}

	for range bashArtifactAttempts {
		var buf [16]byte
		if _, err := rand.Read(buf[:]); err != nil {
			return "", fmt.Errorf("generate bash artifact name: %w", err)
		}
		rel := path.Join(dir, hex.EncodeToString(buf[:])+".log")
		f, err := root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("create bash artifact: %w", err)
		}
		if _, err := f.Write(body); err != nil {
			_ = f.Close()        // best-effort cleanup after write failure
			_ = root.Remove(rel) // best-effort cleanup after write failure
			return "", fmt.Errorf("write bash artifact: %w", err)
		}
		if err := f.Close(); err != nil {
			_ = root.Remove(rel) // best-effort cleanup after close failure
			return "", fmt.Errorf("close bash artifact: %w", err)
		}
		return rel, nil
	}
	return "", errors.New("create bash artifact: name collisions exhausted")
}

// ensureArtifactDir makes sure rel is a real directory (not a symlink) in root,
// creating it with mode 0700 when absent. Existing permissions are untouched.
func ensureArtifactDir(root *os.Root, rel string) error {
	info, err := root.Lstat(rel)
	if errors.Is(err, fs.ErrNotExist) {
		if err := root.Mkdir(rel, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("create bash artifact dir %s: %w", rel, err)
		}
		info, err = root.Lstat(rel)
	}
	if err != nil {
		return fmt.Errorf("stat bash artifact dir %s: %w", rel, err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("bash artifact dir %s is a symlink", rel)
	}
	if !info.IsDir() {
		return fmt.Errorf("bash artifact dir %s is not a directory", rel)
	}
	return nil
}
