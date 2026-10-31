package builtin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

// symlinkSearchFixture writes the workspace shared by the symlink search tests.
// Every real file contains "needle", and each link under src/ exercises one
// eligibility rule: a plain link, a link out of the walk root but inside the
// workspace, an absolute link, dangling and directory links, a special file, a
// blocked target, an excluded target, a target outside the workspace, and a
// target that only escapes containment when its parent symlink is resolved.
func symlinkSearchFixture(t *testing.T, excludePaths, excludePatterns, blockedPaths []string) Env {
	t.Helper()
	root := t.TempDir()
	for _, rel := range []string{"src/inside.go", "outside/outside.go", "excluded/excluded.go", "blocked/blocked.go"} {
		mustWriteTree(t, root, rel, "needle\n")
	}
	mustMkfifo(t, filepath.Join(root, "special.pipe"))
	escapeDir := t.TempDir()
	mustWriteTree(t, escapeDir, "escape.go", "needle\n")
	mustSymlink(t, escapeDir, filepath.Join(root, "inner"))

	src := filepath.Join(root, "src")
	mustMkdir(t, src)
	links := map[string]string{
		"link-inside.go":    "inside.go",
		"link-workspace.go": "../outside/outside.go",
		"link-absolute.go":  filepath.Join(root, "outside", "outside.go"),
		"link-byname.go":    "inside.go",
		"link-dangling.go":  "../outside/missing.go",
		"link-directory.go": "../outside",
		"link-special.go":   "../special.pipe",
		"link-blocked.go":   "../blocked/blocked.go",
		"link-excluded.go":  "../excluded/excluded.go",
		"link-escape.go":    filepath.Join(escapeDir, "escape.go"),
		"link-chain.go":     "../inner/escape.go",
	}
	for name, target := range links {
		mustSymlink(t, target, filepath.Join(src, name))
	}

	policy := tool.NewPathPolicy(root, config.PathsConfig{BlockedPaths: blockedPaths})
	excluder := tool.NewWorkspaceExcluder(root, excludePaths, excludePatterns)
	return Env{WorkDir: root, PathPolicy: &policy, Excluder: &excluder}
}

// grepPatternRows runs grep for the fixture needle in files_with_matches mode
// and returns its rows.
func grepPatternRows(t *testing.T, env Env, searchPath string) []string {
	t.Helper()
	result := runGrepForTest(t, NewGrepTool(env), map[string]any{
		"path":        searchPath,
		"pattern":     "needle",
		"output_mode": "files_with_matches",
	})
	return outputRows(result.Output, "No matches found")
}

