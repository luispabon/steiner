package builtin

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

// documentationWriteScope is the shape of a documentation-only write scope:
// documentation extensions plus extensionless documentation file names.
func documentationWriteScope() tool.WriteScope {
	return tool.WriteScope{
		Reason:     "test executor: writes are restricted to documentation files",
		Extensions: []string{".md", ".mdx", ".markdown", ".rst", ".adoc"},
		Basenames:  []string{"README", "CHANGELOG", "CONTRIBUTING"},
	}
}

// newScopedMutateExecutor wires the real mutate tool through a scoped executor.
func newScopedMutateExecutor(t *testing.T, root string) *tool.Executor {
	t.Helper()
	return newScopedMutateExecutorWithPaths(t, root, config.PathsConfig{ProjectRootOnly: true})
}

// newScopedMutateExecutorWithPaths is newScopedMutateExecutor with an explicit
// paths configuration.
func newScopedMutateExecutorWithPaths(t *testing.T, root string, paths config.PathsConfig) *tool.Executor {
	t.Helper()
	policy := tool.NewPathPolicy(root, paths)
	registry := tool.NewRegistry(NewMutateTool(Env{
		WorkDir:      root,
		PathPolicy:   &policy,
		FileObserved: func(string) bool { return true },
	}))
	return tool.NewExecutor(registry, config.Config{
		Paths: paths,
	}, nil, root, "", tool.Unsandboxed{}).WithWriteScope(documentationWriteScope())
}

func executeMutate(t *testing.T, executor *tool.Executor, input map[string]any) (*MutateResult, error) {
	t.Helper()
	result, err := executor.Execute(context.Background(), "mutate", "", input)
	if err != nil {
		return nil, err
	}
	got, ok := result.(*MutateResult)
	if !ok {
		t.Fatalf("mutate result = %T, want *MutateResult", result)
	}
	return got, nil
}

func assertPolicyDenied(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("Execute(mutate) error = nil, want policy_denied")
	}
	var toolErr *tool.ToolExecutionError
	if !errors.As(err, &toolErr) {
		t.Fatalf("error = %v (%T), want *tool.ToolExecutionError", err, err)
	}
	if toolErr.Kind != "policy_denied" {
		t.Fatalf("error kind = %q, want policy_denied", toolErr.Kind)
	}
	if !strings.Contains(toolErr.Message, documentationWriteScope().Reason) {
		t.Fatalf("error message = %q, want scope reason", toolErr.Message)
	}
}

func TestMutateExecutorWriteScopeAllowsDocumentationFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs", "a"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	executor := newScopedMutateExecutor(t, root)

	result, err := executeMutate(t, executor, map[string]any{
		"operations": []any{
			map[string]any{"type": "create", "path": "README.md", "content": "# readme\n"},
			map[string]any{"type": "create", "path": "docs/a/b.md", "content": "# doc\n"},
			map[string]any{"type": "create", "path": "CHANGELOG", "content": "changes\n"},
		},
	})
	if err != nil {
		t.Fatalf("Execute(mutate) error = %v", err)
	}
	if result.OperationsFailed != 0 || result.OperationsApplied != 3 {
		t.Fatalf("result = %#v, want 3 applied, 0 failed", result)
	}
	assertContent(t, filepath.Join(root, "README.md"), "# readme\n")
	assertContent(t, filepath.Join(root, "docs", "a", "b.md"), "# doc\n")
	assertContent(t, filepath.Join(root, "CHANGELOG"), "changes\n")
}

