package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadTrustStore(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "trusted_projects.json")

		store, notice, err := readTrustStore(path)
		if err != nil {
			t.Fatalf("readTrustStore: %v", err)
		}
		if notice != "" {
			t.Fatalf("notice = %q, want empty", notice)
		}
		if store.Projects == nil {
			t.Fatal("Projects is nil, want non-nil empty map")
		}
		if len(store.Projects) != 0 {
			t.Fatalf("Projects = %v, want empty", store.Projects)
		}
	})

	t.Run("valid file round-trip", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "trusted_projects.json")
		now := time.Now()

		var store trustStoreFile
		store.trust("/repo/one", now)
		if err := writeTrustStore(path, store); err != nil {
			t.Fatalf("writeTrustStore: %v", err)
		}

		got, notice, err := readTrustStore(path)
		if err != nil {
			t.Fatalf("readTrustStore: %v", err)
		}
		if notice != "" {
			t.Fatalf("notice = %q, want empty", notice)
		}
		if !got.isTrusted("/repo/one") {
			t.Fatal("isTrusted(/repo/one) = false, want true")
		}
	})

	t.Run("invalid JSON deleted with notice", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "trusted_projects.json")
		if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		store, notice, err := readTrustStore(path)
		if err != nil {
			t.Fatalf("readTrustStore: %v", err)
		}
		if len(store.Projects) != 0 {
			t.Fatalf("Projects = %v, want empty", store.Projects)
		}
		if !strings.Contains(notice, path) {
			t.Errorf("notice = %q, want to contain path %q", notice, path)
		}
		if !strings.Contains(notice, "trust your projects again") {
			t.Errorf("notice = %q, want to contain %q", notice, "trust your projects again")
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("Stat(path) err = %v, want not-exist", err)
		}
	})

	t.Run("unsupported version deleted with notice", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "trusted_projects.json")
		if err := os.WriteFile(path, []byte(`{"version":2,"projects":{}}`), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		_, notice, err := readTrustStore(path)
		if err != nil {
			t.Fatalf("readTrustStore: %v", err)
		}
		if !strings.Contains(notice, "unsupported version 2") {
			t.Errorf("notice = %q, want to contain %q", notice, "unsupported version 2")
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("Stat(path) err = %v, want not-exist", err)
		}
	})

	t.Run("unreadable file is a hard error", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("running as root bypasses permission bits")
		}
		path := filepath.Join(t.TempDir(), "trusted_projects.json")
		if err := os.WriteFile(path, []byte(`{"version":1,"projects":{}}`), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		if err := os.Chmod(path, 0o000); err != nil {
			t.Fatalf("Chmod: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(path, 0o600) })

		_, _, err := readTrustStore(path)
		if err == nil {
			t.Fatal("readTrustStore err = nil, want error")
		}
		if _, statErr := os.Stat(path); statErr != nil {
			t.Errorf("Stat(path) err = %v, want file to still exist", statErr)
		}
	})
}

func TestWriteTrustStorePermissions(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "nested")
	path := filepath.Join(nested, "trusted_projects.json")

	if err := writeTrustStore(path, trustStoreFile{}); err != nil {
		t.Fatalf("writeTrustStore: %v", err)
	}

	dirInfo, err := os.Stat(nested)
	if err != nil {
		t.Fatalf("Stat(nested): %v", err)
	}
	if mode := dirInfo.Mode().Perm(); mode != 0o700 {
		t.Errorf("dir perm = %o, want %o", mode, 0o700)
	}

	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(path): %v", err)
	}
	if mode := fileInfo.Mode().Perm(); mode != 0o600 {
		t.Errorf("file perm = %o, want %o", mode, 0o600)
	}
}

func TestTrustProject(t *testing.T) {
	t.Run("trusts a project", func(t *testing.T) {
		home := t.TempDir()
		opts := LoadOptions{HomeDir: home}
		root := "/repo/one"

		if err := TrustProject(opts, root, time.Now()); err != nil {
			t.Fatalf("TrustProject: %v", err)
		}

		store, _, err := readTrustStore(trustStorePath(home))
		if err != nil {
			t.Fatalf("readTrustStore: %v", err)
		}
		if !store.isTrusted(root) {
			t.Fatalf("isTrusted(%q) = false, want true", root)
		}
	})

	t.Run("recovers from corrupt store", func(t *testing.T) {
		home := t.TempDir()
		path := trustStorePath(home)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		opts := LoadOptions{HomeDir: home}
		root := "/repo/two"
		if err := TrustProject(opts, root, time.Now()); err != nil {
			t.Fatalf("TrustProject: %v", err)
		}

		store, notice, err := readTrustStore(path)
		if err != nil {
			t.Fatalf("readTrustStore: %v", err)
		}
		if notice != "" {
			t.Fatalf("notice = %q, want empty after recovery", notice)
		}
		if !store.isTrusted(root) {
			t.Fatalf("isTrusted(%q) = false, want true", root)
		}
		if len(store.Projects) != 1 {
			t.Fatalf("Projects = %v, want exactly one entry", store.Projects)
		}
	})
}
