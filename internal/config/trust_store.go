package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/luispabon/steiner/internal/fsatomic"
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

	if err := fsatomic.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write trust store %q: %w", path, err)
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

	// The notice is ignored here: callers run InspectProject first, which
	// surfaces corruption notices to the user.
	store, _, err := readTrustStore(path)
	if err != nil {
		return err
	}

	store.trust(root, now)
	return writeTrustStore(path, store)
}