// TestMutateExecutorWriteScopeSymlinkedRoot proves a scope root that is itself
// a symlink still permits a write through a link inside it: the planner
// re-checks the link's canonical target through the same policy.
func TestMutateExecutorWriteScopeSymlinkedRoot(t *testing.T) {
	base := t.TempDir()
	realRoot := filepath.Join(base, "real")
	linkedRoot := filepath.Join(base, "rootlink")
	if err := os.Mkdir(realRoot, 0o755); err != nil {
		t.Fatalf("mkdir real root: %v", err)
	}
	mustWrite(t, filepath.Join(realRoot, "real.md"), "doc\n")
	if err := os.Symlink("real.md", filepath.Join(realRoot, "alias.md")); err != nil {
		t.Fatalf("symlink alias: %v", err)
	}
	if err := os.Symlink(realRoot, linkedRoot); err != nil {
		t.Fatalf("symlink root: %v", err)
	}

	// projectRootOnly stays off so the scope, not the lexical root, decides:
	// the canonical target of alias.md lives outside the linked root's spelling.
	executor := newScopedMutateExecutorWithPaths(t, linkedRoot, config.PathsConfig{})

	result, err := executeMutate(t, executor, map[string]any{
		"operations": []any{
			map[string]any{"type": "write", "path": "alias.md", "content": "new doc\n"},
		},
	})
	if err != nil {
		t.Fatalf("Execute(mutate) error = %v", err)
	}
	if result.OperationsApplied != 1 || result.OperationsFailed != 0 {
		t.Fatalf("result = %#v, want 1 applied, 0 failed", result)
	}
	assertContent(t, filepath.Join(realRoot, "real.md"), "new doc\n")
}

// TestMutateExecutorWriteScopeDeniesLinkFromOutside proves operations spelled
// outside the scope root are denied even when the path resolves into scope,
// which would otherwise let a delete or move remove the outside link itself.
func TestMutateExecutorWriteScopeDeniesLinkFromOutside(t *testing.T) {
	tests := []struct {
		name   string
		opType string
		source string
	}{
		{name: "delete through outside link", opType: "delete_file", source: "link.md"},
		{name: "move source outside link", opType: "move", source: "link.md"},
		{name: "write through outside link", opType: "write", source: "link.md"},
		{name: "create under outside directory link", opType: "create", source: "dirlink/new.md"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			outside := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
				t.Fatalf("mkdir docs: %v", err)
			}
			target := filepath.Join(root, "docs", "a.md")
			mustWrite(t, target, "doc\n")
			link := filepath.Join(outside, "link.md")
			if err := os.Symlink(target, link); err != nil {
				t.Fatalf("symlink link.md: %v", err)
			}
			if err := os.Symlink(filepath.Join(root, "docs"), filepath.Join(outside, "dirlink")); err != nil {
				t.Fatalf("symlink dirlink: %v", err)
			}
			executor := newScopedMutateExecutorWithPaths(t, root, config.PathsConfig{})

			path := filepath.Join(outside, filepath.FromSlash(tc.source))
			op := map[string]any{"type": tc.opType, "path": path}
			switch tc.opType {
			case "move":
				op = map[string]any{"type": tc.opType, "from": path, "to": filepath.Join(outside, "moved.md")}
			case "write", "create":
				op["content"] = "blocked\n"
			}
			_, err := executeMutate(t, executor, map[string]any{"operations": []any{op}})
			assertPolicyDenied(t, err)

			assertContent(t, target, "doc\n")
			if _, statErr := os.Lstat(link); statErr != nil {
				t.Fatalf("outside link removed by denied %s: %v", tc.opType, statErr)
			}
			if _, statErr := os.Stat(filepath.Join(outside, "moved.md")); !os.IsNotExist(statErr) {
				t.Fatalf("move destination exists after denial, stat err = %v", statErr)
			}
			if _, statErr := os.Stat(filepath.Join(outside, "dirlink", "new.md")); !os.IsNotExist(statErr) {
				t.Fatalf("file created through outside directory link, stat err = %v", statErr)
			}
			if _, statErr := os.Stat(filepath.Join(root, "docs", "new.md")); !os.IsNotExist(statErr) {
				t.Fatalf("in-scope file created through outside link, stat err = %v", statErr)
			}
		})
	}
}

