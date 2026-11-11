package builtin

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

// mustWriteTree writes content to root/rel, creating parent directories.
func mustWriteTree(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	mustMkdir(t, filepath.Dir(path))
	mustWrite(t, path, content)
}

func assertSameStringSet(t *testing.T, label string, got, want []string) {
	t.Helper()
	gotSorted := append([]string(nil), got...)
	wantSorted := append([]string(nil), want...)
	slices.Sort(gotSorted)
	slices.Sort(wantSorted)
	if !slices.Equal(gotSorted, wantSorted) {
		t.Fatalf("%s = %v, want %v", label, got, want)
	}
}

func grepResultFiles(results []grepFileResult) []string {
	files := make([]string, 0, len(results))
	for _, result := range results {
		files = append(files, result.file)
	}
	return files
}

func TestGrepSearch_PrunesBlockedDescendants(t *testing.T) {
	root := t.TempDir()
	mustWriteTree(t, root, "visible.txt", "needle\n")
	mustWriteTree(t, root, "secrets/secret.txt", "needle\n")
	mustWriteTree(t, root, "secrets/nested/deep.txt", "needle\n")

	policy := tool.NewPathPolicy(root, config.PathsConfig{BlockedPaths: []string{"secrets"}})
	results, _, err := grepSearch(context.Background(), grepSearchParams{
		root:        root,
		displayPath: root,
		pattern:     "needle",
		policy:      &policy,
	})
	if err != nil {
		t.Fatalf("grepSearch: %v", err)
	}
	assertSameStringSet(t, "grep files", grepResultFiles(results), []string{"visible.txt"})
}

func TestGrepSearch_SymlinkRoot(t *testing.T) {
	root := t.TempDir()
	mustWriteTree(t, root, "real/a.txt", "needle\n")
	mustWriteTree(t, root, "real/sub/b.txt", "needle\n")
	mustWriteTree(t, root, "outside/leak.txt", "needle\n")
	mustSymlink(t, filepath.Join(root, "outside"), filepath.Join(root, "real", "alias"))
	mustSymlink(t, filepath.Join(root, "real"), filepath.Join(root, "link"))

	policy := tool.NewPathPolicy(root, config.PathsConfig{})
	results, _, err := grepSearch(context.Background(), grepSearchParams{
		root:        filepath.Join(root, "link"),
		displayPath: "link",
		pattern:     "needle",
		policy:      &policy,
	})
	if err != nil {
		t.Fatalf("grepSearch: %v", err)
	}
	assertSameStringSet(t, "grep files", grepResultFiles(results), []string{"a.txt", "sub/b.txt"})
}

func TestGrepSearch_DeniesSymlinkRootIntoBlockedPath(t *testing.T) {
	root := t.TempDir()
	mustWriteTree(t, root, "secrets/secret.txt", "needle\n")
	mustSymlink(t, filepath.Join(root, "secrets"), filepath.Join(root, "link"))

	policy := tool.NewPathPolicy(root, config.PathsConfig{BlockedPaths: []string{"secrets"}})
	_, _, err := grepSearch(context.Background(), grepSearchParams{
		root:        filepath.Join(root, "link"),
		displayPath: "link",
		pattern:     "needle",
		policy:      &policy,
	})
	if err == nil {
		t.Fatal("grepSearch succeeded for a symlink root resolving into a blocked path")
	}
	var policyErr *tool.PathPolicyError
	if !errors.As(err, &policyErr) {
		t.Fatalf("error = %v, want PathPolicyError", err)
	}
}

func TestGlobWalk_PrunesBlockedDescendants(t *testing.T) {
	root := t.TempDir()
	mustWriteTree(t, root, "visible.go", "package visible\n")
	mustWriteTree(t, root, "secrets/secret.go", "package secret\n")
	mustWriteTree(t, root, "secrets/nested/deep.go", "package deep\n")

	policy := tool.NewPathPolicy(root, config.PathsConfig{BlockedPaths: []string{"secrets"}})
	result := runGlobWalk(t, globWalkParams{
		root:        root,
		lexicalRoot: root,
		pattern:     "**/*.go",
		excluder:    tool.NewWorkspaceExcluder(root, nil, nil),
		policy:      &policy,
		limit:       defaultGlobLimit,
	})
	assertSameStringSet(t, "glob matches", outputRows(result.Output, ""), []string{"visible.go"})
}

