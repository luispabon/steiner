package builtin

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
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
	results, err := grepSearch(context.Background(), grepSearchParams{
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
	results, err := grepSearch(context.Background(), grepSearchParams{
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
	_, err := grepSearch(context.Background(), grepSearchParams{
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
	matches, err := globWalk(root, "**/*.go", tool.NewWorkspaceExcluder(root, nil, nil), &policy)
	if err != nil {
		t.Fatalf("globWalk: %v", err)
	}
	assertSameStringSet(t, "glob matches", matches, []string{"visible.go"})
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

func TestLSTool_HidesBlockedEntries(t *testing.T) {
	root := t.TempDir()
	mustWriteTree(t, root, "visible.txt", "visible")
	mustWriteTree(t, root, "sub/keep.txt", "keep")
	mustWriteTree(t, root, "secrets/secret.txt", "secret")
	mustWriteTree(t, root, "secrets/deep/secret.txt", "secret")

	policy := tool.NewPathPolicy(root, config.PathsConfig{BlockedPaths: []string{"secrets"}})
	toolDef := NewLSTool(Env{WorkDir: root, PathPolicy: &policy})
	ctx := context.Background()

	t.Run("non-recursive listing hides blocked entries", func(t *testing.T) {
		resultI, err := toolDef.Handler(ctx, map[string]any{"path": "."})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := resultI.(*Result)
		if !ok {
			t.Fatalf("result type = %T, want *Result", resultI)
		}
		if strings.Contains(result.Output, "secrets") {
			t.Fatalf("Output leaks blocked entry: %q", result.Output)
		}
		if !strings.Contains(result.Output, "visible.txt") {
			t.Fatalf("Output missing visible.txt: %q", result.Output)
		}
	})

	t.Run("recursive listing prunes blocked directories", func(t *testing.T) {
		resultI, err := toolDef.Handler(ctx, map[string]any{"path": ".", "recursive": true})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := resultI.(*Result)
		if !ok {
			t.Fatalf("result type = %T, want *Result", resultI)
		}
		if strings.Contains(result.Output, "secrets") {
			t.Fatalf("Output leaks blocked descendant: %q", result.Output)
		}
		if !strings.Contains(result.Output, "sub/keep.txt") {
			t.Fatalf("Output missing sub/keep.txt: %q", result.Output)
		}
	})

	t.Run("unresolvable entries fail closed", func(t *testing.T) {
		loopRoot := t.TempDir()
		mustWriteTree(t, loopRoot, "visible.txt", "visible")
		mustSymlink(t, "alias", filepath.Join(loopRoot, "alias"))

		loopPolicy := tool.NewPathPolicy(loopRoot, config.PathsConfig{BlockedPaths: []string{"blocked"}})
		loopDef := NewLSTool(Env{WorkDir: loopRoot, PathPolicy: &loopPolicy})
		resultI, err := loopDef.Handler(ctx, map[string]any{"path": "."})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := resultI.(*Result)
		if !ok {
			t.Fatalf("result type = %T, want *Result", resultI)
		}
		if strings.Contains(result.Output, "alias") {
			t.Fatalf("Output leaks entry with unresolvable policy path: %q", result.Output)
		}
		if !strings.Contains(result.Output, "visible.txt") {
			t.Fatalf("Output missing visible.txt: %q", result.Output)
		}
	})
}

func TestLSTool_SymlinkRoot(t *testing.T) {
	root := t.TempDir()
	mustWriteTree(t, root, "real/a.txt", "a")
	mustWriteTree(t, root, "real/sub/b.txt", "b")
	mustSymlink(t, filepath.Join(root, "real"), filepath.Join(root, "link"))

	policy := tool.NewPathPolicy(root, config.PathsConfig{})
	toolDef := NewLSTool(Env{WorkDir: root, PathPolicy: &policy})
	ctx := context.Background()

	t.Run("non-recursive listing follows an explicit symlink root", func(t *testing.T) {
		resultI, err := toolDef.Handler(ctx, map[string]any{"path": "link"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := resultI.(*Result)
		if !ok {
			t.Fatalf("result type = %T, want *Result", resultI)
		}
		assertSameStringSet(t, "ls output lines", strings.Split(result.Output, "\n"), []string{"sub/", "a.txt"})
	})

	t.Run("recursive listing follows an explicit symlink root", func(t *testing.T) {
		resultI, err := toolDef.Handler(ctx, map[string]any{"path": "link", "recursive": true})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := resultI.(*Result)
		if !ok {
			t.Fatalf("result type = %T, want *Result", resultI)
		}
		assertSameStringSet(t, "ls output lines", strings.Split(result.Output, "\n"), []string{"a.txt", "sub/", "sub/b.txt"})
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

func TestLSTool_DeniesSymlinkRootIntoBlockedPath(t *testing.T) {
	root := t.TempDir()
	mustWriteTree(t, root, "secrets/secret.txt", "secret")
	mustSymlink(t, filepath.Join(root, "secrets"), filepath.Join(root, "link"))

	policy := tool.NewPathPolicy(root, config.PathsConfig{BlockedPaths: []string{"secrets"}})
	toolDef := NewLSTool(Env{WorkDir: root, PathPolicy: &policy})
	ctx := context.Background()

	for _, recursive := range []bool{false, true} {
		name := "non-recursive"
		if recursive {
			name = "recursive"
		}
		t.Run(name, func(t *testing.T) {
			_, err := toolDef.Handler(ctx, map[string]any{"path": "link", "recursive": recursive})
			var policyErr *tool.PathPolicyError
			if !errors.As(err, &policyErr) {
				t.Fatalf("error = %v, want PathPolicyError", err)
			}
		})
	}
}

func TestLSTool_UnparseableListingFailsClosed(t *testing.T) {
	root := t.TempDir()
	oddDir := filepath.Join(root, "odd\n\nname")
	if err := os.MkdirAll(oddDir, 0o755); err != nil {
		t.Skipf("filesystem rejects directory names containing blank lines: %v", err)
	}
	mustWriteTree(t, oddDir, "visible.txt", "visible")
	mustWriteTree(t, oddDir, "secrets/secret.txt", "secret")

	policy := tool.NewPathPolicy(root, config.PathsConfig{BlockedPaths: []string{"secrets"}})
	toolDef := NewLSTool(Env{WorkDir: root, PathPolicy: &policy})

	_, err := toolDef.Handler(context.Background(), map[string]any{"path": "odd\n\nname"})
	if err == nil {
		t.Fatal("ls returned an unfiltered listing instead of failing closed")
	}
	if strings.Contains(err.Error(), "secret") {
		t.Fatalf("error leaks entry names: %v", err)
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
	// handler wiring from resolveSearchRoot into glob and ls is covered by the
	// non-sandbox symlink-root tests above.

	t.Run("grep reads the sandbox copy", func(t *testing.T) {
		results, err := grepSearch(ctx, grepSearchParams{
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
		matches, err := globWalk(want, "**/*.txt", tool.NewWorkspaceExcluder(want, nil, nil), &policy)
		if err != nil {
			t.Fatalf("globWalk: %v", err)
		}
		assertSameStringSet(t, "glob matches", matches, []string{"sandbox.txt"})
	})

	t.Run("ls reads the sandbox copy", func(t *testing.T) {
		result, err := lsRecursive(ctx, want, defaultLSLimit, 0, tool.NewWorkspaceExcluder(want, nil, nil), &policy)
		if err != nil {
			t.Fatalf("lsRecursive: %v", err)
		}
		assertSameStringSet(t, "ls output lines", strings.Split(result.Output, "\n"), []string{"sandbox.txt"})
	})
}

func TestLSTool_ShowsSpecialFilesThatPolicyCannotRead(t *testing.T) {
	root := t.TempDir()
	mustWriteTree(t, root, "visible.txt", "visible")
	fifo := filepath.Join(root, "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo unsupported: %v", err)
	}
	secretsDir := filepath.Join(root, "secrets")
	mustMkdir(t, secretsDir)
	if err := syscall.Mkfifo(filepath.Join(secretsDir, "pipe"), 0o600); err != nil {
		t.Skipf("mkfifo unsupported: %v", err)
	}

	policy := tool.NewPathPolicy(root, config.PathsConfig{BlockedPaths: []string{"secrets"}})
	toolDef := NewLSTool(Env{WorkDir: root, PathPolicy: &policy})
	ctx := context.Background()

	t.Run("non-recursive listing shows special files", func(t *testing.T) {
		resultI, err := toolDef.Handler(ctx, map[string]any{"path": "."})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := resultI.(*Result)
		if !ok {
			t.Fatalf("result type = %T, want *Result", resultI)
		}
		assertSameStringSet(t, "ls output lines", strings.Split(result.Output, "\n"), []string{"pipe", "visible.txt"})
	})

	t.Run("recursive listing shows special files", func(t *testing.T) {
		resultI, err := toolDef.Handler(ctx, map[string]any{"path": ".", "recursive": true})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := resultI.(*Result)
		if !ok {
			t.Fatalf("result type = %T, want *Result", resultI)
		}
		assertSameStringSet(t, "ls output lines", strings.Split(result.Output, "\n"), []string{"pipe", "visible.txt"})
	})

	t.Run("glob keeps read semantics for matched special files", func(t *testing.T) {
		matches, err := globWalk(root, "*", tool.NewWorkspaceExcluder(root, nil, nil), &policy)
		if err != nil {
			t.Fatalf("globWalk: %v", err)
		}
		assertSameStringSet(t, "glob matches", matches, []string{"visible.txt"})
	})
}