func TestMutateExecutorWriteScopeDeniesOutOfScopeOperations(t *testing.T) {
	tests := []struct {
		name  string
		path  string
		setup func(t *testing.T, root string)
	}{
		{name: "go file", path: "x.go"},
		{name: "txt file", path: "docs/x.txt"},
		{name: "lowercase readme", path: "readme"},
		{name: "uppercase extension", path: "README.MD"},
		{
			name: "allowed name linking to forbidden file",
			path: "guide.md",
			setup: func(t *testing.T, root string) {
				t.Helper()
				mustWrite(t, filepath.Join(root, "core.go"), "package main\n")
				if err := os.Symlink(filepath.Join(root, "core.go"), filepath.Join(root, "guide.md")); err != nil {
					t.Fatalf("symlink: %v", err)
				}
			},
		},
		{
			name: "allowed name linking outside root",
			path: "escape.md",
			setup: func(t *testing.T, root string) {
				t.Helper()
				outside := t.TempDir()
				mustWrite(t, filepath.Join(outside, "outside.md"), "doc\n")
				if err := os.Symlink(filepath.Join(outside, "outside.md"), filepath.Join(root, "escape.md")); err != nil {
					t.Fatalf("symlink: %v", err)
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			if tc.setup != nil {
				tc.setup(t, root)
			}
			executor := newScopedMutateExecutor(t, root)

			_, err := executeMutate(t, executor, map[string]any{
				"operations": []any{
					map[string]any{"type": "create", "path": tc.path, "content": "blocked\n"},
				},
			})
			assertPolicyDenied(t, err)
			if tc.setup != nil {
				return
			}
			if _, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(tc.path))); !os.IsNotExist(statErr) {
				t.Fatalf("denied path exists after denial, stat err = %v", statErr)
			}
		})
	}
}

func TestMutateExecutorWriteScopeDeniesPathOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	executor := newScopedMutateExecutor(t, root)

	outsideDoc := filepath.Join(outside, "notes.md")
	_, err := executeMutate(t, executor, map[string]any{
		"operations": []any{
			map[string]any{"type": "create", "path": outsideDoc, "content": "doc\n"},
		},
	})
	assertPolicyDenied(t, err)
	if _, statErr := os.Stat(outsideDoc); !os.IsNotExist(statErr) {
		t.Fatalf("out-of-root file exists after denial, stat err = %v", statErr)
	}
}

