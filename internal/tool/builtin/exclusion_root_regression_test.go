package builtin

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

// TestSearchExclusions_IntermediateSymlinkRoot proves a configured exclusion
// still applies when an explicit search path reaches it through a symlinked
// parent directory. Resolving only the final path component would walk the
// canonical target without validating it.
func TestSearchExclusions_IntermediateSymlinkRoot(t *testing.T) {
	root := t.TempDir()
	mustWriteTree(t, root, "real/secret/key.go", "needle\n")
	mustWriteTree(t, root, "real/open/key.go", "needle\n")
	mustSymlink(t, filepath.Join(root, "real"), filepath.Join(root, "alias"))

	env := workspaceTestEnv(t, root, []string{"real/secret"}, nil)

	assertSameStringSet(t, "excluded grep rows", grepWorkspaceRows(t, env, "alias/secret"), nil)
	assertSameStringSet(t, "excluded glob rows", globWorkspaceRows(t, env, "alias/secret"), nil)
	assertSameStringSet(t, "excluded recursive ls rows", lsWorkspaceRows(t, env, "alias/secret", true, 0), nil)
	assertSameStringSet(t, "excluded non-recursive ls rows", lsWorkspaceRows(t, env, "alias/secret", false, 0), nil)

	assertSameStringSet(t, "allowed grep rows", grepWorkspaceRows(t, env, "alias/open"), []string{"key.go"})
	assertSameStringSet(t, "allowed glob rows", globWorkspaceRows(t, env, "alias/open"), []string{"key.go"})
	assertSameStringSet(t, "allowed recursive ls rows", lsWorkspaceRows(t, env, "alias/open", true, 0), []string{"key.go"})
	assertSameStringSet(t, "allowed non-recursive ls rows", lsWorkspaceRows(t, env, "alias/open", false, 0), []string{"key.go"})
}

// TestSearchExclusions_IntermediateSymlinkBlockedTarget proves resolving an
// intermediate symlink also feeds the resolved target through PathPolicy, so a
// blocked path cannot be reached through an alias.
func TestSearchExclusions_IntermediateSymlinkBlockedTarget(t *testing.T) {
	root := t.TempDir()
	mustWriteTree(t, root, "real/secret/key.go", "needle\n")
	mustSymlink(t, filepath.Join(root, "real"), filepath.Join(root, "alias"))

	policy := tool.NewPathPolicy(root, config.PathsConfig{BlockedPaths: []string{"real/secret"}})
	excluder := tool.NewWorkspaceExcluder(root, nil, nil)
	env := Env{WorkDir: root, PathPolicy: &policy, Excluder: &excluder}
	ctx := context.Background()

	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "grep",
			call: func() error {
				_, err := NewGrepTool(env).Handler(ctx, map[string]any{
					"path":        "alias/secret",
					"pattern":     "needle",
					"output_mode": "files_with_matches",
				})
				return err
			},
		},
		{
			name: "glob",
			call: func() error {
				_, err := NewGlobTool(env).Handler(ctx, map[string]any{"path": "alias/secret", "pattern": "**/*.go"})
				return err
			},
		},
		{
			name: "ls",
			call: func() error {
				_, err := NewLSTool(env).Handler(ctx, map[string]any{"path": "alias/secret", "recursive": true})
				return err
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var policyErr *tool.PathPolicyError
			if err := tt.call(); !errors.As(err, &policyErr) {
				t.Fatalf("%s error = %v, want PathPolicyError", tt.name, err)
			}
		})
	}
}

