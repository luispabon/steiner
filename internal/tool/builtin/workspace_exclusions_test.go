package builtin

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

// workspaceExclusionFixture builds the tree shared by the cross-tool exclusion
// tests. Every file contains "needle" so grep reports the same set the walkers
// enumerate.
func workspaceExclusionFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, rel := range []string{
		"src/main.go",
		"src/helper.go",
		"src/gen/out.go",
		"sub/keep.go",
		"sub/secret/hidden.go",
		"sub/abs_skip/skipped.go",
		"build/out.go",
		".steiner/worktrees/wt/src/wt.go",
	} {
		mustWriteTree(t, root, rel, "needle\n")
	}
	return root
}

func workspaceTestEnv(t *testing.T, root string, excludePaths, excludePatterns []string) Env {
	t.Helper()
	policy := tool.NewPathPolicy(root, config.PathsConfig{})
	excluder := tool.NewWorkspaceExcluder(root, excludePaths, excludePatterns)
	return Env{WorkDir: root, PathPolicy: &policy, Excluder: &excluder}
}

func outputRows(output, empty string) []string {
	if output == "" || output == empty {
		return nil
	}
	return strings.Split(output, "\n")
}

func grepWorkspaceRows(t *testing.T, env Env, searchPath string) []string {
	t.Helper()
	resultI, err := NewGrepTool(env).Handler(context.Background(), map[string]any{
		"path":        searchPath,
		"pattern":     "needle",
		"output_mode": "files_with_matches",
	})
	if err != nil {
		t.Fatalf("grep %q: %v", searchPath, err)
	}
	result, ok := resultI.(GrepResult)
	if !ok {
		t.Fatalf("grep result type = %T, want GrepResult", resultI)
	}
	return outputRows(result.Output, "No matches found")
}

func globWorkspaceRows(t *testing.T, env Env, searchPath string) []string {
	t.Helper()
	resultI, err := NewGlobTool(env).Handler(context.Background(), map[string]any{
		"path":    searchPath,
		"pattern": "**/*.go",
	})
	if err != nil {
		t.Fatalf("glob %q: %v", searchPath, err)
	}
	result, ok := resultI.(Result)
	if !ok {
		t.Fatalf("glob result type = %T, want Result", resultI)
	}
	return outputRows(result.Output, "")
}

func lsWorkspaceRows(t *testing.T, env Env, searchPath string, recursive bool, limit int) []string {
	t.Helper()
	input := map[string]any{"path": searchPath}
	if recursive {
		input["recursive"] = true
	}
	if limit > 0 {
		input["limit"] = limit
	}
	resultI, err := NewLSTool(env).Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("ls %q: %v", searchPath, err)
	}
	result, ok := resultI.(*Result)
	if !ok {
		t.Fatalf("ls result type = %T, want *Result", resultI)
	}
	return outputRows(result.Output, "")
}