// globPatternRows runs the glob tool and returns its rows.
func globPatternRows(t *testing.T, env Env, searchPath, pattern string) []string {
	t.Helper()
	resultI, err := NewGlobTool(env).Handler(context.Background(), map[string]any{
		"path":    searchPath,
		"pattern": pattern,
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

// TestSymlinkSearchEntryEligibility proves grep and glob agree on which nested
// symlink entries they may treat as regular files, and that the lexical link
// name is what both return.
func TestSymlinkSearchEntryEligibility(t *testing.T) {
	tests := []struct {
		name            string
		excludePaths    []string
		excludePatterns []string
		blockedPaths    []string
		want            []string
	}{
		{
			name: "eligible regular file links only",
			want: []string{
				"inside.go",
				"link-absolute.go",
				"link-blocked.go",
				"link-byname.go",
				"link-excluded.go",
				"link-inside.go",
				"link-workspace.go",
			},
		},
		{
			name:            "excluded link name and excluded or blocked targets are rejected",
			excludePaths:    []string{"src/link-byname.go"},
			excludePatterns: []string{"excluded"},
			blockedPaths:    []string{"blocked"},
			want: []string{
				"inside.go",
				"link-absolute.go",
				"link-inside.go",
				"link-workspace.go",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := symlinkSearchFixture(t, tt.excludePaths, tt.excludePatterns, tt.blockedPaths)
			assertSameStringSet(t, "grep rows", grepPatternRows(t, env, "src"), tt.want)
			assertSameStringSet(t, "glob rows", globPatternRows(t, env, "src", "**/*.go"), tt.want)
		})
	}
}

// TestGrepSymlinkReadsResolvedTarget proves a searched link is read through its
// resolved target while the reported path stays the lexical link name, and that
// the scan counters account for linked files.
func TestGrepSymlinkReadsResolvedTarget(t *testing.T) {
	root := t.TempDir()
	mustWriteTree(t, root, "src/real.go", "inside only\n")
	mustWriteTree(t, root, "shared/target.go", "shared needle\n")
	mustWriteTree(t, root, "shared/payload.bin", grepCountersBinaryContent)
	mustSymlink(t, "../shared/target.go", filepath.Join(root, "src", "alias.go"))
	mustSymlink(t, "../shared/payload.bin", filepath.Join(root, "src", "payload.bin"))
	env := workspaceTestEnv(t, root, nil, nil)

	result := runGrepForTest(t, NewGrepTool(env), map[string]any{
		"path":        "src",
		"pattern":     "shared needle",
		"output_mode": "content",
	})
	if !strings.Contains(result.Output, "## alias.go") {
		t.Errorf("Output = %q, want the lexical link name as the file header", result.Output)
	}
	if strings.Contains(result.Output, "target.go") {
		t.Errorf("Output = %q, want the resolved target hidden behind the link name", result.Output)
	}
	if result.FilesSearched != 2 {
		t.Errorf("FilesSearched = %d, want 2 for the regular file and the linked file", result.FilesSearched)
	}
	if result.SkippedBinary != 1 {
		t.Errorf("SkippedBinary = %d, want 1 for the linked binary file", result.SkippedBinary)
	}

	assertSameStringSet(t, "glob rows", globPatternRows(t, env, "src", "*.bin"), []string{"payload.bin"})
}

// TestGrepNestedSymlinkUsesSandboxTmpRewrite proves a nested link into /tmp is
// read through the policy-resolved sandbox path instead of the host file behind
// the link. The searches call grepSearch and globWalk directly because a
// workspace that itself lives under /tmp would be rewritten before the walk
// starts, which is unrelated to nested-link handling.
func TestGrepNestedSymlinkUsesSandboxTmpRewrite(t *testing.T) {
	hostDir, err := os.MkdirTemp("/tmp", "steiner-975-nested-link-")
	if err != nil {
		t.Skipf("cannot create directory under /tmp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(hostDir) })
	mustWriteTree(t, hostDir, "needle.txt", "host-needle\n")

	root := t.TempDir()
	sandboxTmp := filepath.Join(root, "sandbox-tmp")
	mustMkdir(t, sandboxTmp)
	policy := tool.NewPathPolicyWithSandbox(root, config.PathsConfig{}, sandboxTmp)

	// The sandbox copy holds different content from the host file, so a search
	// that reads the host target instead of the rewritten path cannot match.
	sandboxFile := filepath.Join(sandboxTmp, filepath.Base(hostDir), "needle.txt")
	mustWriteTree(t, filepath.Dir(sandboxFile), filepath.Base(sandboxFile), "sandbox-needle\n")

	src := filepath.Join(root, "src")
	mustMkdir(t, src)
	mustSymlink(t, filepath.Join(hostDir, "needle.txt"), filepath.Join(src, "tmplink.txt"))

	t.Run("grep reads the sandbox copy", func(t *testing.T) {
		results, stats, err := grepSearch(context.Background(), grepSearchParams{
			root:        src,
			displayPath: "src",
			pattern:     "sandbox-needle",
			policy:      &policy,
		})
		if err != nil {
			t.Fatalf("grepSearch: %v", err)
		}
		assertSameStringSet(t, "grep files", grepResultFiles(results), []string{"tmplink.txt"})
		if stats.filesSearched != 1 {
			t.Errorf("filesSearched = %d, want 1 for the linked file", stats.filesSearched)
		}

		hostResults, _, err := grepSearch(context.Background(), grepSearchParams{
			root:        src,
			displayPath: "src",
			pattern:     "host-needle",
			policy:      &policy,
		})
		if err != nil {
			t.Fatalf("grepSearch: %v", err)
		}
		assertSameStringSet(t, "host files", grepResultFiles(hostResults), nil)
	})

	t.Run("glob lists the link", func(t *testing.T) {
		matches, err := globWalk(src, src, "**/*.txt", tool.NewWorkspaceExcluder(root, nil, nil), &policy)
		if err != nil {
			t.Fatalf("globWalk: %v", err)
		}
		assertSameStringSet(t, "glob matches", matches, []string{"tmplink.txt"})
	})
}

// TestSymlinkSearchAppliesBuiltinExcludesOutsideWalkRoot covers a link under the
// search root that points at a workspace file inside a built-in excluded
// directory. The target has no walk-relative path, so the exclusion has to be
// evaluated against the workspace root; otherwise node_modules and .git would be
// bypassed. A search rooted inside such a directory keeps its own view, so links
// whose targets stay inside that root are still eligible.
func TestSymlinkSearchAppliesBuiltinExcludesOutsideWalkRoot(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{
		"src/visible.go",
		"node_modules/pkg/real.go",
		"node_modules/other/secret.go",
		".git/objects/secret.go",
	} {
		mustWriteTree(t, root, rel, "needle\n")
	}
	mustSymlink(t, "visible.go", filepath.Join(root, "src", "link-visible.go"))
	mustSymlink(t, "../node_modules/other/secret.go", filepath.Join(root, "src", "link-modules.go"))
	mustSymlink(t, "../.git/objects/secret.go", filepath.Join(root, "src", "link-git.go"))
	mustSymlink(t, "real.go", filepath.Join(root, "node_modules", "pkg", "alias.go"))
	env := workspaceTestEnv(t, root, nil, nil)

	wantUnderSearchRoot := []string{"link-visible.go", "visible.go"}
	assertSameStringSet(t, "grep rows", grepPatternRows(t, env, "src"), wantUnderSearchRoot)
	assertSameStringSet(t, "glob rows", globPatternRows(t, env, "src", "**/*.go"), wantUnderSearchRoot)

	wantUnderBuiltinDir := []string{"alias.go", "real.go"}
	assertSameStringSet(t, "grep rows under built-in dir", grepPatternRows(t, env, "node_modules/pkg"), wantUnderBuiltinDir)
	assertSameStringSet(t, "glob rows under built-in dir", globPatternRows(t, env, "node_modules/pkg", "**/*.go"), wantUnderBuiltinDir)
}

// TestSymlinkSearchUsesLexicalWorkspaceForConfiguredExcludes proves configured
// exclusions still match a link target outside the search root when the
// workspace root is a symlink alias. The excluder is anchored at the path the
// caller configured, while the link target resolves to the alias's real
// location, so the target has to be mapped back into the configured view.
func TestSymlinkSearchUsesLexicalWorkspaceForConfiguredExcludes(t *testing.T) {
	realRoot := t.TempDir()
	for _, rel := range []string{"src/main.go", "shared/open.go", "secret/key.txt"} {
		mustWriteTree(t, realRoot, rel, "needle\n")
	}
	mustSymlink(t, "../secret/key.txt", filepath.Join(realRoot, "src", "link-secret.go"))
	mustSymlink(t, "../shared/open.go", filepath.Join(realRoot, "src", "link-open.go"))

	alias := filepath.Join(t.TempDir(), "workspace")
	mustSymlink(t, realRoot, alias)

	env := workspaceTestEnv(t, alias, []string{"secret"}, []string{"secret/*.txt"})
	want := []string{"link-open.go", "main.go"}
	assertSameStringSet(t, "grep rows", grepPatternRows(t, env, "src"), want)
	assertSameStringSet(t, "glob rows", globPatternRows(t, env, "src", "**/*.go"), want)
}

// TestSymlinkSearchUsesVirtualTmpBaseForExternalTargets proves a virtual /tmp
// walk applies configured patterns to a link target outside the walk root. The
// workspace view cannot describe such a target, so the pattern is matched
// against the target's virtual /tmp path instead.
func TestSymlinkSearchUsesVirtualTmpBaseForExternalTargets(t *testing.T) {
	root := t.TempDir()
	sandboxTmp := filepath.Join(root, "sandbox-tmp")
	session := filepath.Join(sandboxTmp, "s1")
	mustWriteTree(t, session, "report.txt", "needle\n")
	mustWriteTree(t, sandboxTmp, "other/app.txt", "needle\n")
	mustWriteTree(t, sandboxTmp, "other/allowed.txt", "needle\n")
	mustSymlink(t, "../other/app.txt", filepath.Join(session, "link-app.txt"))
	mustSymlink(t, "../other/allowed.txt", filepath.Join(session, "link-allowed.txt"))

	policy := tool.NewPathPolicyWithSandbox(root, config.PathsConfig{}, sandboxTmp).WithVirtualTmpRequest("/tmp")
	excluder := tool.NewWorkspaceExcluder(root, nil, []string{"app.txt"})

	matches, err := globWalk(session, session, "**/*.txt", excluder, &policy)
	if err != nil {
		t.Fatalf("globWalk: %v", err)
	}
	want := []string{"link-allowed.txt", "report.txt"}
	assertSameStringSet(t, "glob matches", matches, want)

	results, _, err := grepSearch(context.Background(), grepSearchParams{
		root:        session,
		displayPath: "s1",
		pattern:     "needle",
		excluder:    &excluder,
		policy:      &policy,
	})
	if err != nil {
		t.Fatalf("grepSearch: %v", err)
	}
	assertSameStringSet(t, "grep files", grepResultFiles(results), want)
}