// TestSearchExclusions_ExplicitRootBelowBuiltinSymlink proves a built-in
// directory name in the lexical ancestors of an explicit root does not hide
// that root. Built-in names match walk-relative components, and the resolved
// walk root sits above the lexical alias here, so the alias path must be
// matched against its own root.
func TestSearchExclusions_ExplicitRootBelowBuiltinSymlink(t *testing.T) {
	root := t.TempDir()
	mustWriteTree(t, root, "src/main.go", "needle\n")
	mustMkdir(t, filepath.Join(root, "build"))
	mustSymlink(t, root, filepath.Join(root, "build", "up"))

	env := workspaceTestEnv(t, root, nil, nil)

	assertSameStringSet(t, "grep rows", grepWorkspaceRows(t, env, "build/up"), []string{"src/main.go"})
	assertSameStringSet(t, "glob rows", globWorkspaceRows(t, env, "build/up"), []string{"src/main.go"})
	assertSameStringSet(t, "recursive ls rows", lsWorkspaceRows(t, env, "build/up", true, 0), []string{"src/", "src/main.go"})
	assertSameStringSet(t, "non-recursive ls rows", lsWorkspaceRows(t, env, "build/up", false, 0), []string{"src/"})
}

// TestSearchExclusions_SandboxTmpProvenance proves exclusion checks follow the
// path form the caller named. A request for the virtual /tmp tree is evaluated
// against /tmp, which sits outside the workspace, so workspace-relative
// exclusions and slashless component patterns do not hide it while an absolute
// /tmp entry does. A request naming the storage directory keeps the physical
// view, so exclusions written against it apply. The fixture uses the production
// layout, .steiner/tmp/sandbox-tmp/<id>, so the storage directory sits inside
// the workspace.
func TestSearchExclusions_SandboxTmpProvenance(t *testing.T) {
	root := t.TempDir()
	sandboxTmp := filepath.Join(root, ".steiner", "tmp", "sandbox-tmp", "s1")
	mustWriteTree(t, sandboxTmp, "notes.go", "needle\n")

	storagePath := filepath.Join(".steiner", "tmp", "sandbox-tmp", "s1")
	storageAbsolute := filepath.Join(root, storagePath)

	tests := []struct {
		name            string
		excludePaths    []string
		excludePatterns []string
		searchPath      string
		want            []string
	}{
		{
			name:         "virtual tmp request bypasses storage directory exclusion",
			excludePaths: []string{".steiner/tmp"},
			searchPath:   "/tmp",
			want:         []string{"notes.go"},
		},
		{
			name:         "physical storage request honors storage directory exclusion",
			excludePaths: []string{".steiner/tmp"},
			searchPath:   storagePath,
		},
		{
			name:         "absolute physical storage request honors storage directory exclusion",
			excludePaths: []string{filepath.Join(root, ".steiner", "tmp")},
			searchPath:   storageAbsolute,
		},
		{
			name:         "virtual tmp request ignores absolute storage path exclusion",
			excludePaths: []string{storageAbsolute},
			searchPath:   "/tmp",
			want:         []string{"notes.go"},
		},
		{
			name:         "virtual tmp exclusion applies to a virtual tmp request",
			excludePaths: []string{"/tmp"},
			searchPath:   "/tmp",
		},
		{
			name:            "slashless tmp pattern skips the physical storage request",
			excludePatterns: []string{"tmp"},
			searchPath:      storagePath,
		},
		{
			name:            "slashless tmp pattern does not apply to a virtual tmp request",
			excludePatterns: []string{"tmp"},
			searchPath:      "/tmp",
			want:            []string{"notes.go"},
		},
		{
			name:       "explicit physical storage root stays searchable",
			searchPath: storagePath,
			want:       []string{"notes.go"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy := tool.NewPathPolicyWithSandbox(root, config.PathsConfig{}, sandboxTmp)
			excluder := tool.NewWorkspaceExcluder(root, tt.excludePaths, tt.excludePatterns)
			env := Env{WorkDir: root, PathPolicy: &policy, Excluder: &excluder}

			assertSameStringSet(t, "grep rows", grepWorkspaceRows(t, env, tt.searchPath), tt.want)
			assertSameStringSet(t, "glob rows", globWorkspaceRows(t, env, tt.searchPath), tt.want)
			assertSameStringSet(t, "recursive ls rows", lsWorkspaceRows(t, env, tt.searchPath, true, 0), tt.want)
			assertSameStringSet(t, "non-recursive ls rows", lsWorkspaceRows(t, env, tt.searchPath, false, 0), tt.want)
		})
	}
}