func TestGlobTool_SymlinkRoot(t *testing.T) {
	root := t.TempDir()
	mustWriteTree(t, root, "real/main.go", "package main\n")
	mustWriteTree(t, root, "real/sub/lib.go", "package lib\n")
	mustSymlink(t, filepath.Join(root, "real"), filepath.Join(root, "link"))

	policy := tool.NewPathPolicy(root, config.PathsConfig{})
	excluder := tool.NewWorkspaceExcluder(root, nil, nil)
	toolDef := NewGlobTool(Env{WorkDir: root, PathPolicy: &policy, Excluder: &excluder})

	resultI, err := toolDef.Handler(context.Background(), map[string]any{
		"path":    "link",
		"pattern": "**/*.go",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result, ok := resultI.(Result)
	if !ok {
		t.Fatalf("result type = %T, want Result", resultI)
	}
	assertSameStringSet(t, "glob output lines", strings.Split(result.Output, "\n"), []string{"main.go", "sub/lib.go"})
}

// TestGlobWalk_HidesBlockedEntries proves a blocked directory and its
// descendants never pass the walk, and that an entry whose policy path cannot
// be resolved is omitted.
func TestGlobWalk_HidesBlockedEntries(t *testing.T) {
	root := t.TempDir()
	mustWriteTree(t, root, "visible.txt", "visible")
	mustWriteTree(t, root, "sub/keep.txt", "keep")
	mustWriteTree(t, root, "secrets/secret.txt", "secret")
	mustWriteTree(t, root, "secrets/deep/secret.txt", "secret")

	policy := tool.NewPathPolicy(root, config.PathsConfig{BlockedPaths: []string{"secrets"}})
	result := runGlobWalk(t, globWalkParams{
		root:        root,
		lexicalRoot: root,
		pattern:     "**",
		excluder:    tool.NewWorkspaceExcluder(root, nil, nil),
		policy:      &policy,
		limit:       defaultGlobLimit,
	})
	assertSameStringSet(t, "glob matches", outputRows(result.Output, ""),
		[]string{"sub/", "sub/keep.txt", "visible.txt"})

	t.Run("unresolvable entries fail closed", func(t *testing.T) {
		loopRoot := t.TempDir()
		mustWriteTree(t, loopRoot, "visible.txt", "visible")
		mustSymlink(t, "alias", filepath.Join(loopRoot, "alias"))

		loopPolicy := tool.NewPathPolicy(loopRoot, config.PathsConfig{BlockedPaths: []string{"blocked"}})
		loopResult := runGlobWalk(t, globWalkParams{
			root:        loopRoot,
			lexicalRoot: loopRoot,
			pattern:     "**",
			excluder:    tool.NewWorkspaceExcluder(loopRoot, nil, nil),
			policy:      &loopPolicy,
			limit:       defaultGlobLimit,
		})
		assertSameStringSet(t, "glob matches", outputRows(loopResult.Output, ""), []string{"visible.txt"})
	})
}

func TestGlobTool_DeniesSymlinkRootIntoBlockedPath(t *testing.T) {
	root := t.TempDir()
	mustWriteTree(t, root, "secrets/secret.go", "package secret\n")
	mustSymlink(t, filepath.Join(root, "secrets"), filepath.Join(root, "link"))

	policy := tool.NewPathPolicy(root, config.PathsConfig{BlockedPaths: []string{"secrets"}})
	excluder := tool.NewWorkspaceExcluder(root, nil, nil)
	toolDef := NewGlobTool(Env{WorkDir: root, PathPolicy: &policy, Excluder: &excluder})

	_, err := toolDef.Handler(context.Background(), map[string]any{
		"path":    "link",
		"pattern": "**/*.go",
	})
	var policyErr *tool.PathPolicyError
	if !errors.As(err, &policyErr) {
		t.Fatalf("error = %v, want PathPolicyError", err)
	}
}

func TestResolveSearchRoot_AppliesSandboxTmpRewrite(t *testing.T) {
	realTmp, err := os.MkdirTemp("/tmp", "steiner-975-root-")
	if err != nil {
		t.Skipf("cannot create directory under /tmp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(realTmp) })
	mustWriteTree(t, realTmp, "leak.txt", "needle\n")

	target, err := filepath.EvalSymlinks(realTmp)
	if err != nil {
		t.Fatalf("resolve target: %v", err)
	}

	root := t.TempDir()
	sandboxTmp := filepath.Join(t.TempDir(), "sandbox-tmp")
	mustMkdir(t, sandboxTmp)
	policy := tool.NewPathPolicyWithSandbox(root, config.PathsConfig{}, sandboxTmp)

	want, err := policy.ResolveReadPath(target)
	if err != nil {
		t.Fatalf("resolve target through policy: %v", err)
	}
	if want == target {
		t.Skip("sandbox /tmp rewrite does not apply to symlink targets on this platform")
	}

	// Distinct content behind the rewritten sandbox path: a walk that lands
	// here finds sandbox.txt, a walk that escapes to the host finds leak.txt.
	mustWriteTree(t, want, "sandbox.txt", "needle\n")

	link := filepath.Join(root, "link")
	mustSymlink(t, realTmp, link)

	got, err := resolveSearchRoot(link, &policy)
	if err != nil {
		t.Fatalf("resolveSearchRoot: %v", err)
	}
	if got != want {
		t.Fatalf("resolveSearchRoot = %q, want policy-resolved %q", got, want)
	}

	ctx := context.Background()

	// The policy root and the sandbox tmp both live under /tmp here, so a
	// handler path like "link" would be rewritten before the symlink is
	// resolved. Exercise each tool on the resolved walk root instead; the
	// handler wiring from resolveSearchRoot into glob is covered by the
	// non-sandbox symlink-root tests above.

	t.Run("grep reads the sandbox copy", func(t *testing.T) {
		results, _, err := grepSearch(ctx, grepSearchParams{
			root:        link,
			displayPath: "link",
			pattern:     "needle",
			policy:      &policy,
		})
		if err != nil {
			t.Fatalf("grepSearch: %v", err)
		}
		assertSameStringSet(t, "grep files", grepResultFiles(results), []string{"sandbox.txt"})
	})

	t.Run("glob reads the sandbox copy", func(t *testing.T) {
		result := runGlobWalk(t, globWalkParams{
			root:        want,
			lexicalRoot: want,
			pattern:     "**/*.txt",
			excluder:    tool.NewWorkspaceExcluder(want, nil, nil),
			policy:      &policy,
			limit:       defaultGlobLimit,
		})
		assertSameStringSet(t, "glob matches", outputRows(result.Output, ""), []string{"sandbox.txt"})
	})
}

func TestGrepSearch_FileRootAppliesSandboxTmpRewrite(t *testing.T) {
	hostDir, err := os.MkdirTemp("/tmp", "steiner-975-file-root-")
	if err != nil {
		t.Skipf("cannot create directory under /tmp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(hostDir) })
	mustWriteTree(t, hostDir, "host.txt", "host-needle\n")

	target, err := filepath.EvalSymlinks(filepath.Join(hostDir, "host.txt"))
	if err != nil {
		t.Fatalf("resolve target: %v", err)
	}

	root := t.TempDir()
	sandboxTmp := filepath.Join(t.TempDir(), "sandbox-tmp")
	mustMkdir(t, sandboxTmp)
	policy := tool.NewPathPolicyWithSandbox(root, config.PathsConfig{}, sandboxTmp)

	want, err := policy.ResolveReadPath(target)
	if err != nil {
		t.Fatalf("resolve target through policy: %v", err)
	}
	if want == target {
		t.Skip("sandbox /tmp rewrite does not apply to symlink targets on this platform")
	}

	// Distinct content behind the rewritten sandbox path: reading the resolved
	// path finds sandbox.txt, reading through the link finds the host file.
	mustWriteTree(t, filepath.Dir(want), filepath.Base(want), "sandbox-needle\n")

	link := filepath.Join(root, "hostlink.txt")
	mustSymlink(t, target, link)

	results, _, err := grepSearch(context.Background(), grepSearchParams{
		root:        link,
		displayPath: "hostlink.txt",
		pattern:     "needle",
		policy:      &policy,
	})
	if err != nil {
		t.Fatalf("grepSearch: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("grep files = %v, want one file", grepResultFiles(results))
	}
	if results[0].file != "hostlink.txt" {
		t.Fatalf("grep file = %q, want %q", results[0].file, "hostlink.txt")
	}
	if len(results[0].matches) != 1 || results[0].matches[0].line != "sandbox-needle" {
		t.Fatalf("grep matches = %v, want the sandbox copy content", results[0].matches)
	}
}

// TestGlobWalk_ListsSpecialFiles proves a listing returns a special file by
// name without opening it, and that a blocked one stays hidden.
func TestGlobWalk_ListsSpecialFiles(t *testing.T) {
	root := t.TempDir()
	mustWriteTree(t, root, "visible.txt", "visible")
	fifo := filepath.Join(root, "pipe")
	mustMkfifo(t, fifo)
	secretsDir := filepath.Join(root, "secrets")
	mustMkdir(t, secretsDir)
	mustMkfifo(t, filepath.Join(secretsDir, "pipe"))

	policy := tool.NewPathPolicy(root, config.PathsConfig{BlockedPaths: []string{"secrets"}})
	result := runGlobWalk(t, globWalkParams{
		root:        root,
		lexicalRoot: root,
		pattern:     "**",
		excluder:    tool.NewWorkspaceExcluder(root, nil, nil),
		policy:      &policy,
		limit:       defaultGlobLimit,
	})
	assertSameStringSet(t, "glob matches", outputRows(result.Output, ""), []string{"pipe", "visible.txt"})
}
