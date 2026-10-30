package tool

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
)

// testDocScope mirrors the shape of a documentation-only write scope: an
// extension set plus extensionless basenames, with a caller-supplied reason.
func testDocScope() WriteScope {
	return WriteScope{
		Reason:     "test scope: writes are restricted to documentation files",
		Extensions: []string{".md", ".mdx", ".markdown", ".rst", ".adoc"},
		Basenames:  []string{"README", "CHANGELOG", "CONTRIBUTING"},
	}
}

func TestWriteScopeAllows(t *testing.T) {
	scope := testDocScope()
	tests := []struct {
		name string
		path string
		want bool
	}{
		{name: "markdown extension", path: "README.md", want: true},
		{name: "nested markdown", path: "docs/a/b.md", want: true},
		{name: "mdx extension", path: "docs/page.mdx", want: true},
		{name: "markdown extension", path: "docs/page.markdown", want: true},
		{name: "rst extension", path: "docs/page.rst", want: true},
		{name: "adoc extension", path: "docs/page.adoc", want: true},
		{name: "extensionless README", path: "README", want: true},
		{name: "nested CHANGELOG", path: "docs/CHANGELOG", want: true},
		{name: "nested CONTRIBUTING", path: "CONTRIBUTING", want: true},
		{name: "multi-dot name keeps final extension", path: "docs/page.draft.md", want: true},
		{name: "hidden file with stem", path: ".hidden.md", want: true},
		{name: "dot only plus extension", path: ".md", want: false},
		{name: "double dot plus extension", path: "..md", want: false},
		{name: "nested dot only plus extension", path: "docs/.md", want: false},
		{name: "lowercase readme", path: "readme", want: false},
		{name: "uppercase extension", path: "README.MD", want: false},
		{name: "txt extension", path: "README.txt", want: false},
		{name: "markdown-looking middle segment", path: "x.md.go", want: false},
		{name: "go file under markdown-named directory", path: "docs.md/x.go", want: false},
		{name: "directory component does not grant eligibility", path: "docs/README/x.go", want: false},
		{name: "go extension", path: "main.go", want: false},
		{name: "txt extension on nested path", path: "docs/notes.txt", want: false},
		{name: "trailing separator", path: "docs/", want: false},
		{name: "root", path: "/", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := scope.allows(tc.path); got != tc.want {
				t.Fatalf("allows(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

func TestWriteScopeActive(t *testing.T) {
	if (WriteScope{Reason: "only a reason"}).active() {
		t.Fatal("scope with no rules reported active")
	}
	if !(WriteScope{Extensions: []string{".md"}}).active() {
		t.Fatal("scope with extensions reported inactive")
	}
	if !(WriteScope{Basenames: []string{"README"}}).active() {
		t.Fatal("scope with basenames reported inactive")
	}
}

func TestPathPolicyWriteScope(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs", "nested"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "docs.md"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	policy := NewPathPolicy(root, config.PathsConfig{ProjectRootOnly: true}).WithWriteScope(testDocScope())

	tests := []struct {
		name      string
		path      string
		wantAllow bool
	}{
		{name: "root-level markdown", path: filepath.Join(root, "README.md"), wantAllow: true},
		{name: "nested markdown missing leaf", path: filepath.Join(root, "docs", "a", "b.md"), wantAllow: true},
		{name: "extensionless CHANGELOG", path: filepath.Join(root, "CHANGELOG"), wantAllow: true},
		{name: "relative markdown", path: "docs/new.md", wantAllow: true},
		{name: "go file", path: filepath.Join(root, "x.go"), wantAllow: false},
		{name: "txt file", path: filepath.Join(root, "docs", "x.txt"), wantAllow: false},
		{name: "go file under markdown-named directory", path: filepath.Join(root, "docs.md", "x.go"), wantAllow: false},
		{name: "absolute path outside root", path: filepath.Join(outside, "a.md"), wantAllow: false},
		{name: "tmp path outside root", path: "/tmp/notes.md", wantAllow: false},
		{name: "parent traversal out of root", path: root + "/docs/../../x.md", wantAllow: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := policy.ResolvePath(tc.path, true)
			if tc.wantAllow {
				if err != nil {
					t.Fatalf("ResolvePath(%q) error = %v, want nil", tc.path, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ResolvePath(%q) = %q, want denial", tc.path, got)
			}
			assertScopeDenial(t, err, tc.path, testDocScope().Reason)
		})
	}
}

func TestPathPolicyWriteScopeSandboxTmpRewriteDenied(t *testing.T) {
	root := t.TempDir()
	sandboxTmp := t.TempDir()
	policy := NewPathPolicyWithSandbox(root, config.PathsConfig{}, sandboxTmp).WithWriteScope(testDocScope())
	if _, err := policy.ResolvePath("/tmp/notes.md", true); err == nil {
		t.Fatal("ResolvePath(/tmp/notes.md) = nil, want scope denial")
	} else {
		assertScopeDenial(t, err, "/tmp/notes.md", testDocScope().Reason)
	}
}

func TestPathPolicyWriteScopeSymlinks(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("doc\n"), 0o600); err != nil {
		t.Fatalf("write allowed file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "core.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatalf("write forbidden file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outside, "outside.md"), []byte("doc\n"), 0o600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}

	links := []struct {
		name   string
		target string
	}{
		{name: "notes.go", target: filepath.Join(root, "README.md")},
		{name: "guide.md", target: filepath.Join(root, "core.go")},
		{name: "escape.md", target: filepath.Join(outside, "outside.md")},
		{name: "alias.md", target: filepath.Join(root, "README.md")},
	}
	linkPaths := make(map[string]string, len(links))
	for _, link := range links {
		path := filepath.Join(root, link.name)
		if err := os.Symlink(link.target, path); err != nil {
			t.Fatalf("symlink %s: %v", link.name, err)
		}
		linkPaths[link.name] = path
	}

	policy := NewPathPolicy(root, config.PathsConfig{ProjectRootOnly: true}).WithWriteScope(testDocScope())

	tests := []struct {
		name      string
		path      string
		wantAllow bool
	}{
		{name: "forbidden-looking link to allowed file", path: linkPaths["notes.go"], wantAllow: false},
		{name: "allowed name linking to forbidden file", path: linkPaths["guide.md"], wantAllow: false},
		{name: "allowed name linking outside root", path: linkPaths["escape.md"], wantAllow: false},
		{name: "allowed name linking to allowed file", path: linkPaths["alias.md"], wantAllow: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := policy.ResolvePath(tc.path, true)
			if tc.wantAllow {
				if err != nil {
					t.Fatalf("ResolvePath(%q) error = %v, want nil", tc.path, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ResolvePath(%q) = nil, want scope denial", tc.path)
			}
			assertScopeDenial(t, err, tc.path, testDocScope().Reason)
		})
	}
}

// TestPathPolicyWriteScopeSymlinkedRoot proves a scope root that is itself a
// symlink accepts the canonical paths the mutate planner re-checks through the
// same policy.
func TestPathPolicyWriteScopeSymlinkedRoot(t *testing.T) {
	base := t.TempDir()
	realRoot := filepath.Join(base, "real")
	linkedRoot := filepath.Join(base, "rootlink")
	if err := os.Mkdir(realRoot, 0o755); err != nil {
		t.Fatalf("mkdir real root: %v", err)
	}
	if err := os.Symlink(realRoot, linkedRoot); err != nil {
		t.Fatalf("symlink root: %v", err)
	}
	realDoc := filepath.Join(realRoot, "real.md")
	if err := os.WriteFile(realDoc, []byte("doc\n"), 0o600); err != nil {
		t.Fatalf("write doc: %v", err)
	}
	policy := NewPathPolicy(linkedRoot, config.PathsConfig{}).WithWriteScope(testDocScope())

	if _, err := policy.ResolvePath(filepath.Join(linkedRoot, "real.md"), true); err != nil {
		t.Fatalf("ResolvePath(linked real.md) error = %v, want nil", err)
	}
	if _, err := policy.ResolvePath(realDoc, true); err != nil {
		t.Fatalf("ResolvePath(%q) error = %v, want nil", realDoc, err)
	}
	goTarget := filepath.Join(realRoot, "core.go")
	_, err := policy.ResolvePath(goTarget, true)
	if err == nil {
		t.Fatalf("ResolvePath(%q) = nil, want scope denial", goTarget)
	}
	assertScopeDenial(t, err, goTarget, testDocScope().Reason)
	outsideDoc := filepath.Join(t.TempDir(), "notes.md")
	if _, err := policy.ResolvePath(outsideDoc, true); err == nil {
		t.Fatalf("ResolvePath(%q) = nil, want scope denial", outsideDoc)
	} else {
		assertScopeDenial(t, err, outsideDoc, testDocScope().Reason)
	}
}

// TestPathPolicyWriteScopeDeniesDirectorySymlinkEscape proves a missing leaf
// under a directory symlink that leaves the scope root is denied.
func TestPathPolicyWriteScopeDeniesDirectorySymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "linkout")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	policy := NewPathPolicy(root, config.PathsConfig{ProjectRootOnly: true}).WithWriteScope(testDocScope())

	missing := filepath.Join(root, "linkout", "new.md")
	_, err := policy.ResolvePath(missing, true)
	if err == nil {
		t.Fatalf("ResolvePath(%q) = nil, want scope denial", missing)
	}
	assertScopeDenial(t, err, missing, testDocScope().Reason)
	if _, statErr := os.Stat(filepath.Join(outside, "new.md")); !os.IsNotExist(statErr) {
		t.Fatalf("escaped leaf exists after denial, stat err = %v", statErr)
	}
}

// TestPathPolicyWriteScopeDeniesLinkFromOutside proves a path spelled outside
// the scope root is denied even when it resolves to an in-scope file or
// directory.
func TestPathPolicyWriteScopeDeniesLinkFromOutside(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	target := filepath.Join(root, "docs", "a.md")
	if err := os.WriteFile(target, []byte("doc\n"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	link := filepath.Join(outside, "link.md")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink link.md: %v", err)
	}
	dirLink := filepath.Join(outside, "dirlink")
	if err := os.Symlink(filepath.Join(root, "docs"), dirLink); err != nil {
		t.Fatalf("symlink dirlink: %v", err)
	}
	policy := NewPathPolicy(root, config.PathsConfig{}).WithWriteScope(testDocScope())

	tests := []struct {
		name string
		path string
	}{
		{name: "write through outside file link", path: link},
		{name: "delete through outside file link", path: link},
		{name: "move source outside file link", path: link},
		{name: "missing leaf under outside directory link", path: filepath.Join(dirLink, "new.md")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := policy.ResolvePath(tc.path, true)
			if err == nil {
				t.Fatalf("ResolvePath(%q) = nil, want scope denial", tc.path)
			}
			assertScopeDenial(t, err, tc.path, testDocScope().Reason)
		})
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("in-scope target missing after denials: %v", err)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("outside link missing after denials: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dirLink, "new.md")); !os.IsNotExist(err) {
		t.Fatalf("leaf visible through outside directory link, stat err = %v", err)
	}
}

// TestPathPolicyWriteScopeDeniesDanglingSymlink proves a dangling link is a
// non-promptable denial rather than a write through an unnamed path.
func TestPathPolicyWriteScopeDeniesDanglingSymlink(t *testing.T) {
	root := t.TempDir()
	link := filepath.Join(root, "dangling.md")
	if err := os.Symlink(filepath.Join(root, "nowhere.go"), link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	policy := NewPathPolicy(root, config.PathsConfig{ProjectRootOnly: true}).WithWriteScope(testDocScope())

	_, err := policy.ResolvePath(link, true)
	if err == nil {
		t.Fatalf("ResolvePath(%q) = nil, want denial", link)
	}
	var policyErr *PathPolicyError
	if !errors.As(err, &policyErr) {
		t.Fatalf("error = %v (%T), want *PathPolicyError", err, err)
	}
	if policyErr.Promptable {
		t.Fatal("PathPolicyError.Promptable = true for a dangling link, want false")
	}
	if !strings.Contains(policyErr.Reason, "resolve write scope path") {
		t.Fatalf("PathPolicyError.Reason = %q, want resolution denial", policyErr.Reason)
	}
}

func TestPathPolicyWriteScopeDeniesDirectories(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"docs.md", "README"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
	}
	// A link whose own name is allowed and whose resolved target name is
	// allowed, but which points at a directory.
	if err := os.Mkdir(filepath.Join(root, "README-target"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink(filepath.Join(root, "README-target"), filepath.Join(root, "README.md")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	policy := NewPathPolicy(root, config.PathsConfig{ProjectRootOnly: true}).WithWriteScope(testDocScope())

	for _, name := range []string{"docs.md", "README", "README.md"} {
		path := filepath.Join(root, name)
		t.Run(name, func(t *testing.T) {
			if _, err := policy.ResolvePath(path, true); err == nil {
				t.Fatalf("ResolvePath(%q) = nil, want directory scope denial", path)
			} else {
				assertScopeDenial(t, err, path, testDocScope().Reason)
			}
			if _, err := policy.ResolvePath(path, false); err != nil {
				t.Fatalf("ResolvePath(%q, read) error = %v, want nil", path, err)
			}
		})
	}
}

func TestPathPolicyWriteScopeSurvivesWithoutRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	policy := NewPathPolicy(root, config.PathsConfig{ProjectRootOnly: true}).WithWriteScope(testDocScope())
	if _, err := policy.ResolvePath(filepath.Join(outside, "notes.md"), true); err == nil {
		t.Fatal("ResolvePath(outside notes.md) = nil under scope, want denial")
	}

	relaxed := policy.WithoutRoot()
	outsideDoc := filepath.Join(outside, "notes.md")
	if _, err := relaxed.ResolvePath(outsideDoc, true); err == nil {
		t.Fatalf("WithoutRoot ResolvePath(%q) = nil, want scope denial", outsideDoc)
	} else {
		assertScopeDenial(t, err, outsideDoc, testDocScope().Reason)
	}
	if _, err := relaxed.ResolvePath(filepath.Join(root, "x.go"), true); err == nil {
		t.Fatal("WithoutRoot ResolvePath(root x.go) = nil, want scope denial")
	}
	if _, err := relaxed.ResolvePath(filepath.Join(root, "README.md"), true); err != nil {
		t.Fatalf("WithoutRoot ResolvePath(root README.md) error = %v, want nil", err)
	}
}

func TestPathPolicyWriteScopeSurvivesRestrictWritesTo(t *testing.T) {
	root := t.TempDir()
	policy := NewPathPolicy(root, config.PathsConfig{ProjectRootOnly: true}).WithWriteScope(testDocScope())
	restricted := policy.RestrictWritesTo(filepath.Join(root, "docs"))

	if _, err := restricted.ResolvePath(filepath.Join(root, "docs", "a.md"), true); err != nil {
		t.Fatalf("ResolvePath(docs/a.md) error = %v, want nil", err)
	}
	if _, err := restricted.ResolvePath(filepath.Join(root, "docs", "a.go"), true); err == nil {
		t.Fatal("ResolvePath(docs/a.go) = nil, want scope denial")
	} else {
		assertScopeDenial(t, err, filepath.Join(root, "docs", "a.go"), testDocScope().Reason)
	}
	// Outside the allowlist and inside the scope still fails the allowlist.
	if _, err := restricted.ResolvePath(filepath.Join(root, "README.md"), true); err == nil {
		t.Fatal("ResolvePath(root README.md) = nil, want plan allowlist denial")
	}
}

func TestPathPolicyWriteScopeFailsClosedWithoutRoot(t *testing.T) {
	policy := PathPolicy{}.WithWriteScope(testDocScope())
	dir := t.TempDir()
	_, err := policy.ResolvePath(filepath.Join(dir, "notes.md"), true)
	if err == nil {
		t.Fatal("ResolvePath on scope installed without root = nil, want denial")
	}
	assertScopeDenial(t, err, filepath.Join(dir, "notes.md"), testDocScope().Reason)
}

func TestPathPolicyWriteScopeClonesCollections(t *testing.T) {
	root := t.TempDir()
	extensions := []string{".md"}
	basenames := []string{"README"}
	scope := WriteScope{Reason: "scoped", Extensions: extensions, Basenames: basenames}
	policy := NewPathPolicy(root, config.PathsConfig{ProjectRootOnly: true}).WithWriteScope(scope)

	extensions[0] = ".go"
	basenames[0] = "Makefile"

	if _, err := policy.ResolvePath(filepath.Join(root, "x.go"), true); err == nil {
		t.Fatal("ResolvePath(x.go) = nil after caller mutated extensions, want denial")
	}
	if _, err := policy.ResolvePath(filepath.Join(root, "README"), true); err != nil {
		t.Fatalf("ResolvePath(README) error = %v after caller mutated basenames, want nil", err)
	}
}

func TestPathPolicyWriteScopeReadsUnaffected(t *testing.T) {
	root := t.TempDir()
	goFile := filepath.Join(root, "x.go")
	if err := os.WriteFile(goFile, []byte("package main\n"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	policy := NewPathPolicy(root, config.PathsConfig{ProjectRootOnly: true}).WithWriteScope(testDocScope())

	if _, err := policy.ResolveReadPath(goFile); err != nil {
		t.Fatalf("ResolveReadPath(x.go) error = %v, want nil", err)
	}
	if _, err := policy.ResolvePath(goFile, false); err != nil {
		t.Fatalf("ResolvePath(x.go, read) error = %v, want nil", err)
	}
	if _, err := policy.ResolvePath(goFile, true); err == nil {
		t.Fatal("ResolvePath(x.go, writable) = nil, want scope denial")
	}
}

func TestPathPolicyWriteScopeReadInputValidationUnaffected(t *testing.T) {
	root := t.TempDir()
	goFile := filepath.Join(root, "x.go")
	if err := os.WriteFile(goFile, []byte("package main\n"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	policy := NewPathPolicy(root, config.PathsConfig{ProjectRootOnly: true}).WithWriteScope(testDocScope())
	if _, err := policy.ValidateToolInput("read", map[string]any{"path": goFile}); err != nil {
		t.Fatalf("ValidateToolInput(read) error = %v, want nil", err)
	}
}

func TestPathPolicyWriteScopeDenialText(t *testing.T) {
	root := t.TempDir()
	denied := filepath.Join(root, "x.go")
	policy := NewPathPolicy(root, config.PathsConfig{}).WithWriteScope(WriteScope{Extensions: []string{".md"}})
	_, err := policy.ResolvePath(denied, true)
	if err == nil {
		t.Fatal("ResolvePath(x.go) = nil, want denial")
	}
	if !strings.Contains(err.Error(), "writes are restricted by tool policy") {
		t.Fatalf("error = %v, want fallback denial reason", err)
	}
	if !strings.Contains(err.Error(), denied) {
		t.Fatalf("error = %v, want the denied path %q in the message", err, denied)
	}
	var policyErr *PathPolicyError
	if !errors.As(err, &policyErr) {
		t.Fatalf("error type = %T, want *PathPolicyError", err)
	}
	if policyErr.Reason == "" {
		t.Fatal("PathPolicyError.Reason = empty, want scope reason")
	}

	// A configured reason stays the prefix of the denial text.
	custom := NewPathPolicy(root, config.PathsConfig{}).WithWriteScope(testDocScope())
	_, err = custom.ResolvePath(denied, true)
	if err == nil {
		t.Fatal("ResolvePath(x.go) with a configured reason = nil, want denial")
	}
	if !strings.Contains(err.Error(), testDocScope().Reason) {
		t.Fatalf("error = %v, want configured reason %q", err, testDocScope().Reason)
	}
	if !strings.Contains(err.Error(), denied) {
		t.Fatalf("error = %v, want the denied path %q in the message", err, denied)
	}
}

// assertScopeDenial asserts err is a non-promptable PathPolicyError whose
// reason starts with wantReason.
func assertScopeDenial(t *testing.T, err error, path, wantReason string) {
	t.Helper()
	var policyErr *PathPolicyError
	if !errors.As(err, &policyErr) {
		t.Fatalf("error = %v (%T), want *PathPolicyError", err, err)
	}
	if policyErr.Promptable {
		t.Fatalf("PathPolicyError.Promptable = true for %q, want false", path)
	}
	if !strings.HasPrefix(policyErr.Reason, wantReason) {
		t.Fatalf("PathPolicyError.Reason = %q, want prefix %q", policyErr.Reason, wantReason)
	}
}

// newWriteScopeExecutor builds an executor over a mutate-shaped handler that
// records the effective policy it received.
func newWriteScopeExecutor(t *testing.T, root, sandboxTmp string, mode config.ExecutionMode, approver ApprovalResponder) (*Executor, func() *PathPolicy) {
	t.Helper()
	var gotPolicy *PathPolicy
	reg := NewRegistry(ToolDef{
		Name: "mutate",
		Handler: func(ctx context.Context, _ map[string]any) (any, error) {
			if policy, ok := ctx.Value(EffectivePolicyKey{}).(*PathPolicy); ok {
				gotPolicy = policy
			}
			return "ok", nil
		},
	})
	cfg := config.Config{Paths: config.PathsConfig{ProjectRootOnly: true}}
	executor := NewExecutor(reg, cfg, approver, root, sandboxTmp, Unsandboxed{})
	if mode != "" {
		executor = executor.WithModeGetter(func() config.ExecutionMode { return mode })
	}
	return executor, func() *PathPolicy { return gotPolicy }
}

func writeScopeMutateInput(path string) map[string]any {
	return map[string]any{
		"operations": []any{
			map[string]any{"type": "create", "path": path, "content": "doc\n"},
		},
	}
}

func TestExecutorWriteScopeAllowsInScopeWrite(t *testing.T) {
	root := t.TempDir()
	executor, gotPolicy := newWriteScopeExecutor(t, root, "", "", nil)
	executor.WithWriteScope(testDocScope())

	path := filepath.Join(root, "docs", "guide.md")
	if _, err := executor.Execute(context.Background(), "mutate", "c1", writeScopeMutateInput(path)); err != nil {
		t.Fatalf("Execute(mutate) error = %v, want nil", err)
	}
	policy := gotPolicy()
	if policy == nil || !policy.writeScope.active() {
		t.Fatal("handler effective policy carries no write scope")
	}
	if policy.writeScopeRoot != root {
		t.Fatalf("handler effective policy scope root = %q, want %q", policy.writeScopeRoot, root)
	}
}

func TestExecutorWriteScopeDeniesWithoutApproval(t *testing.T) {
	root := t.TempDir()
	approver := &mockApprover{response: ApprovalResponse{Allow: true}}
	executor, _ := newWriteScopeExecutor(t, root, "", "", approver)
	executor.WithWriteScope(testDocScope())

	_, err := executor.Execute(context.Background(), "mutate", "c1", writeScopeMutateInput(filepath.Join(root, "x.go")))
	if err == nil {
		t.Fatal("Execute(mutate) error = nil, want policy_denied")
	}
	var toolErr *ToolExecutionError
	if !errors.As(err, &toolErr) || toolErr.Kind != "policy_denied" {
		t.Fatalf("error = %v, want ToolExecutionError{Kind: policy_denied}", err)
	}
	if !strings.Contains(toolErr.Message, testDocScope().Reason) {
		t.Fatalf("error message = %q, want scope reason", toolErr.Message)
	}
	if approver.called {
		t.Fatal("approver called for a write-scope denial, want non-promptable denial")
	}
}

func TestExecutorWriteScopeDeniesPathsOutsideRootWithoutApproval(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	approver := &mockApprover{response: ApprovalResponse{Allow: true}}
	executor, _ := newWriteScopeExecutor(t, root, "", "", approver)
	executor.WithWriteScope(testDocScope())

	_, err := executor.Execute(context.Background(), "mutate", "c1", writeScopeMutateInput(filepath.Join(outside, "notes.md")))
	if err == nil {
		t.Fatal("Execute(mutate) error = nil, want policy_denied")
	}
	if approver.called {
		t.Fatal("approver called for an out-of-root scoped write, want non-promptable denial")
	}
}

func TestExecutorWriteScopeEmptyLeavesWritesUnrestricted(t *testing.T) {
	root := t.TempDir()
	executor, gotPolicy := newWriteScopeExecutor(t, root, "", "", nil)
	executor.WithWriteScope(WriteScope{Reason: "unused"})

	if _, err := executor.Execute(context.Background(), "mutate", "c1", writeScopeMutateInput(filepath.Join(root, "x.go"))); err != nil {
		t.Fatalf("Execute(mutate) error = %v, want nil", err)
	}
	if policy := gotPolicy(); policy == nil {
		t.Fatal("handler received no effective policy")
	} else if policy.writeScope.active() {
		t.Fatal("empty scope was applied to the effective policy")
	}
}

func TestExecutorWithoutWriteScopeBehaviorUnchanged(t *testing.T) {
	root := t.TempDir()
	executor, gotPolicy := newWriteScopeExecutor(t, root, "", "", nil)
	if _, err := executor.Execute(context.Background(), "mutate", "c1", writeScopeMutateInput(filepath.Join(root, "x.go"))); err != nil {
		t.Fatalf("Execute(mutate) error = %v, want nil", err)
	}
	if policy := gotPolicy(); policy == nil {
		t.Fatal("handler received no effective policy")
	} else if policy.writeScope.active() {
		t.Fatal("effective policy has a write scope without WithWriteScope")
	}
}

func TestExecutorWriteScopeTmpRewriteStillDenied(t *testing.T) {
	root := t.TempDir()
	sandboxTmp := t.TempDir()
	executor, _ := newWriteScopeExecutor(t, root, sandboxTmp, "", nil)
	executor.WithWriteScope(testDocScope())

	_, err := executor.Execute(context.Background(), "mutate", "c1", writeScopeMutateInput("/tmp/notes.md"))
	if err == nil {
		t.Fatal("Execute(mutate) error = nil for /tmp doc path, want policy_denied")
	}
}

func TestExecutorWriteScopeIntersectsPlanMode(t *testing.T) {
	root := t.TempDir()
	executor, _ := newWriteScopeExecutor(t, root, "", config.ExecutionModePlan, nil)
	executor.WithWriteScope(testDocScope())

	// Inside the plan allowlist and inside the scope: allowed.
	planDoc := filepath.Join(config.PlanModeWritableDirs()[0], "notes.md")
	if _, err := executor.Execute(context.Background(), "mutate", "c1", writeScopeMutateInput(planDoc)); err != nil {
		t.Fatalf("Execute(mutate %q) error = %v, want nil", planDoc, err)
	}

	// Inside the scope but outside the plan allowlist: denied by plan mode.
	docsDoc := filepath.Join(root, "docs", "notes.md")
	_, err := executor.Execute(context.Background(), "mutate", "c2", writeScopeMutateInput(docsDoc))
	if err == nil {
		t.Fatalf("Execute(mutate %q) error = nil, want plan-mode denial", docsDoc)
	}
	if strings.Contains(err.Error(), testDocScope().Reason) {
		t.Fatalf("error = %v, want plan-mode denial, got scope denial", err)
	}

	// Inside the plan allowlist but outside the scope: denied by the scope.
	planGo := filepath.Join(config.PlanModeWritableDirs()[0], "notes.go")
	_, err = executor.Execute(context.Background(), "mutate", "c3", writeScopeMutateInput(planGo))
	if err == nil {
		t.Fatalf("Execute(mutate %q) error = nil, want scope denial", planGo)
	}
	if !strings.Contains(err.Error(), testDocScope().Reason) {
		t.Fatalf("error = %v, want scope denial reason", err)
	}
}

func TestExecutorWriteScopeClonesCollections(t *testing.T) {
	root := t.TempDir()
	executor, _ := newWriteScopeExecutor(t, root, "", "", nil)
	extensions := []string{".md"}
	executor.WithWriteScope(WriteScope{Reason: "scoped", Extensions: extensions})
	extensions[0] = ".go"

	if _, err := executor.Execute(context.Background(), "mutate", "c1", writeScopeMutateInput(filepath.Join(root, "notes.go"))); err == nil {
		t.Fatal("Execute(mutate notes.go) = nil after caller mutated extensions, want denial")
	}
	if _, err := executor.Execute(context.Background(), "mutate", "c2", writeScopeMutateInput(filepath.Join(root, "notes.md"))); err != nil {
		t.Fatalf("Execute(mutate notes.md) error = %v, want nil", err)
	}
}
