package builtin

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

// TestSymlinkRootExclusions proves workspace exclusions are validated against
// both the lexical path a caller named and the resolved target of a symlink
// root, while an explicit root keeps its built-in directory override.
func TestSymlinkRootExclusions(t *testing.T) {
	tests := []struct {
		name               string
		files              []string
		links              map[string]string // link name -> target
		excludePaths       []string
		excludePatterns    []string
		searchPath         string
		wantGrepGlob       []string
		wantLSRecursive    []string
		wantLSNonRecursive []string
	}{
		{
			name:               "alias to configured excluded dir",
			files:              []string{"secrets/key.go"},
			links:              map[string]string{"alias": "secrets"},
			excludePaths:       []string{"secrets"},
			searchPath:         "alias",
			wantGrepGlob:       nil,
			wantLSRecursive:    nil,
			wantLSNonRecursive: nil,
		},
		{
			name:               "excluded link name pointing at allowed target",
			files:              []string{"src/main.go"},
			links:              map[string]string{"link": "src"},
			excludePaths:       []string{"link"},
			searchPath:         "link",
			wantGrepGlob:       nil,
			wantLSRecursive:    nil,
			wantLSNonRecursive: nil,
		},
		{
			name:               "slash exclusion under symlinked subdir",
			files:              []string{"src/main.go", "src/secret/hidden.go"},
			links:              map[string]string{"link": "src"},
			excludePatterns:    []string{"link/secret"},
			searchPath:         "link",
			wantGrepGlob:       []string{"main.go"},
			wantLSRecursive:    []string{"main.go"},
			wantLSNonRecursive: []string{"main.go"},
		},
		{
			name:               "explicit worktree root reached through a symlink",
			files:              []string{".steiner/worktrees/wt/src/wt.go"},
			links:              map[string]string{"wt-link": ".steiner/worktrees/wt"},
			searchPath:         "wt-link",
			wantGrepGlob:       []string{"src/wt.go"},
			wantLSRecursive:    []string{"src/", "src/wt.go"},
			wantLSNonRecursive: []string{"src/"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			for _, rel := range tt.files {
				mustWriteTree(t, root, rel, "needle\n")
			}
			for link, target := range tt.links {
				mustSymlink(t, filepath.Join(root, filepath.FromSlash(target)), filepath.Join(root, link))
			}
			env := workspaceTestEnv(t, root, tt.excludePaths, tt.excludePatterns)
			assertSameStringSet(t, "grep rows", grepWorkspaceRows(t, env, tt.searchPath), tt.wantGrepGlob)
			assertSameStringSet(t, "glob rows", globWorkspaceRows(t, env, tt.searchPath), tt.wantGrepGlob)
			assertSameStringSet(t, "recursive ls rows", lsWorkspaceRows(t, env, tt.searchPath, true, 0), tt.wantLSRecursive)
			assertSameStringSet(t, "non-recursive ls rows", lsWorkspaceRows(t, env, tt.searchPath, false, 0), tt.wantLSNonRecursive)
		})
	}
}

func TestSearchLexicalPath(t *testing.T) {
	tests := []struct {
		name        string
		walkRoot    string
		lexicalRoot string
		path        string
		want        string
	}{
		{name: "root maps to lexical root", walkRoot: "/ws/src", lexicalRoot: "/ws/link", path: "/ws/src", want: "/ws/link"},
		{name: "descendant maps under lexical root", walkRoot: "/ws/src", lexicalRoot: "/ws/link", path: "/ws/src/a/b.go", want: "/ws/link/a/b.go"},
		{name: "same roots keep path", walkRoot: "/ws", lexicalRoot: "/ws", path: "/ws/a", want: "/ws/a"},
		{name: "outside walk root keeps path", walkRoot: "/ws/src", lexicalRoot: "/ws/link", path: "/ws/other/a", want: "/ws/other/a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := searchLexicalPath(tt.walkRoot, tt.lexicalRoot, tt.path)
			if got != tt.want {
				t.Fatalf("searchLexicalPath(%q, %q, %q) = %q, want %q", tt.walkRoot, tt.lexicalRoot, tt.path, got, tt.want)
			}
		})
	}
}

// TestGrepFileSymlinkRootExclusions proves an explicit file root that is a
// symlink is validated against both its lexical name and, through PathPolicy,
// its resolved target.
func TestGrepFileSymlinkRootExclusions(t *testing.T) {
	tests := []struct {
		name          string
		excludePaths  []string
		blockedPaths  []string
		wantRows      []string
		wantPolicyErr bool
	}{
		{
			name:         "resolved target excluded",
			excludePaths: []string{"secrets"},
		},
		{
			name:         "lexical link name excluded",
			excludePaths: []string{"keylink.txt"},
		},
		{
			name:     "allowed symlinked file is searched",
			wantRows: []string{"keylink.txt"},
		},
		{
			name:          "blocked target fails closed",
			blockedPaths:  []string{"secrets"},
			wantPolicyErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			mustWriteTree(t, root, "secrets/key.txt", "needle\n")
			mustSymlink(t, filepath.Join(root, "secrets", "key.txt"), filepath.Join(root, "keylink.txt"))

			policy := tool.NewPathPolicy(root, config.PathsConfig{BlockedPaths: tt.blockedPaths})
			excluder := tool.NewWorkspaceExcluder(root, tt.excludePaths, nil)
			env := Env{WorkDir: root, PathPolicy: &policy, Excluder: &excluder}

			if tt.wantPolicyErr {
				_, err := NewGrepTool(env).Handler(context.Background(), map[string]any{
					"path":        "keylink.txt",
					"pattern":     "needle",
					"output_mode": "files_with_matches",
				})
				var policyErr *tool.PathPolicyError
				if !errors.As(err, &policyErr) {
					t.Fatalf("grep error = %v, want PathPolicyError", err)
				}
				return
			}

			assertSameStringSet(t, "grep rows", grepWorkspaceRows(t, env, "keylink.txt"), tt.wantRows)
		})
	}
}