func TestMutateExecutorWriteScopeMixedBatchAppliesNothing(t *testing.T) {
	root := t.TempDir()
	executor := newScopedMutateExecutor(t, root)

	_, err := executeMutate(t, executor, map[string]any{
		"operations": []any{
			map[string]any{"type": "create", "path": "README.md", "content": "# readme\n"},
			map[string]any{"type": "create", "path": "x.go", "content": "package main\n"},
		},
	})
	assertPolicyDenied(t, err)
	if _, statErr := os.Stat(filepath.Join(root, "README.md")); !os.IsNotExist(statErr) {
		t.Fatalf("in-scope file created despite mixed-batch denial, stat err = %v", statErr)
	}

	entries, readErr := os.ReadDir(root)
	if readErr != nil {
		t.Fatalf("read root: %v", readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("root entries after denied batch = %v, want none", entries)
	}
}

func TestMutateExecutorWriteScopeMoveEndpoints(t *testing.T) {
	tests := []struct {
		name      string
		from      string
		to        string
		wantAllow bool
	}{
		{name: "both endpoints allowed", from: "a.md", to: "b.md", wantAllow: true},
		{name: "destination out of scope", from: "a.md", to: "b.go", wantAllow: false},
		{name: "source out of scope", from: "a.go", to: "b.md", wantAllow: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			mustWrite(t, filepath.Join(root, "a.md"), "doc a\n")
			mustWrite(t, filepath.Join(root, "a.go"), "package main\n")
			executor := newScopedMutateExecutor(t, root)

			result, err := executeMutate(t, executor, map[string]any{
				"operations": []any{
					map[string]any{"type": "move", "from": tc.from, "to": tc.to},
				},
			})
			if !tc.wantAllow {
				assertPolicyDenied(t, err)
				assertContent(t, filepath.Join(root, "a.md"), "doc a\n")
				assertContent(t, filepath.Join(root, "a.go"), "package main\n")
				if _, statErr := os.Stat(filepath.Join(root, tc.to)); !os.IsNotExist(statErr) {
					t.Fatalf("destination %q exists after denial, stat err = %v", tc.to, statErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Execute(mutate) error = %v", err)
			}
			if result.OperationsApplied != 1 || result.OperationsFailed != 0 {
				t.Fatalf("result = %#v, want 1 applied", result)
			}
			assertContent(t, filepath.Join(root, "b.md"), "doc a\n")
		})
	}
}

func TestMutateExecutorWriteScopeTmpPathDenied(t *testing.T) {
	root := t.TempDir()
	sandboxTmp := t.TempDir()
	policy := tool.NewPathPolicyWithSandbox(root, config.PathsConfig{ProjectRootOnly: true}, sandboxTmp)
	registry := tool.NewRegistry(NewMutateTool(Env{
		WorkDir:      root,
		PathPolicy:   &policy,
		FileObserved: func(string) bool { return true },
	}))
	executor := tool.NewExecutor(registry, config.Config{
		Paths: config.PathsConfig{ProjectRootOnly: true},
	}, nil, root, sandboxTmp, tool.Unsandboxed{}).WithWriteScope(documentationWriteScope())

	_, err := executeMutate(t, executor, map[string]any{
		"operations": []any{
			map[string]any{"type": "create", "path": "/tmp/notes.md", "content": "doc\n"},
		},
	})
	assertPolicyDenied(t, err)
	if _, statErr := os.Stat(filepath.Join(sandboxTmp, "notes.md")); !os.IsNotExist(statErr) {
		t.Fatalf("sandbox tmp file created after denial, stat err = %v", statErr)
	}
}

// TestMutateHandlerWriteScopeRecheck proves the real handler re-resolves
// paths through the executor's effective policy: called directly with a scoped
// policy in context, a symlink whose canonical target is out of scope fails
// the operation with the scope reason and leaves the target untouched.
func TestMutateHandlerWriteScopeRecheck(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "core.go"), "package main\n")
	if err := os.Symlink(filepath.Join(root, "core.go"), filepath.Join(root, "guide.md")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	policy := tool.NewPathPolicy(root, config.PathsConfig{ProjectRootOnly: true}).
		WithWriteScope(documentationWriteScope())
	toolDef := NewMutateTool(Env{WorkDir: root, PathPolicy: &policy, FileObserved: func(string) bool { return true }})
	ctx := context.WithValue(context.Background(), tool.EffectivePolicyKey{}, &policy)

	result, err := toolDef.Handler(ctx, map[string]any{
		"operations": []any{
			map[string]any{"type": "create", "path": "guide.md", "content": "blocked\n"},
		},
	})
	if err != nil {
		t.Fatalf("mutate Handler() error = %v", err)
	}
	got, ok := result.(*MutateResult)
	if !ok {
		t.Fatalf("mutate Handler() result = %T, want *MutateResult", result)
	}
	if got.OperationsFailed != 1 || got.OperationsApplied != 0 {
		t.Fatalf("result = %#v, want 1 failed, 0 applied", got)
	}
	if !strings.Contains(got.Output, documentationWriteScope().Reason) {
		t.Fatalf("output = %q, want scope denial reason", got.Output)
	}
	assertContent(t, filepath.Join(root, "core.go"), "package main\n")
}

func TestMutateExecutorWriteScopeDeniesDirectoryEndpoints(t *testing.T) {
	tests := []struct {
		name   string
		source string
		target string
		opType string
	}{
		{name: "delete directory with markdown extension", opType: "delete_file", source: "docs.md"},
		{name: "delete extensionless README directory", opType: "delete_file", source: "README"},
		{name: "move directory source", opType: "move", source: "README", target: "CHANGELOG"},
		{name: "move destination directory", opType: "move", source: "guide.md", target: "docs.md"},
		{name: "create targeting markdown-named directory", opType: "create", source: "docs.md"},
		{name: "write targeting markdown-named directory", opType: "write", source: "docs.md"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			mustWrite(t, filepath.Join(root, "guide.md"), "doc\n")
			for _, dir := range []string{"docs.md", "README"} {
				if err := os.Mkdir(filepath.Join(root, dir), 0o755); err != nil {
					t.Fatalf("mkdir %s: %v", dir, err)
				}
				mustWrite(t, filepath.Join(root, dir, "inner.go"), "package inner\n")
			}
			executor := newScopedMutateExecutor(t, root)

			op := map[string]any{"type": tc.opType, "path": tc.source}
			switch tc.opType {
			case "move":
				op = map[string]any{"type": tc.opType, "from": tc.source, "to": tc.target}
			case "create", "write":
				op["content"] = "blocked\n"
			}
			_, err := executeMutate(t, executor, map[string]any{"operations": []any{op}})
			assertPolicyDenied(t, err)

			for _, dir := range []string{"docs.md", "README"} {
				if info, statErr := os.Stat(filepath.Join(root, dir)); statErr != nil || !info.IsDir() {
					t.Fatalf("directory %q missing or not a directory after denial, err = %v", dir, statErr)
				}
				assertContent(t, filepath.Join(root, dir, "inner.go"), "package inner\n")
			}
			assertContent(t, filepath.Join(root, "guide.md"), "doc\n")
			if _, statErr := os.Stat(filepath.Join(root, "CHANGELOG")); !os.IsNotExist(statErr) {
				t.Fatalf("move destination exists after denial, stat err = %v", statErr)
			}
		})
	}
}

