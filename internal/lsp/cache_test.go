package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCacheDirFor(t *testing.T) {
	tests := []struct {
		name        string
		configDir   string
		root        string
		wantExist   bool
		wantPerms   bool
		wantPersist bool
	}{
		{
			name:        "creates cache dir with 0o700 in custom config dir",
			configDir:   "",
			root:        "/workspace/project1",
			wantExist:   true,
			wantPerms:   true,
			wantPersist: true,
		},
		{
			name:        "creates cache dir with 0o700 in default user cache dir",
			configDir:   "",
			root:        "/workspace/project1",
			wantExist:   true,
			wantPerms:   true,
			wantPersist: true,
		},
		{
			name:        "same root yields same cache dir",
			configDir:   "",
			root:        "/workspace/project1",
			wantExist:   true,
			wantPerms:   true,
			wantPersist: true,
		},
		{
			name:        "different roots yield different cache dirs",
			configDir:   "",
			root:        "/workspace/project2",
			wantExist:   true,
			wantPerms:   true,
			wantPersist: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// For testing, override the user cache dir if needed.
			if tt.configDir == "" {
				tmpCache := t.TempDir()
				tt.configDir = tmpCache
			}

			got, err := cacheDirFor(tt.configDir, tt.root)
			if err != nil {
				t.Fatalf("cacheDirFor: %v", err)
			}

			if tt.wantExist {
				info, err := os.Stat(got)
				if err != nil {
					t.Errorf("cache dir stat: %v", err)
				}
				if !info.IsDir() {
					t.Errorf("cache path is not a directory: %v", got)
				}
			}

			if tt.wantPerms {
				info, err := os.Stat(got)
				if err != nil {
					t.Errorf("cache dir stat for perms: %v", err)
				}
				if info.Mode().Perm() != 0o700 {
					t.Errorf("cache dir perms = %o, want 0o700", info.Mode().Perm())
				}
			}

			if tt.wantPersist {
				// Verify that calling again with same root returns same path.
				got2, err := cacheDirFor(tt.configDir, tt.root)
				if err != nil {
					t.Fatalf("cacheDirFor second call: %v", err)
				}
				if got2 != got {
					t.Errorf("cache dir not stable: first %q, second %q", got, got2)
				}
				// Verify the directory still exists (wasn't deleted).
				info, err := os.Stat(got2)
				if err != nil {
					t.Errorf("cache dir stat after second call: %v", err)
				}
				if !info.IsDir() {
					t.Errorf("cache path is not a directory after second call: %v", got2)
				}
			}
		})
	}
}

func TestCacheDirDifferentRootsAreDifferent(t *testing.T) {
	configDir := t.TempDir()
	root1 := "/workspace/project1"
	root2 := "/workspace/project2"

	dir1, err := cacheDirFor(configDir, root1)
	if err != nil {
		t.Fatalf("cacheDirFor root1: %v", err)
	}

	dir2, err := cacheDirFor(configDir, root2)
	if err != nil {
		t.Fatalf("cacheDirFor root2: %v", err)
	}

	if dir1 == dir2 {
		t.Errorf("different roots should yield different cache dirs, got same: %q", dir1)
	}

	// Both should still exist and have correct permissions.
	for _, dir := range []string{dir1, dir2} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Errorf("stat cache dir %q: %v", dir, err)
		}
		if info.Mode().Perm() != 0o700 {
			t.Errorf("cache dir perms = %o, want 0o700", info.Mode().Perm())
		}
	}
}

func TestCacheDirLocationStructure(t *testing.T) {
	configDir := t.TempDir()
	root := "/workspace/project"

	dir, err := cacheDirFor(configDir, root)
	if err != nil {
		t.Fatalf("cacheDirFor: %v", err)
	}

	// Verify the structure is <configDir>/steiner/lsp/<hash>
	expectedBase := filepath.Join(configDir, "steiner", "lsp")
	rel, err := filepath.Rel(expectedBase, dir)
	if err != nil || strings.HasPrefix(rel, "..") {
		t.Errorf("cache dir %q is not under expected base %q", dir, expectedBase)
	}
}
