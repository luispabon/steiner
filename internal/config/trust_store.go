package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

const trustStoreVersion = 1

type trustStoreFile struct {
	Version  int                          `json:"version"`
	Projects map[string]trustedProjectRec `json:"projects"`
}

type trustedProjectRec struct {
	TrustedAt time.Time `json:"trusted_at"`
}

func trustStorePath(homeDir string) string {
	return filepath.Join(homeDir, ".config", "steiner", "trusted_projects.json")
}

// readTrustStore returns the store, a non-empty user-facing notice when a corrupt
// store was deleted, or an error.
func readTrustStore(path string) (trustStoreFile, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return trustStoreFile{Projects: map[string]trustedProjectRec{}}, "", nil
		}
		return trustStoreFile{}, "", fmt.Errorf("read trust store %q: %w", path, err)
	}

	var store trustStoreFile
	var reason string
	if err := json.Unmarshal(data, &store); err != nil {
		reason = err.Error()
	} else if store.Version != trustStoreVersion {
		reason = fmt.Sprintf("unsupported version %d", store.Version)
	}

	if reason != "" {
		if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) {
			return trustStoreFile{}, "", fmt.Errorf("delete corrupt trust store %q: %w", path, removeErr)
		}
		notice := fmt.Sprintf("Your trusted-projects file (%s) was corrupt (%s) and has been deleted. You will need to trust your projects again.", path, reason)
		return trustStoreFile{Projects: map[string]trustedProjectRec{}}, notice, nil
	}

	if store.Projects == nil {
		store.Projects = map[string]trustedProjectRec{}
	}
	return store, "", nil
}

func writeTrustStore(path string, store trustStoreFile) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("write trust store %q: create dir: %w", path, err)
	}

	store.Version = trustStoreVersion
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return fmt.Errorf("write trust store %q: marshal: %w", path, err)
	}

	tmp, err := os.CreateTemp(dir, ".trusted_projects-*.json")
	if err != nil {
		return fmt.Errorf("write trust store %q: create temp file: %w", path, err)
	}
	tmpPath := tmp.Name()

	// Cleanup below is best-effort: the rename never happened in these error
	// paths, so a leftover temp file is the only cost of an ignored error here.
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write trust store %q: chmod temp file: %w", path, err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write trust store %q: write temp file: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write trust store %q: sync temp file: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write trust store %q: close temp file: %w", path, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write trust store %q: rename temp file: %w", path, err)
	}
	return nil
}

func (s trustStoreFile) isTrusted(root string) bool {
	_, ok := s.Projects[root]
	return ok
}

func (s *trustStoreFile) trust(root string, now time.Time) {
	if s.Projects == nil {
		s.Projects = map[string]trustedProjectRec{}
	}
	s.Projects[root] = trustedProjectRec{TrustedAt: now.UTC()}
}

// TrustProject records root as permanently trusted in the trust store under the
// home directory resolved from opts.
func TrustProject(opts LoadOptions, root string, now time.Time) error {
	env := loadEnvironment(opts.Env)
	homeDir, err := resolveHomeDir(opts.HomeDir, env)
	if err != nil {
		return err
	}

	path := trustStorePath(homeDir)

	// The notice is ignored here: an earlier InspectProject call (added in a
	// future step) already surfaces corruption notices to the user.
	store, _, err := readTrustStore(path)
	if err != nil {
		return err
	}

	store.trust(root, now)
	return writeTrustStore(path, store)
}