func TestMutateExecutorWriteScopeAllowsDeletingDocumentationFile(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "README"), "readme\n")
	executor := newScopedMutateExecutor(t, root)

	result, err := executeMutate(t, executor, map[string]any{
		"operations": []any{
			map[string]any{"type": "delete_file", "path": "README"},
		},
	})
	if err != nil {
		t.Fatalf("Execute(mutate) error = %v", err)
	}
	if result.OperationsApplied != 1 || result.OperationsFailed != 0 {
		t.Fatalf("result = %#v, want 1 applied", result)
	}
	if _, statErr := os.Stat(filepath.Join(root, "README")); !os.IsNotExist(statErr) {
		t.Fatalf("README file still exists after delete, stat err = %v", statErr)
	}
}

func TestMutateExecutorWriteScopeMissingParentDirectory(t *testing.T) {
	root := t.TempDir()
	executor := newScopedMutateExecutor(t, root)

	result, err := executeMutate(t, executor, map[string]any{
		"operations": []any{
			map[string]any{"type": "create", "path": "docs/sub/new.md", "content": "doc\n"},
		},
	})
	if err != nil {
		t.Fatalf("Execute(mutate) error = %v, want handler-level failure", err)
	}
	if result.OperationsFailed != 1 || result.OperationsApplied != 0 {
		t.Fatalf("result = %#v, want 1 failed, 0 applied", result)
	}
	if !strings.Contains(result.Output, "bash mkdir -p") {
		t.Fatalf("output = %q, want bash mkdir -p suggestion", result.Output)
	}
	if _, statErr := os.Stat(filepath.Join(root, "docs")); !os.IsNotExist(statErr) {
		t.Fatalf("write scope created a parent directory, stat err = %v", statErr)
	}
}