// TestWorkspaceExclusions_ApplyAcrossSearchTools proves grep, glob, recursive
// ls, and non-recursive ls agree on workspace-anchored exclusions, including
// walks started from a workspace subdirectory.
func TestWorkspaceExclusions_ApplyAcrossSearchTools(t *testing.T) {
	root := workspaceExclusionFixture(t)
	tests := []struct {
		name               string
		excludePaths       []string
		excludePatterns    []string
		searchPath         string
		wantGrepGlob       []string
		wantLSRecursive    []string
		wantLSNonRecursive []string
	}{
		{
			name:               "configured exact path below workspace subroot",
			excludePaths:       []string{"sub/secret"},
			searchPath:         "sub",
			wantGrepGlob:       []string{"abs_skip/skipped.go", "keep.go"},
			wantLSRecursive:    []string{"abs_skip/", "abs_skip/skipped.go", "keep.go"},
			wantLSNonRecursive: []string{"abs_skip/", "keep.go"},
		},
		{
			name:               "absolute configured exclude path",
			excludePaths:       []string{filepath.Join(root, "sub", "abs_skip")},
			searchPath:         "sub",
			wantGrepGlob:       []string{"keep.go", "secret/hidden.go"},
			wantLSRecursive:    []string{"keep.go", "secret/", "secret/hidden.go"},
			wantLSNonRecursive: []string{"keep.go", "secret/"},
		},
		{
			name:               "slash pattern anchored to workspace",
			excludePatterns:    []string{"src/gen"},
			searchPath:         ".",
			wantGrepGlob:       []string{"src/helper.go", "src/main.go", "sub/abs_skip/skipped.go", "sub/keep.go", "sub/secret/hidden.go"},
			wantLSRecursive:    []string{"src/", "src/helper.go", "src/main.go", "sub/", "sub/abs_skip/", "sub/abs_skip/skipped.go", "sub/keep.go", "sub/secret/", "sub/secret/hidden.go"},
			wantLSNonRecursive: []string{"src/", "sub/"},
		},
		{
			name:               "slash pattern stays anchored from subroot walk",
			excludePatterns:    []string{"src/gen"},
			searchPath:         "src",
			wantGrepGlob:       []string{"helper.go", "main.go"},
			wantLSRecursive:    []string{"helper.go", "main.go"},
			wantLSNonRecursive: []string{"helper.go", "main.go"},
		},
		{
			name:               "builtin directory excluded from workspace walk",
			searchPath:         ".",
			wantGrepGlob:       []string{"src/gen/out.go", "src/helper.go", "src/main.go", "sub/abs_skip/skipped.go", "sub/keep.go", "sub/secret/hidden.go"},
			wantLSRecursive:    []string{"src/", "src/gen/", "src/gen/out.go", "src/helper.go", "src/main.go", "sub/", "sub/abs_skip/", "sub/abs_skip/skipped.go", "sub/keep.go", "sub/secret/", "sub/secret/hidden.go"},
			wantLSNonRecursive: []string{"src/", "sub/"},
		},
		{
			name:               "explicit build root is searchable",
			searchPath:         "build",
			wantGrepGlob:       []string{"out.go"},
			wantLSRecursive:    []string{"out.go"},
			wantLSNonRecursive: []string{"out.go"},
		},
		{
			name:               "explicit worktree root is searchable",
			searchPath:         ".steiner/worktrees/wt",
			wantGrepGlob:       []string{"src/wt.go"},
			wantLSRecursive:    []string{"src/", "src/wt.go"},
			wantLSNonRecursive: []string{"src/"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := workspaceTestEnv(t, root, tt.excludePaths, tt.excludePatterns)
			assertSameStringSet(t, "grep rows", grepWorkspaceRows(t, env, tt.searchPath), tt.wantGrepGlob)
			assertSameStringSet(t, "glob rows", globWorkspaceRows(t, env, tt.searchPath), tt.wantGrepGlob)
			assertSameStringSet(t, "recursive ls rows", lsWorkspaceRows(t, env, tt.searchPath, true, 0), tt.wantLSRecursive)
			assertSameStringSet(t, "non-recursive ls rows", lsWorkspaceRows(t, env, tt.searchPath, false, 0), tt.wantLSNonRecursive)
		})
	}
}

// TestLSExclusionsFilterBeforePagination proves non-recursive ls drops
// configured and built-in exclusions before limit and offset are applied.
func TestLSExclusionsFilterBeforePagination(t *testing.T) {
	tests := []struct {
		name         string
		excludePaths []string
		tree         []string
		want         []string
	}{
		{
			name:         "configured exclusion does not consume the page",
			excludePaths: []string{"aaa_excluded"},
			tree:         []string{"aaa_excluded/hidden.txt", "b.txt"},
			want:         []string{"b.txt"},
		},
		{
			name: "builtin exclusion does not consume the page",
			tree: []string{"a.txt", "build/hidden.txt"},
			want: []string{"a.txt"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			for _, rel := range tt.tree {
				mustWriteTree(t, root, rel, "x")
			}
			env := workspaceTestEnv(t, root, tt.excludePaths, nil)
			got := lsWorkspaceRows(t, env, ".", false, 1)
			if !slices.Equal(got, tt.want) {
				t.Fatalf("ls rows = %v, want %v", got, tt.want)
			}
		})
	}
}
